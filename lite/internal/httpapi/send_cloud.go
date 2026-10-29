package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/mail"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	mailpkg "github.com/phongbui83/maildev/internal/mail"
	"github.com/phongbui83/maildev/internal/model"
	"github.com/phongbui83/maildev/internal/store"
)

// Cloud e-mail APIs used by Java/.NET/Python/PHP/Node/Go SDKs. The project is
// selected by the API key id embedded in the endpoint path (SDKs let you override
// the base URL but not add headers), the secret is verified from the request:
//
//   Azure Communication Services Email  https://<host>/azure/<keyid>  (HMAC-SHA256 signature with accesskey = base64(secret))
//   Microsoft Graph sendMail             https://<host>/graph/<keyid>  (Bearer <keyid>.<secret> or Bearer <secret>)
//   Gmail API                            https://<host>/gmail/<keyid>  (Bearer <keyid>.<secret> or Bearer <secret>)

// credentialFromPath resolves the key id in the URL and verifies the secret
// supplied as Bearer token / X-API-Key (full key or bare secret).
func (s *Server) credentialFromPath(r *http.Request) (*model.Credential, *model.Project, error) {
	keyID := chi.URLParam(r, "keyID")
	secret := r.Header.Get("X-API-Key")
	if secret == "" {
		if h := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(h), "bearer ") {
			secret = strings.TrimSpace(h[7:])
		}
	}
	if i := strings.LastIndex(secret, "."); i > 0 && secret[:i] == keyID {
		secret = secret[i+1:]
	}
	if keyID == "" || secret == "" {
		return nil, nil, store.ErrNotFound
	}
	return s.store.Authenticate(r.Context(), model.CredAPI, keyID, secret)
}

// --- Azure Communication Services Email ---

type acsAddress struct {
	Address     string `json:"address"`
	DisplayName string `json:"displayName"`
}

func (a acsAddress) addr() mail.Address { return mail.Address{Name: a.DisplayName, Address: a.Address} }

type acsRequest struct {
	SenderAddress string `json:"senderAddress"`
	Content       struct {
		Subject   string `json:"subject"`
		PlainText string `json:"plainText"`
		HTML      string `json:"html"`
	} `json:"content"`
	Recipients struct {
		To  []acsAddress `json:"to"`
		Cc  []acsAddress `json:"cc"`
		Bcc []acsAddress `json:"bcc"`
	} `json:"recipients"`
	ReplyTo     []acsAddress      `json:"replyTo"`
	Headers     map[string]string `json:"headers"`
	Attachments []struct {
		Name            string `json:"name"`
		ContentType     string `json:"contentType"`
		ContentInBase64 string `json:"contentInBase64"`
		ContentID       string `json:"contentId"`
	} `json:"attachments"`
}

func acsFail(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

// acsAuthenticate accepts the ACS HMAC-SHA256 scheme (what the SDKs send) or a
// plain X-API-Key / Bearer secret.
func (s *Server) acsAuthenticate(r *http.Request, body []byte) (*model.Credential, *model.Project, error) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "HMAC-SHA256 ") {
		return s.credentialFromPath(r)
	}
	keyID := chi.URLParam(r, "keyID")
	cred, _, err := s.store.LookupAPIKeyID(r.Context(), keyID)
	if err != nil {
		return nil, nil, store.ErrNotFound
	}
	secret := cred.Secret
	// Signature = base64(HMAC(key, "METHOD\npath?query\ndate;host;contentsha")), key = base64 decode of accesskey.
	key := []byte(base64.StdEncoding.EncodeToString([]byte(secret))) // accesskey shown to the user is base64(secret)
	keyBytes, _ := base64.StdEncoding.DecodeString(string(key))
	var sig string
	for _, part := range strings.Split(strings.TrimPrefix(auth, "HMAC-SHA256 "), "&") {
		if strings.HasPrefix(part, "Signature=") {
			sig = strings.TrimPrefix(part, "Signature=")
		}
	}
	sum := sha256.Sum256(body)
	contentHash := base64.StdEncoding.EncodeToString(sum[:])
	if got := r.Header.Get("x-ms-content-sha256"); got != "" && got != contentHash {
		return nil, nil, store.ErrNotFound
	}
	pathAndQuery := r.URL.RequestURI()
	stringToSign := r.Method + "\n" + pathAndQuery + "\n" + r.Header.Get("x-ms-date") + ";" + r.Host + ";" + contentHash
	m := hmac.New(sha256.New, keyBytes)
	m.Write([]byte(stringToSign))
	want := base64.StdEncoding.EncodeToString(m.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(want), []byte(sig)) != 1 {
		return nil, nil, store.ErrNotFound
	}
	return s.store.Authenticate(r.Context(), model.CredAPI, keyID, secret)
}

func (s *Server) handleAzureSend(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.cfg.MaxMessageBytes*2))
	if err != nil {
		acsFail(w, 413, "RequestEntityTooLarge", "message too large")
		return
	}
	cred, project, err := s.acsAuthenticate(r, body)
	if err != nil {
		s.store.Audit(r.Context(), store.AuditParams{Actor: chi.URLParam(r, "keyID"), Action: "api_auth_failed", IP: clientIP(r), Protocol: model.SourceAzure, Detail: map[string]any{"path": r.URL.Path}})
		acsFail(w, 401, "Unauthorized", "invalid key id or signature")
		return
	}
	var req acsRequest
	if err := jsonUnmarshal(body, &req); err != nil {
		acsFail(w, 400, "BadRequest", "invalid JSON: "+err.Error())
		return
	}
	m := &mailpkg.Message{Subject: req.Content.Subject, Text: req.Content.PlainText, HTML: req.Content.HTML, Headers: req.Headers}
	if req.SenderAddress != "" {
		if m.From, err = mailpkg.ParseAddress(req.SenderAddress); err != nil {
			acsFail(w, 400, "BadRequest", "invalid senderAddress")
			return
		}
	}
	for _, a := range req.Recipients.To {
		m.To = append(m.To, a.addr())
	}
	for _, a := range req.Recipients.Cc {
		m.Cc = append(m.Cc, a.addr())
	}
	for _, a := range req.Recipients.Bcc {
		m.Bcc = append(m.Bcc, a.addr())
	}
	for _, a := range req.ReplyTo {
		m.ReplyTo = append(m.ReplyTo, a.addr())
	}
	if len(m.To)+len(m.Cc)+len(m.Bcc) == 0 {
		acsFail(w, 400, "BadRequest", "recipients.to is required")
		return
	}
	for _, a := range req.Attachments {
		data, derr := base64.StdEncoding.DecodeString(a.ContentInBase64)
		if derr != nil {
			acsFail(w, 400, "BadRequest", "attachment contentInBase64 is not base64")
			return
		}
		m.Attachments = append(m.Attachments, mailpkg.FileAttachment{Filename: a.Name, ContentType: a.ContentType, ContentID: a.ContentID, Inline: a.ContentID != "", Data: data})
	}
	e, err := s.capture(r, cred, project, model.SourceAzure, m)
	if err != nil {
		s.ingestError(w, err)
		return
	}
	// The SDK polls the operation until Succeeded.
	w.Header().Set("Operation-Location", s.cfg.PublicURL+"/azure/"+cred.Username+"/emails/operations/"+e.ID.String()+"?api-version=2023-03-31")
	w.Header().Set("Operation-Id", e.ID.String())
	writeJSON(w, 202, map[string]any{"id": e.ID.String(), "status": "Succeeded"})
}

func (s *Server) handleAzureStatus(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "opID")
	if _, err := uuid.Parse(id); err != nil {
		acsFail(w, 404, "NotFound", "unknown operation")
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "status": "Succeeded"})
}

// --- Microsoft Graph sendMail ---

type graphRecipient struct {
	EmailAddress struct {
		Address string `json:"address"`
		Name    string `json:"name"`
	} `json:"emailAddress"`
}

func (g graphRecipient) addr() mail.Address {
	return mail.Address{Name: g.EmailAddress.Name, Address: g.EmailAddress.Address}
}

type graphRequest struct {
	Message struct {
		Subject string `json:"subject"`
		Body    struct {
			ContentType string `json:"contentType"`
			Content     string `json:"content"`
		} `json:"body"`
		From          *graphRecipient  `json:"from"`
		ToRecipients  []graphRecipient `json:"toRecipients"`
		CcRecipients  []graphRecipient `json:"ccRecipients"`
		BccRecipients []graphRecipient `json:"bccRecipients"`
		ReplyTo       []graphRecipient `json:"replyTo"`
		Attachments   []struct {
			Name         string `json:"name"`
			ContentType  string `json:"contentType"`
			ContentBytes string `json:"contentBytes"`
			ContentID    string `json:"contentId"`
			IsInline     bool   `json:"isInline"`
		} `json:"attachments"`
		InternetMessageHeaders []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"internetMessageHeaders"`
	} `json:"message"`
}

func graphFail(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

func (s *Server) handleGraphSend(w http.ResponseWriter, r *http.Request) {
	cred, project, err := s.credentialFromPath(r)
	if err != nil {
		s.store.Audit(r.Context(), store.AuditParams{Actor: chi.URLParam(r, "keyID"), Action: "api_auth_failed", IP: clientIP(r), Protocol: model.SourceGraph, Detail: map[string]any{"path": r.URL.Path}})
		graphFail(w, 401, "InvalidAuthenticationToken", "use the project API key as bearer token")
		return
	}
	var req graphRequest
	if err := readJSON(r, &req, s.cfg.MaxMessageBytes*2); err != nil {
		graphFail(w, 400, "BadRequest", "invalid JSON: "+err.Error())
		return
	}
	msg := req.Message
	m := &mailpkg.Message{Subject: msg.Subject, Headers: map[string]string{}}
	if strings.EqualFold(msg.Body.ContentType, "html") {
		m.HTML = msg.Body.Content
	} else {
		m.Text = msg.Body.Content
	}
	if msg.From != nil {
		m.From = msg.From.addr()
	} else {
		// Graph sends as the signed-in user; use the path user id when it looks like an address.
		if u := chi.URLParam(r, "user"); strings.Contains(u, "@") {
			m.From = mail.Address{Address: u}
		} else {
			m.From = mail.Address{Address: "graph@" + s.cfg.Hostname}
		}
	}
	for _, x := range msg.ToRecipients {
		m.To = append(m.To, x.addr())
	}
	for _, x := range msg.CcRecipients {
		m.Cc = append(m.Cc, x.addr())
	}
	for _, x := range msg.BccRecipients {
		m.Bcc = append(m.Bcc, x.addr())
	}
	for _, x := range msg.ReplyTo {
		m.ReplyTo = append(m.ReplyTo, x.addr())
	}
	for _, h := range msg.InternetMessageHeaders {
		m.Headers[h.Name] = h.Value
	}
	if len(m.To)+len(m.Cc)+len(m.Bcc) == 0 {
		graphFail(w, 400, "BadRequest", "toRecipients is required")
		return
	}
	for _, a := range msg.Attachments {
		data, derr := base64.StdEncoding.DecodeString(a.ContentBytes)
		if derr != nil {
			graphFail(w, 400, "BadRequest", "attachment contentBytes is not base64")
			return
		}
		m.Attachments = append(m.Attachments, mailpkg.FileAttachment{Filename: a.Name, ContentType: a.ContentType, ContentID: a.ContentID, Inline: a.IsInline, Data: data})
	}
	if _, err := s.capture(r, cred, project, model.SourceGraph, m); err != nil {
		s.ingestError(w, err)
		return
	}
	w.WriteHeader(202)
}

// --- Gmail API ---

func gmailFail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": status, "message": msg, "status": "INVALID_ARGUMENT"}})
}

func (s *Server) handleGmailSend(w http.ResponseWriter, r *http.Request) {
	cred, project, err := s.credentialFromPath(r)
	if err != nil {
		s.store.Audit(r.Context(), store.AuditParams{Actor: chi.URLParam(r, "keyID"), Action: "api_auth_failed", IP: clientIP(r), Protocol: model.SourceGmail, Detail: map[string]any{"path": r.URL.Path}})
		writeJSON(w, 401, map[string]any{"error": map[string]any{"code": 401, "message": "use the project API key as bearer token", "status": "UNAUTHENTICATED"}})
		return
	}
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	var raw []byte
	if strings.HasPrefix(ct, "message/rfc822") {
		raw, err = io.ReadAll(http.MaxBytesReader(w, r.Body, s.cfg.MaxMessageBytes+1024))
		if err != nil {
			gmailFail(w, 413, "message too large")
			return
		}
	} else {
		var req struct {
			Raw string `json:"raw"`
		}
		if err := readJSON(r, &req, s.cfg.MaxMessageBytes*2); err != nil || req.Raw == "" {
			gmailFail(w, 400, "body must contain raw (base64url RFC 822 message)")
			return
		}
		raw, err = base64.RawURLEncoding.DecodeString(strings.TrimRight(req.Raw, "="))
		if err != nil {
			if raw, err = base64.StdEncoding.DecodeString(req.Raw); err != nil {
				gmailFail(w, 400, "raw is not valid base64url")
				return
			}
		}
	}
	e, err := s.captureRaw(r, cred, project, model.SourceGmail, raw, "", nil)
	if err != nil {
		s.ingestError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": e.ID.String(), "threadId": e.ID.String(), "labelIds": []string{"SENT"}})
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
