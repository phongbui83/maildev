// Package smime verifies S/MIME signatures of captured messages against a trust
// store built from the company PKI (OpenBao ca_chain endpoints) and/or a PEM file.
// Encrypted messages are never decrypted here.
package smime

import (
	"bufio"
	"bytes"
	"context"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/quotedprintable"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-message/textproto"
	"github.com/smallstep/pkcs7"
)

// Trust is a refreshable certificate pool.
type Trust struct {
	urls        []string
	file        string
	systemRoots bool

	mu   sync.RWMutex
	pool *x509.CertPool
	n    int
}

func NewTrust(urls []string, file string, systemRoots bool) *Trust {
	return &Trust{urls: urls, file: file, systemRoots: systemRoots}
}

// Refresh rebuilds the pool. Failures of individual sources are logged; the
// previous pool is kept when nothing could be loaded.
func (t *Trust) Refresh(ctx context.Context) error {
	var pool *x509.CertPool
	if t.systemRoots {
		if sp, err := x509.SystemCertPool(); err == nil && sp != nil {
			pool = sp
		}
	}
	if pool == nil {
		pool = x509.NewCertPool()
	}
	n := 0
	var errs []error
	if t.file != "" {
		pem, err := os.ReadFile(t.file)
		if err != nil {
			errs = append(errs, err)
		} else if !pool.AppendCertsFromPEM(pem) {
			errs = append(errs, fmt.Errorf("%s: no certificates", t.file))
		} else {
			n++
		}
	}
	client := &http.Client{Timeout: 15 * time.Second}
	for _, u := range t.urls {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", u, err))
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode != 200 {
			errs = append(errs, fmt.Errorf("%s: http %d", u, resp.StatusCode))
			continue
		}
		if !pool.AppendCertsFromPEM(body) {
			errs = append(errs, fmt.Errorf("%s: no PEM certificates in response", u))
			continue
		}
		n++
	}
	if n == 0 && (t.file != "" || len(t.urls) > 0) {
		return fmt.Errorf("no S/MIME trust source loaded: %v", errors.Join(errs...))
	}
	for _, e := range errs {
		slog.Warn("smime trust source failed", "err", e)
	}
	t.mu.Lock()
	t.pool, t.n = pool, n
	t.mu.Unlock()
	slog.Info("smime trust store loaded", "sources", n, "urls", t.urls, "file", t.file)
	return nil
}

// RefreshLoop re-fetches the chains periodically (CA rotation) until ctx ends.
func (t *Trust) RefreshLoop(ctx context.Context, every time.Duration) {
	tk := time.NewTicker(every)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			if err := t.Refresh(ctx); err != nil {
				slog.Warn("smime trust refresh failed", "err", err)
			}
		}
	}
}

func (t *Trust) Pool() *x509.CertPool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.pool
}

// Result of a signature check.
type Result struct {
	Valid  bool
	Signer string // "Name <email>" or subject CN
	Error  string
	// Content is the inner MIME entity for opaque signatures (nil otherwise).
	Content []byte
}

// Verify checks the signature of a raw message whose top-level type was
// classified as "signed" (multipart/signed, detached) or "signed-opaque"
// (application/pkcs7-mime signed-data).
func Verify(raw []byte, kind string, trust *Trust, at time.Time) Result {
	var pool *x509.CertPool
	if trust != nil {
		pool = trust.Pool()
	}
	switch kind {
	case "signed":
		return verifyDetached(raw, pool, at)
	case "signed-opaque":
		return verifyOpaque(raw, pool, at)
	}
	return Result{Error: "not a signed message"}
}

func splitHeaderBody(raw []byte) (textproto.Header, []byte, error) {
	br := bufio.NewReader(bytes.NewReader(raw))
	h, err := textproto.ReadHeader(br)
	if err != nil {
		return textproto.Header{}, nil, err
	}
	rest, _ := io.ReadAll(br)
	return h, rest, nil
}

func decodeBody(h textproto.Header, body []byte) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(h.Get("Content-Transfer-Encoding"))) {
	case "base64":
		clean := strings.Map(func(r rune) rune {
			if r == '\r' || r == '\n' || r == ' ' || r == '\t' {
				return -1
			}
			return r
		}, string(body))
		return base64.StdEncoding.DecodeString(clean)
	case "quoted-printable":
		return io.ReadAll(quotedprintable.NewReader(bytes.NewReader(body)))
	}
	return body, nil
}

func verifyDetached(raw []byte, pool *x509.CertPool, at time.Time) Result {
	raw = normalizeCRLF(raw)
	h, body, err := splitHeaderBody(raw)
	if err != nil {
		return Result{Error: "unreadable headers"}
	}
	_, params, err := mime.ParseMediaType(h.Get("Content-Type"))
	if err != nil || params["boundary"] == "" {
		return Result{Error: "multipart/signed without boundary"}
	}
	delim := []byte("--" + params["boundary"])
	// Locate the first part: after the first delimiter line.
	i := bytes.Index(body, delim)
	if i < 0 {
		return Result{Error: "first boundary not found"}
	}
	start := i + len(delim)
	if nl := bytes.Index(body[start:], []byte("\n")); nl >= 0 {
		start += nl + 1
	}
	// The signed content ends right before the CRLF that precedes the next delimiter.
	j := bytes.Index(body[start:], append([]byte("\r\n"), delim...))
	if j < 0 {
		return Result{Error: "second boundary not found"}
	}
	content := body[start : start+j]
	sigStart := start + j + 2 + len(delim)
	if nl := bytes.Index(body[sigStart:], []byte("\n")); nl >= 0 {
		sigStart += nl + 1
	}
	sigPart := body[sigStart:]
	if k := bytes.Index(sigPart, append([]byte("\r\n"), delim...)); k >= 0 {
		sigPart = sigPart[:k]
	}
	sh, sbody, err := splitHeaderBody(sigPart)
	if err != nil {
		return Result{Error: "unreadable signature part"}
	}
	der, err := decodeBody(sh, sbody)
	if err != nil {
		return Result{Error: "signature part is not valid base64"}
	}
	p7, err := pkcs7.Parse(der)
	if err != nil {
		return Result{Error: "invalid PKCS#7 signature: " + err.Error()}
	}
	p7.Content = content
	return finish(p7, pool, at, nil)
}

func verifyOpaque(raw []byte, pool *x509.CertPool, at time.Time) Result {
	h, body, err := splitHeaderBody(raw)
	if err != nil {
		return Result{Error: "unreadable headers"}
	}
	der, err := decodeBody(h, body)
	if err != nil {
		return Result{Error: "body is not valid base64"}
	}
	p7, err := pkcs7.Parse(der)
	if err != nil {
		return Result{Error: "invalid PKCS#7 data: " + err.Error()}
	}
	r := finish(p7, pool, at, p7.Content)
	return r
}

func finish(p7 *pkcs7.PKCS7, pool *x509.CertPool, at time.Time, content []byte) Result {
	res := Result{Content: content}
	if signer := p7.GetOnlySigner(); signer != nil {
		res.Signer = describe(signer)
	}
	if pool == nil {
		res.Error = "no S/MIME trust store configured"
		return res
	}
	if err := p7.VerifyWithChainAtTime(pool, at); err != nil {
		res.Error = err.Error()
		return res
	}
	res.Valid = true
	return res
}

func describe(c *x509.Certificate) string {
	name := c.Subject.CommonName
	email := ""
	if len(c.EmailAddresses) > 0 {
		email = c.EmailAddresses[0]
	}
	switch {
	case name != "" && email != "" && name != email:
		return name + " <" + email + ">"
	case email != "":
		return email
	default:
		return name
	}
}

func normalizeCRLF(b []byte) []byte {
	out := make([]byte, 0, len(b)+len(b)/40)
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' && (i == 0 || b[i-1] != '\r') {
			out = append(out, '\r')
		}
		out = append(out, b[i])
	}
	return out
}
