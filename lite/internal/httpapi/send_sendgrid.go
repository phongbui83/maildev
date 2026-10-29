package httpapi

import (
	"encoding/base64"
	"net/http"
	"net/mail"
	"strings"

	mailpkg "github.com/phongbui83/maildev/internal/mail"
	"github.com/phongbui83/maildev/internal/model"
)

// SendGrid v3 Mail Send API subset. Docs: https://docs.sendgrid.com/api-reference/mail-send/mail-send
type sgAddress struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}

func (a sgAddress) addr() mail.Address { return mail.Address{Name: a.Name, Address: a.Email} }

type sgPersonalization struct {
	To      []sgAddress       `json:"to"`
	Cc      []sgAddress       `json:"cc"`
	Bcc     []sgAddress       `json:"bcc"`
	Subject string            `json:"subject"`
	Headers map[string]string `json:"headers"`
}

type sgRequest struct {
	Personalizations []sgPersonalization `json:"personalizations"`
	From             sgAddress           `json:"from"`
	ReplyTo          *sgAddress          `json:"reply_to"`
	Subject          string              `json:"subject"`
	Content          []struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"content"`
	Attachments []struct {
		Content     string `json:"content"`
		Type        string `json:"type"`
		Filename    string `json:"filename"`
		Disposition string `json:"disposition"`
		ContentID   string `json:"content_id"`
	} `json:"attachments"`
	Headers    map[string]string `json:"headers"`
	TemplateID string            `json:"template_id"`
}

type sgError struct {
	Errors []struct {
		Message string `json:"message"`
		Field   string `json:"field,omitempty"`
	} `json:"errors"`
}

func sgFail(w http.ResponseWriter, status int, msg, field string) {
	var e sgError
	e.Errors = append(e.Errors, struct {
		Message string `json:"message"`
		Field   string `json:"field,omitempty"`
	}{msg, field})
	writeJSON(w, status, e)
}

func (s *Server) handleSendGrid(w http.ResponseWriter, r *http.Request) {
	cred, project, err := s.credentialFromRequest(r)
	if err != nil {
		sgFail(w, http.StatusUnauthorized, "The provided authorization grant is invalid, expired, or revoked", "")
		return
	}
	var req sgRequest
	if err := readJSON(r, &req, s.cfg.MaxMessageBytes*2); err != nil {
		sgFail(w, 400, "Bad Request: "+err.Error(), "")
		return
	}
	if req.From.Email == "" {
		sgFail(w, 400, "The from email does not contain a valid address.", "from.email")
		return
	}
	if len(req.Personalizations) == 0 {
		sgFail(w, 400, "The personalizations field is required and must have at least one personalization.", "personalizations")
		return
	}
	var text, html string
	for _, c := range req.Content {
		switch strings.ToLower(c.Type) {
		case "text/plain":
			text = c.Value
		case "text/html":
			html = c.Value
		}
	}
	if text == "" && html == "" && req.TemplateID != "" {
		text = "(SendGrid dynamic template " + req.TemplateID + ": content is rendered by SendGrid and not available in MailDev)"
	}
	var attachments []mailpkg.FileAttachment
	for _, a := range req.Attachments {
		data, err := base64.StdEncoding.DecodeString(a.Content)
		if err != nil {
			sgFail(w, 400, "The attachment content must be base64 encoded.", "attachments.content")
			return
		}
		attachments = append(attachments, mailpkg.FileAttachment{
			Filename: a.Filename, ContentType: a.Type, ContentID: a.ContentID,
			Inline: strings.EqualFold(a.Disposition, "inline"), Data: data,
		})
	}

	var lastID string
	for _, p := range req.Personalizations {
		m := &mailpkg.Message{From: req.From.addr(), Subject: req.Subject, Text: text, HTML: html, Attachments: attachments, Headers: map[string]string{}}
		if p.Subject != "" {
			m.Subject = p.Subject
		}
		for k, v := range req.Headers {
			m.Headers[k] = v
		}
		for k, v := range p.Headers {
			m.Headers[k] = v
		}
		if req.ReplyTo != nil && req.ReplyTo.Email != "" {
			m.ReplyTo = []mail.Address{req.ReplyTo.addr()}
		}
		for _, a := range p.To {
			m.To = append(m.To, a.addr())
		}
		for _, a := range p.Cc {
			m.Cc = append(m.Cc, a.addr())
		}
		for _, a := range p.Bcc {
			m.Bcc = append(m.Bcc, a.addr())
		}
		if len(m.To) == 0 {
			sgFail(w, 400, "The to array is required for all personalization objects, and must have at least one email object with a valid email address.", "personalizations.to")
			return
		}
		e, err := s.capture(r, cred, project, model.SourceSendGrid, m)
		if err != nil {
			s.ingestError(w, err)
			return
		}
		lastID = e.ID.String()
	}
	w.Header().Set("X-Message-Id", lastID)
	w.WriteHeader(http.StatusAccepted)
}
