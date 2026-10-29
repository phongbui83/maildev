package relay

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/phongbui83/maildev/internal/model"
)

// sendSendGrid rebuilds the captured message as a SendGrid v3 mail/send request
// (SendGrid cannot accept raw MIME). Secret is the SendGrid API key.
func sendSendGrid(ctx context.Context, cfg *model.RelayConfig, from string, e *model.Email) (*Result, error) {
	endpoint := strings.TrimRight(cfg.Endpoint, "/")
	if endpoint == "" {
		endpoint = "https://api.sendgrid.com"
	}
	type addr struct {
		Email string `json:"email"`
		Name  string `json:"name,omitempty"`
	}
	conv := func(list []model.Address) []addr {
		var out []addr
		for _, a := range list {
			if a.Address != "" {
				out = append(out, addr{Email: a.Address, Name: a.Name})
			}
		}
		return out
	}
	pers := map[string]any{}
	to := conv(e.To)
	if len(to) == 0 {
		for _, r := range Recipients(e) {
			to = append(to, addr{Email: r})
		}
	}
	pers["to"] = to
	if cc := conv(e.Cc); len(cc) > 0 {
		pers["cc"] = cc
	}
	if bcc := conv(e.Bcc); len(bcc) > 0 {
		pers["bcc"] = bcc
	}
	fromAddr := addr{Email: from}
	if cfg.FromOverride == "" && len(e.From) > 0 {
		fromAddr.Name = e.From[0].Name
	}
	var content []map[string]string
	if e.TextBody != "" {
		content = append(content, map[string]string{"type": "text/plain", "value": e.TextBody})
	}
	if e.HTMLBody != "" {
		content = append(content, map[string]string{"type": "text/html", "value": e.HTMLBody})
	}
	if len(content) == 0 {
		content = append(content, map[string]string{"type": "text/plain", "value": "(empty message)"})
	}
	payload := map[string]any{
		"personalizations": []any{pers},
		"from":             fromAddr,
		"subject":          e.Subject,
		"content":          content,
	}
	var atts []map[string]string
	for _, a := range e.Attachments {
		att := map[string]string{
			"content":     base64.StdEncoding.EncodeToString(a.Data),
			"type":        a.ContentType,
			"filename":    a.Filename,
			"disposition": "attachment",
		}
		if a.Inline && a.ContentID != "" {
			att["disposition"] = "inline"
			att["content_id"] = a.ContentID
		}
		atts = append(atts, att)
	}
	if len(atts) > 0 {
		payload["attachments"] = atts
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/v3/mail/send", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.Secret)
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("sendgrid request: %w", err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("sendgrid returned %d: %s", resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	return &Result{RemoteID: resp.Header.Get("X-Message-Id"), Detail: "accepted by SendGrid"}, nil
}
