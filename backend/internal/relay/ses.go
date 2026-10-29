package relay

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/phongbui83/maildev/internal/model"
)

// sendSES posts the raw message to the SES v2 SendEmail API, signed with SigV4.
// Endpoint defaults to https://email.<region>.amazonaws.com; an override lets you
// target another MailDev instance or a VPC endpoint.
func sendSES(ctx context.Context, cfg *model.RelayConfig, from string, rcpts []string, raw []byte) (*Result, error) {
	endpoint := strings.TrimRight(cfg.Endpoint, "/")
	region := cfg.Region
	if endpoint == "" {
		endpoint = "https://email." + region + ".amazonaws.com"
	}
	if region == "" {
		region = "us-east-1"
	}
	body, _ := json.Marshal(map[string]any{
		"FromEmailAddress": from,
		"Destination":      map[string]any{"ToAddresses": rcpts},
		"Content":          map[string]any{"Raw": map[string]string{"Data": base64.StdEncoding.EncodeToString(raw)}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/v2/email/outbound-emails", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	signV4(req, body, cfg.AccessKeyID, cfg.Secret, region, "ses", time.Now().UTC())

	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("ses request: %w", err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("ses returned %d: %s", resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	var out struct {
		MessageId string `json:"MessageId"`
	}
	_ = json.Unmarshal(rb, &out)
	return &Result{RemoteID: out.MessageId, Detail: "accepted by SES"}, nil
}

// signV4 implements AWS Signature Version 4 for a single request (no SDK dependency).
func signV4(req *http.Request, body []byte, akid, secret, region, service string, now time.Time) {
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	payloadHash := sha256hex(body)
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	host := req.URL.Host
	req.Header.Set("Host", host)

	// Canonical headers: host, x-amz-content-sha256, x-amz-date (sorted, lowercase).
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalHeaders := "host:" + host + "\n" + "x-amz-content-sha256:" + payloadHash + "\n" + "x-amz-date:" + amzDate + "\n"
	canonicalURI := req.URL.EscapedPath()
	if canonicalURI == "" {
		canonicalURI = "/"
	}
	canonicalQuery := canonicalQueryString(req.URL.Query())
	canonicalRequest := strings.Join([]string{req.Method, canonicalURI, canonicalQuery, canonicalHeaders, signedHeaders, payloadHash}, "\n")

	scope := dateStamp + "/" + region + "/" + service + "/aws4_request"
	stringToSign := strings.Join([]string{"AWS4-HMAC-SHA256", amzDate, scope, sha256hex([]byte(canonicalRequest))}, "\n")

	kDate := hmacSHA256([]byte("AWS4"+secret), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))

	req.Header.Set("Authorization", fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s", akid, scope, signedHeaders, signature))
}

func canonicalQueryString(q url.Values) string {
	if len(q) == 0 {
		return ""
	}
	return strings.ReplaceAll(q.Encode(), "+", "%20")
}

func sha256hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}
