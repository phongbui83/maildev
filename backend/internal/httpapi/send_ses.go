package httpapi

import (
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/mail"
	"regexp"
	"strings"

	"github.com/google/uuid"

	mailpkg "github.com/phongbui83/maildev/internal/mail"
	"github.com/phongbui83/maildev/internal/model"
	"github.com/phongbui83/maildev/internal/store"
)

// AWS SES compatibility. The SigV4 signature is not verified: the access key id
// found in the Authorization header selects the project. Point the SDK at
// "<public_url>/ses" (BaseEndpoint / endpoint_url) with the project's SES keys.

var sigv4CredRe = regexp.MustCompile(`Credential=([A-Z0-9]+)/`)

func (s *Server) sesCredential(r *http.Request) (*model.Credential, *model.Project, error) {
	akid := ""
	if m := sigv4CredRe.FindStringSubmatch(r.Header.Get("Authorization")); m != nil {
		akid = m[1]
	}
	if akid == "" {
		// SES v1 query API with pre-signed requests or test tools may pass the key explicitly.
		akid = r.FormValue("AWSAccessKeyId")
	}
	if akid == "" {
		return nil, nil, fmt.Errorf("missing credential")
	}
	cred, p, err := s.store.LookupSESKey(r.Context(), akid)
	if err != nil {
		s.store.Audit(r.Context(), store.AuditParams{Actor: akid, Action: "api_auth_failed", IP: clientIP(r), Protocol: model.SourceSES, Detail: map[string]any{"path": r.URL.Path}})
	}
	return cred, p, err
}

// --- SES v1 (query API): POST /ses with Action=SendEmail|SendRawEmail ---

type sesV1Response struct {
	XMLName   xml.Name
	MessageID string `xml:"Result>MessageId"`
	RequestID string `xml:"ResponseMetadata>RequestId"`
}

type sesV1Error struct {
	XMLName   xml.Name `xml:"ErrorResponse"`
	Type      string   `xml:"Error>Type"`
	Code      string   `xml:"Error>Code"`
	Message   string   `xml:"Error>Message"`
	RequestID string   `xml:"RequestId"`
}

func sesV1Fail(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(status)
	_ = xml.NewEncoder(w).Encode(sesV1Error{Type: "Sender", Code: code, Message: msg, RequestID: uuid.NewString()})
}

func (s *Server) handleSESv1(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		sesV1Fail(w, 400, "InvalidParameterValue", "could not parse form")
		return
	}
	cred, project, err := s.sesCredential(r)
	if err != nil {
		sesV1Fail(w, 403, "InvalidClientTokenId", "The security token included in the request is invalid.")
		return
	}
	action := r.FormValue("Action")
	var e *model.Email
	switch action {
	case "SendEmail":
		m := &mailpkg.Message{
			Subject: r.FormValue("Message.Subject.Data"),
			Text:    r.FormValue("Message.Body.Text.Data"),
			HTML:    r.FormValue("Message.Body.Html.Data"),
		}
		if src := r.FormValue("Source"); src != "" {
			if m.From, err = mailpkg.ParseAddress(src); err != nil {
				sesV1Fail(w, 400, "InvalidParameterValue", "invalid Source")
				return
			}
		}
		m.To = formMembers(r, "Destination.ToAddresses.member.")
		m.Cc = formMembers(r, "Destination.CcAddresses.member.")
		m.Bcc = formMembers(r, "Destination.BccAddresses.member.")
		m.ReplyTo = formMembers(r, "ReplyToAddresses.member.")
		if len(m.To)+len(m.Cc)+len(m.Bcc) == 0 {
			sesV1Fail(w, 400, "MissingParameter", "Destination is required")
			return
		}
		e, err = s.capture(r, cred, project, model.SourceSES, m)
	case "SendRawEmail":
		raw, derr := base64.StdEncoding.DecodeString(r.FormValue("RawMessage.Data"))
		if derr != nil {
			sesV1Fail(w, 400, "InvalidParameterValue", "RawMessage.Data must be base64")
			return
		}
		var to []string
		for _, a := range formMembers(r, "Destinations.member.") {
			to = append(to, a.Address)
		}
		e, err = s.captureRaw(r, cred, project, model.SourceSES, raw, r.FormValue("Source"), to)
	default:
		sesV1Fail(w, 400, "InvalidAction", "unsupported Action "+action+" (MailDev supports SendEmail and SendRawEmail)")
		return
	}
	if err != nil {
		sesV1Fail(w, 500, "InternalFailure", err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/xml")
	resp := sesV1Response{XMLName: xml.Name{Space: "http://ses.amazonaws.com/doc/2010-12-01/", Local: action + "Response"}, MessageID: e.ID.String(), RequestID: uuid.NewString()}
	// The Result element is named after the action.
	out, _ := xml.Marshal(resp)
	out = []byte(strings.Replace(string(out), "<Result>", "<"+action+"Result>", 1))
	out = []byte(strings.Replace(string(out), "</Result>", "</"+action+"Result>", 1))
	w.Write(out)
}

func formMembers(r *http.Request, prefix string) []mail.Address {
	var out []mail.Address
	for i := 1; ; i++ {
		v := r.FormValue(fmt.Sprintf("%s%d", prefix, i))
		if v == "" {
			break
		}
		a, err := mailpkg.ParseAddress(v)
		if err != nil {
			continue
		}
		out = append(out, a)
	}
	return out
}

// --- SES v2: POST /ses/v2/email/outbound-emails ---

type sesContentData struct {
	Data    string `json:"Data"`
	Charset string `json:"Charset"`
}

type sesV2Request struct {
	FromEmailAddress string `json:"FromEmailAddress"`
	Destination      struct {
		ToAddresses  []string `json:"ToAddresses"`
		CcAddresses  []string `json:"CcAddresses"`
		BccAddresses []string `json:"BccAddresses"`
	} `json:"Destination"`
	ReplyToAddresses []string `json:"ReplyToAddresses"`
	Content          struct {
		Simple *struct {
			Subject sesContentData `json:"Subject"`
			Body    struct {
				Text *sesContentData `json:"Text"`
				Html *sesContentData `json:"Html"`
			} `json:"Body"`
			Headers []struct {
				Name  string `json:"Name"`
				Value string `json:"Value"`
			} `json:"Headers"`
			Attachments []struct {
				FileName           string `json:"FileName"`
				ContentType        string `json:"ContentType"`
				RawContent         string `json:"RawContent"`
				ContentDisposition string `json:"ContentDisposition"`
				ContentID          string `json:"ContentId"`
			} `json:"Attachments"`
		} `json:"Simple"`
		Raw *struct {
			Data string `json:"Data"`
		} `json:"Raw"`
		Template *struct {
			TemplateName string `json:"TemplateName"`
			TemplateData string `json:"TemplateData"`
		} `json:"Template"`
	} `json:"Content"`
}

func sesV2Fail(w http.ResponseWriter, status int, typ, msg string) {
	w.Header().Set("x-amzn-ErrorType", typ)
	writeJSON(w, status, map[string]string{"message": msg, "__type": typ})
}

func (s *Server) handleSESv2(w http.ResponseWriter, r *http.Request) {
	cred, project, err := s.sesCredential(r)
	if err != nil {
		sesV2Fail(w, 403, "UnrecognizedClientException", "The security token included in the request is invalid.")
		return
	}
	var req sesV2Request
	if err := readJSON(r, &req, s.cfg.MaxMessageBytes*2); err != nil {
		sesV2Fail(w, 400, "BadRequestException", "invalid JSON: "+err.Error())
		return
	}
	var e *model.Email
	switch {
	case req.Content.Raw != nil:
		raw, derr := base64.StdEncoding.DecodeString(req.Content.Raw.Data)
		if derr != nil {
			sesV2Fail(w, 400, "BadRequestException", "Content.Raw.Data must be base64")
			return
		}
		to := append(append(append([]string{}, req.Destination.ToAddresses...), req.Destination.CcAddresses...), req.Destination.BccAddresses...)
		e, err = s.captureRaw(r, cred, project, model.SourceSES, raw, req.FromEmailAddress, to)
	default:
		m := &mailpkg.Message{Headers: map[string]string{}}
		if req.FromEmailAddress != "" {
			if m.From, err = mailpkg.ParseAddress(req.FromEmailAddress); err != nil {
				sesV2Fail(w, 400, "BadRequestException", "invalid FromEmailAddress")
				return
			}
		}
		if m.To, err = parseList(req.Destination.ToAddresses); err != nil {
			sesV2Fail(w, 400, "BadRequestException", err.Error())
			return
		}
		m.Cc, _ = parseList(req.Destination.CcAddresses)
		m.Bcc, _ = parseList(req.Destination.BccAddresses)
		m.ReplyTo, _ = parseList(req.ReplyToAddresses)
		if len(m.To)+len(m.Cc)+len(m.Bcc) == 0 {
			sesV2Fail(w, 400, "BadRequestException", "Destination must contain at least one address")
			return
		}
		if sm := req.Content.Simple; sm != nil {
			m.Subject = sm.Subject.Data
			if sm.Body.Text != nil {
				m.Text = sm.Body.Text.Data
			}
			if sm.Body.Html != nil {
				m.HTML = sm.Body.Html.Data
			}
			for _, h := range sm.Headers {
				m.Headers[h.Name] = h.Value
			}
			for _, a := range sm.Attachments {
				data, derr := base64.StdEncoding.DecodeString(a.RawContent)
				if derr != nil {
					sesV2Fail(w, 400, "BadRequestException", "attachment RawContent must be base64")
					return
				}
				m.Attachments = append(m.Attachments, mailpkg.FileAttachment{Filename: a.FileName, ContentType: a.ContentType, ContentID: a.ContentID, Inline: strings.EqualFold(a.ContentDisposition, "INLINE"), Data: data})
			}
		} else if t := req.Content.Template; t != nil {
			m.Subject = "(SES template " + t.TemplateName + ")"
			m.Text = "SES template " + t.TemplateName + " is rendered by AWS and not available in MailDev.\n\nTemplateData:\n" + t.TemplateData
		}
		e, err = s.capture(r, cred, project, model.SourceSES, m)
	}
	if err != nil {
		sesV2Fail(w, 500, "InternalServiceErrorException", err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"MessageId": e.ID.String()})
}
