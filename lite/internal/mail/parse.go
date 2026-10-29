// Package mail parses raw RFC 5322 messages into the storage model, sanitizes HTML
// for preview and builds MIME messages from API payloads.
package mail

import (
	"bytes"
	"io"
	"mime"
	"net/mail"
	"strings"
	"time"

	"github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset" // register charset decoders
	gomail "github.com/emersion/go-message/mail"

	"github.com/phongbui83/maildev/internal/model"
)

type Parsed struct {
	Email       model.Email
	Attachments []model.Attachment
}

const maxBodyStore = 2 << 20 // 2 MiB per text/html body kept in columns

// Parse extracts headers, bodies and attachments. It never fails hard: an
// unparseable message still yields a record with the raw bytes kept.
func Parse(raw []byte) *Parsed {
	p := &Parsed{}
	e := &p.Email

	ent, err := message.Read(bytes.NewReader(raw))
	if err != nil && ent == nil {
		e.Subject = "(unparseable message)"
		e.TextBody = string(truncate(raw, maxBodyStore))
		return p
	}
	// message.Read may return an *UnknownCharsetError / UnknownEncodingError with a usable entity.
	hdr := gomail.Header{Header: ent.Header}

	e.Subject, _ = hdr.Subject()
	if e.Subject == "" {
		e.Subject = decodeHeader(ent.Header.Get("Subject"))
	}
	e.MessageID, _ = hdr.MessageID()
	if t, err := hdr.Date(); err == nil && !t.IsZero() {
		e.SentAt = &t
	} else if raw := ent.Header.Get("Date"); raw != "" {
		if t, err := mail.ParseDate(raw); err == nil {
			e.SentAt = &t
		}
	}
	e.From = addressList(hdr, "From")
	e.To = addressList(hdr, "To")
	e.Cc = addressList(hdr, "Cc")
	e.Bcc = addressList(hdr, "Bcc")

	e.SMIME = detectSMIME(ent)

	fields := ent.Header.Fields()
	for fields.Next() {
		v, err := fields.Text()
		if err != nil {
			v = fields.Value()
		}
		e.Headers = append(e.Headers, model.Header{Key: fields.Key(), Value: v})
	}

	walk(ent, p, 0)

	if e.TextBody == "" && e.HTMLBody != "" {
		e.TextBody = HTMLToText(e.HTMLBody)
	}
	return p
}

// detectSMIME classifies the top-level S/MIME structure without verifying or
// decrypting anything; clients fetching the raw message do that themselves.
func detectSMIME(ent *message.Entity) string {
	ctype, params, _ := ent.Header.ContentType()
	ctype = strings.ToLower(ctype)
	switch ctype {
	case "multipart/signed":
		if strings.Contains(strings.ToLower(params["protocol"]), "pkcs7-signature") {
			return "signed"
		}
	case "application/pkcs7-mime", "application/x-pkcs7-mime":
		switch strings.ToLower(params["smime-type"]) {
		case "enveloped-data", "authenveloped-data":
			return "encrypted"
		case "signed-data":
			return "signed-opaque"
		case "":
			return "encrypted"
		}
	}
	return ""
}

func decodeHeader(s string) string {
	dec := mime.WordDecoder{}
	out, err := dec.DecodeHeader(s)
	if err != nil {
		return s
	}
	return out
}

func addressList(h gomail.Header, key string) []model.Address {
	addrs, err := h.AddressList(key)
	if err != nil && len(addrs) == 0 {
		// Fallback: lenient parse of the raw value.
		raw := h.Get(key)
		if raw == "" {
			return nil
		}
		list, err := mail.ParseAddressList(decodeHeader(raw))
		if err != nil {
			return []model.Address{{Address: strings.TrimSpace(raw)}}
		}
		out := make([]model.Address, 0, len(list))
		for _, a := range list {
			out = append(out, model.Address{Name: a.Name, Address: a.Address})
		}
		return out
	}
	out := make([]model.Address, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, model.Address{Name: a.Name, Address: a.Address})
	}
	return out
}

func walk(ent *message.Entity, p *Parsed, depth int) {
	if depth > 20 {
		return
	}
	if mr := ent.MultipartReader(); mr != nil {
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				return
			}
			if err != nil {
				// Tolerate malformed parts (go-message returns the entity with an error for unknown charsets).
				if part == nil {
					return
				}
			}
			walk(part, p, depth+1)
		}
	}

	ctype, params, _ := ent.Header.ContentType()
	ctype = strings.ToLower(ctype)
	disp, dparams, _ := ent.Header.ContentDisposition()
	disp = strings.ToLower(disp)
	filename := dparams["filename"]
	if filename == "" {
		filename = params["name"]
	}
	contentID := strings.Trim(ent.Header.Get("Content-ID"), "<>")

	body, _ := io.ReadAll(ent.Body)

	isText := ctype == "text/plain" || ctype == "text/html"
	if isText && disp != "attachment" {
		s := string(truncate(body, maxBodyStore))
		if ctype == "text/html" {
			if p.Email.HTMLBody == "" {
				p.Email.HTMLBody = s
				return
			}
		} else if p.Email.TextBody == "" {
			p.Email.TextBody = s
			return
		}
		// Additional text parts become attachments so nothing is lost.
	}
	if ctype == "" && len(body) == 0 {
		return
	}
	if filename == "" {
		filename = defaultFilename(ctype, contentID)
	}
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	p.Attachments = append(p.Attachments, model.Attachment{
		Filename:    filename,
		ContentType: ctype,
		Size:        len(body),
		ContentID:   contentID,
		Inline:      disp == "inline" || contentID != "",
		Data:        body,
	})
}

func defaultFilename(ctype, cid string) string {
	ext := ""
	if exts, _ := mime.ExtensionsByType(ctype); len(exts) > 0 {
		ext = exts[0]
	}
	if cid != "" {
		return "inline-" + sanitizeName(cid) + ext
	}
	return "part" + ext
}

func sanitizeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "part"
	}
	return b.String()
}

func truncate(b []byte, n int) []byte {
	if len(b) > n {
		return b[:n]
	}
	return b
}

// NormalizeCRLF converts lone LF line endings to CRLF (RFC 5322 canonical form),
// which S/MIME verification and IMAP/POP3 clients expect. CRLF input is returned as is.
func NormalizeCRLF(b []byte) []byte {
	if !bytes.Contains(b, []byte("\n")) {
		return b
	}
	out := make([]byte, 0, len(b)+len(b)/40)
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' && (i == 0 || b[i-1] != '\r') {
			out = append(out, '\r')
		}
		out = append(out, b[i])
	}
	return out
}

// EnsureDate is used by API ingestion when the generated message lacks a Date header.
func EnsureDate(t *time.Time) time.Time {
	if t == nil {
		return time.Now()
	}
	return *t
}
