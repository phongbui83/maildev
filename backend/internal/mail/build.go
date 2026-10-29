package mail

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"net/mail"
	"strings"
	"time"

	"github.com/emersion/go-message"
	gomail "github.com/emersion/go-message/mail"
	"github.com/google/uuid"
)

// Message is a provider-neutral description of an outbound message, produced by
// the SendGrid, SES and generic API adapters.
type Message struct {
	From        mail.Address
	ReplyTo     []mail.Address
	To          []mail.Address
	Cc          []mail.Address
	Bcc         []mail.Address
	Subject     string
	Text        string
	HTML        string
	Headers     map[string]string
	Attachments []FileAttachment
	Date        time.Time
}

type FileAttachment struct {
	Filename    string
	ContentType string
	ContentID   string
	Inline      bool
	Data        []byte
}

// Build renders the message as RFC 5322 bytes. The envelope recipients are returned
// so the caller can record them (Bcc is intentionally omitted from the headers).
func Build(m *Message, hostname string) (raw []byte, envelopeTo []string, err error) {
	if m.Date.IsZero() {
		m.Date = time.Now()
	}
	var h gomail.Header
	h.SetDate(m.Date)
	h.SetSubject(m.Subject)
	if m.From.Address != "" {
		h.SetAddressList("From", []*gomail.Address{{Name: m.From.Name, Address: m.From.Address}})
	}
	if len(m.To) > 0 {
		h.SetAddressList("To", toGo(m.To))
	}
	if len(m.Cc) > 0 {
		h.SetAddressList("Cc", toGo(m.Cc))
	}
	if len(m.ReplyTo) > 0 {
		h.SetAddressList("Reply-To", toGo(m.ReplyTo))
	}
	if _, ok := m.Headers["Message-Id"]; !ok {
		if _, ok := m.Headers["Message-ID"]; !ok {
			h.SetMessageID(uuid.NewString() + "@" + hostname)
		}
	}
	h.Set("MIME-Version", "1.0")
	for k, v := range m.Headers {
		h.Set(k, v)
	}
	for _, a := range m.To {
		envelopeTo = append(envelopeTo, a.Address)
	}
	for _, a := range m.Cc {
		envelopeTo = append(envelopeTo, a.Address)
	}
	for _, a := range m.Bcc {
		envelopeTo = append(envelopeTo, a.Address)
	}

	var buf bytes.Buffer
	hasAttachments := len(m.Attachments) > 0
	hasAlt := m.Text != "" && m.HTML != ""

	switch {
	case !hasAttachments && !hasAlt:
		// single part
		ct := "text/plain"
		body := m.Text
		if m.HTML != "" {
			ct = "text/html"
			body = m.HTML
		}
		h.SetContentType(ct, map[string]string{"charset": "utf-8"})
		h.Set("Content-Transfer-Encoding", "quoted-printable")
		w, err := message.CreateWriter(&buf, h.Header)
		if err != nil {
			return nil, nil, err
		}
		if _, err := io.WriteString(w, body); err != nil {
			return nil, nil, err
		}
		if err := w.Close(); err != nil {
			return nil, nil, err
		}
	default:
		mw, err := gomail.CreateWriter(&buf, h)
		if err != nil {
			return nil, nil, err
		}
		// mail.Writer produces multipart/mixed; put the alternative inside an inline writer.
		iw, err := mw.CreateInline()
		if err != nil {
			return nil, nil, err
		}
		if m.Text != "" {
			var th gomail.InlineHeader
			th.SetContentType("text/plain", map[string]string{"charset": "utf-8"})
			th.Set("Content-Transfer-Encoding", "quoted-printable")
			pw, err := iw.CreatePart(th)
			if err != nil {
				return nil, nil, err
			}
			_, _ = io.WriteString(pw, m.Text)
			_ = pw.Close()
		}
		if m.HTML != "" {
			var th gomail.InlineHeader
			th.SetContentType("text/html", map[string]string{"charset": "utf-8"})
			th.Set("Content-Transfer-Encoding", "quoted-printable")
			pw, err := iw.CreatePart(th)
			if err != nil {
				return nil, nil, err
			}
			_, _ = io.WriteString(pw, m.HTML)
			_ = pw.Close()
		}
		if err := iw.Close(); err != nil {
			return nil, nil, err
		}
		for _, a := range m.Attachments {
			var ah gomail.AttachmentHeader
			ct := a.ContentType
			if ct == "" {
				ct = mime.TypeByExtension(ext(a.Filename))
				if ct == "" {
					ct = "application/octet-stream"
				}
			}
			ah.SetContentType(ct, nil)
			ah.SetFilename(a.Filename)
			ah.Set("Content-Transfer-Encoding", "base64")
			if a.ContentID != "" {
				ah.Set("Content-ID", "<"+a.ContentID+">")
			}
			if a.Inline {
				ah.SetContentDisposition("inline", map[string]string{"filename": a.Filename})
			}
			aw, err := mw.CreateAttachment(ah)
			if err != nil {
				return nil, nil, err
			}
			_, _ = aw.Write(a.Data)
			_ = aw.Close()
		}
		if err := mw.Close(); err != nil {
			return nil, nil, err
		}
	}
	return buf.Bytes(), envelopeTo, nil
}

func toGo(list []mail.Address) []*gomail.Address {
	out := make([]*gomail.Address, 0, len(list))
	for _, a := range list {
		out = append(out, &gomail.Address{Name: a.Name, Address: a.Address})
	}
	return out
}

func ext(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i:]
	}
	return ""
}

// ParseAddress accepts "Name <addr>" or bare addresses leniently.
func ParseAddress(s string) (mail.Address, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return mail.Address{}, fmt.Errorf("empty address")
	}
	a, err := mail.ParseAddress(s)
	if err != nil {
		if strings.Contains(s, "@") && !strings.ContainsAny(s, "<> ") {
			return mail.Address{Address: s}, nil
		}
		return mail.Address{}, err
	}
	return *a, nil
}
