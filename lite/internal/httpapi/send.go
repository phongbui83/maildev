package httpapi

import (
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"

	"github.com/phongbui83/maildev/internal/ingest"
	mailpkg "github.com/phongbui83/maildev/internal/mail"
	"github.com/phongbui83/maildev/internal/model"
	"github.com/phongbui83/maildev/internal/store"
)

// credentialFromRequest authenticates provider-style API calls with the project's
// API key, accepted as "Authorization: Bearer <key>" or "X-API-Key: <key>".
func (s *Server) credentialFromRequest(r *http.Request) (*model.Credential, *model.Project, error) {
	key := r.Header.Get("X-API-Key")
	if key == "" {
		h := r.Header.Get("Authorization")
		if strings.HasPrefix(strings.ToLower(h), "bearer ") {
			key = strings.TrimSpace(h[7:])
		}
	}
	if key == "" {
		return nil, nil, store.ErrNotFound
	}
	cred, p, err := s.store.AuthenticateAPIKey(r.Context(), key)
	if err != nil {
		keyID := key
		if i := strings.LastIndex(key, "."); i > 0 {
			keyID = key[:i]
		}
		s.store.Audit(r.Context(), store.AuditParams{Actor: keyID, Action: "api_auth_failed", IP: clientIP(r), Protocol: sourceFor(r), Detail: map[string]any{"path": r.URL.Path}})
	}
	return cred, p, err
}

func sourceFor(r *http.Request) string {
	switch {
	case strings.HasPrefix(r.URL.Path, "/sendgrid") || strings.HasPrefix(r.URL.Path, "/v3/"):
		return model.SourceSendGrid
	case strings.HasPrefix(r.URL.Path, "/ses") || strings.HasPrefix(r.URL.Path, "/v2/"):
		return model.SourceSES
	}
	return model.SourceAPI
}

// capture builds and stores a message produced by one of the API adapters.
func (s *Server) capture(r *http.Request, cred *model.Credential, project *model.Project, source string, m *mailpkg.Message) (*model.Email, error) {
	raw, envTo, err := mailpkg.Build(m, s.cfg.Hostname)
	if err != nil {
		return nil, err
	}
	return s.ingest.Ingest(r.Context(), ingest.Envelope{
		Project: project, Credential: cred, Source: source, ClientIP: clientIP(r),
		EnvelopeFrom: m.From.Address, EnvelopeTo: envTo,
	}, raw)
}

func (s *Server) captureRaw(r *http.Request, cred *model.Credential, project *model.Project, source string, raw []byte, from string, to []string) (*model.Email, error) {
	return s.ingest.Ingest(r.Context(), ingest.Envelope{
		Project: project, Credential: cred, Source: source, ClientIP: clientIP(r), EnvelopeFrom: from, EnvelopeTo: to,
	}, raw)
}

// --- Generic JSON API: POST /api/v1/send ---

type genericSendRequest struct {
	From        string            `json:"from"`
	ReplyTo     string            `json:"reply_to"`
	To          []string          `json:"to"`
	Cc          []string          `json:"cc"`
	Bcc         []string          `json:"bcc"`
	Subject     string            `json:"subject"`
	Text        string            `json:"text"`
	HTML        string            `json:"html"`
	Headers     map[string]string `json:"headers"`
	Attachments []struct {
		Filename    string `json:"filename"`
		ContentType string `json:"content_type"`
		Content     string `json:"content"` // base64
		ContentID   string `json:"content_id"`
	} `json:"attachments"`
}

func parseList(list []string) ([]mail.Address, error) {
	out := make([]mail.Address, 0, len(list))
	for _, s := range list {
		for _, part := range splitAddresses(s) {
			a, err := mailpkg.ParseAddress(part)
			if err != nil {
				return nil, errors.New("invalid address: " + part)
			}
			out = append(out, a)
		}
	}
	return out, nil
}

func splitAddresses(s string) []string {
	if !strings.Contains(s, ",") || strings.Contains(s, "<") {
		return []string{s}
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (s *Server) handleGenericSend(w http.ResponseWriter, r *http.Request) {
	cred, project, err := s.credentialFromRequest(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid or missing API key")
		return
	}
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.HasPrefix(ct, "message/rfc822") || strings.HasPrefix(ct, "text/plain") {
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.cfg.MaxMessageBytes+1024))
		if err != nil {
			writeError(w, 413, "message too large")
			return
		}
		q := r.URL.Query()
		e, err := s.captureRaw(r, cred, project, model.SourceAPI, raw, q.Get("from"), q["to"])
		if err != nil {
			s.ingestError(w, err)
			return
		}
		writeJSON(w, 202, map[string]any{"id": e.ID, "project": project.Slug, "uid": e.UID})
		return
	}
	var req genericSendRequest
	if err := readJSON(r, &req, s.cfg.MaxMessageBytes*2); err != nil {
		writeError(w, 400, "invalid JSON body: "+err.Error())
		return
	}
	m := &mailpkg.Message{Subject: req.Subject, Text: req.Text, HTML: req.HTML, Headers: req.Headers}
	if req.From != "" {
		if m.From, err = mailpkg.ParseAddress(req.From); err != nil {
			writeError(w, 400, "invalid from address")
			return
		}
	}
	if req.ReplyTo != "" {
		a, err := mailpkg.ParseAddress(req.ReplyTo)
		if err != nil {
			writeError(w, 400, "invalid reply_to address")
			return
		}
		m.ReplyTo = []mail.Address{a}
	}
	if m.To, err = parseList(req.To); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if m.Cc, err = parseList(req.Cc); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if m.Bcc, err = parseList(req.Bcc); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if len(m.To)+len(m.Cc)+len(m.Bcc) == 0 {
		writeError(w, 400, "at least one recipient is required")
		return
	}
	for _, a := range req.Attachments {
		data, err := base64.StdEncoding.DecodeString(a.Content)
		if err != nil {
			writeError(w, 400, "attachment content must be base64")
			return
		}
		m.Attachments = append(m.Attachments, mailpkg.FileAttachment{Filename: a.Filename, ContentType: a.ContentType, ContentID: a.ContentID, Inline: a.ContentID != "", Data: data})
	}
	e, err := s.capture(r, cred, project, model.SourceAPI, m)
	if err != nil {
		s.ingestError(w, err)
		return
	}
	writeJSON(w, 202, map[string]any{"id": e.ID, "project": project.Slug, "uid": e.UID})
}

func (s *Server) ingestError(w http.ResponseWriter, err error) {
	if errors.Is(err, ingest.ErrTooLarge) {
		writeError(w, 413, "message too large")
		return
	}
	slog.Error("api ingest failed", "err", err)
	writeError(w, 500, "could not store message")
}
