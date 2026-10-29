package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/phongbui83/maildev/internal/mail"
	"github.com/phongbui83/maildev/internal/store"
)

func (s *Server) handleListEmails(w http.ResponseWriter, r *http.Request) {
	p := projectFrom(r)
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	list, total, err := s.store.ListEmails(r.Context(), store.ListEmailsParams{
		ProjectID: p.ID, Query: q.Get("q"), Unread: q.Get("unread") == "1", Limit: limit, Offset: offset,
	})
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"items": list, "total": total})
}

func emailIDFrom(r *http.Request) (uuid.UUID, error) {
	return uuid.Parse(chi.URLParam(r, "emailID"))
}

func (s *Server) handleGetEmail(w http.ResponseWriter, r *http.Request) {
	p := projectFrom(r)
	id, err := emailIDFrom(r)
	if err != nil {
		writeError(w, 400, "invalid email id")
		return
	}
	e, err := s.store.GetEmail(r.Context(), p.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, 404, "email not found")
		return
	}
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if !e.Seen && accessFrom(r).canWrite() && r.URL.Query().Get("peek") != "1" {
		_ = s.store.SetEmailSeen(r.Context(), p.ID, id, true)
		e.Seen = true
		s.ingest.Notify("updated", p.ID, e.ID, e.UID)
	}
	s.audit(r, p, "email_view", e.ID.String(), map[string]any{"subject": e.Subject})
	writeJSON(w, 200, e)
}

func (s *Server) handleRawEmail(w http.ResponseWriter, r *http.Request) {
	p := projectFrom(r)
	id, err := emailIDFrom(r)
	if err != nil {
		writeError(w, 400, "invalid email id")
		return
	}
	raw, err := s.store.GetRawEmail(r.Context(), p.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, 404, "email not found")
		return
	}
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	s.audit(r, p, "email_download", id.String(), nil)
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", id.String()+".eml"))
		w.Header().Set("Content-Type", "message/rfc822")
	} else {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(raw)
}

// handleHTMLEmail serves the sanitized HTML body for the preview iframe. Remote
// resources are blocked by CSP unless ?remote=1 is passed explicitly.
func (s *Server) handleHTMLEmail(w http.ResponseWriter, r *http.Request) {
	p := projectFrom(r)
	id, err := emailIDFrom(r)
	if err != nil {
		writeError(w, 400, "invalid email id")
		return
	}
	e, err := s.store.GetEmail(r.Context(), p.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, 404, "email not found")
		return
	}
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	cidPrefix := fmt.Sprintf("/api/projects/%s/emails/%s/cid/", p.ID, e.ID)
	body := e.HTMLBody
	if body == "" {
		body = "<pre style=\"white-space:pre-wrap;font-family:ui-monospace,monospace\">" + htmlEscape(e.TextBody) + "</pre>"
	}
	out := mail.SanitizeHTML(body, cidPrefix)
	csp := "default-src 'none'; img-src 'self' data: cid:; style-src 'unsafe-inline'; font-src 'none'; form-action 'none'; base-uri 'none'"
	if r.URL.Query().Get("remote") == "1" {
		csp = "default-src 'none'; img-src 'self' data: cid: https: http:; style-src 'unsafe-inline' https: http:; font-src https: http: data:; form-action 'none'; base-uri 'none'"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", csp)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Write([]byte("<!doctype html><html><head><meta charset=\"utf-8\"><base target=\"_blank\"></head><body>" + out + "</body></html>"))
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;").Replace(s)
}

func (s *Server) serveAttachment(w http.ResponseWriter, r *http.Request, filename, ctype string, data []byte, download bool) {
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	// Never let the browser execute attachment content in our origin.
	lower := strings.ToLower(ctype)
	if strings.Contains(lower, "html") || strings.Contains(lower, "javascript") || strings.Contains(lower, "svg") || strings.Contains(lower, "xml") {
		download = true
		if strings.Contains(lower, "svg") {
			ctype = "application/octet-stream"
		}
	}
	disp := "inline"
	if download {
		disp = "attachment"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": filename}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Write(data)
}

func (s *Server) handleAttachment(w http.ResponseWriter, r *http.Request) {
	p := projectFrom(r)
	id, err := emailIDFrom(r)
	if err != nil {
		writeError(w, 400, "invalid email id")
		return
	}
	aid, err := uuid.Parse(chi.URLParam(r, "attachmentID"))
	if err != nil {
		writeError(w, 400, "invalid attachment id")
		return
	}
	a, err := s.store.GetAttachment(r.Context(), p.ID, id, aid)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, 404, "attachment not found")
		return
	}
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	s.audit(r, p, "attachment_download", id.String(), map[string]any{"filename": a.Filename})
	s.serveAttachment(w, r, a.Filename, a.ContentType, a.Data, r.URL.Query().Get("download") == "1")
}

func (s *Server) handleCIDAttachment(w http.ResponseWriter, r *http.Request) {
	p := projectFrom(r)
	id, err := emailIDFrom(r)
	if err != nil {
		writeError(w, 400, "invalid email id")
		return
	}
	cid := chi.URLParam(r, "cid")
	a, err := s.store.GetAttachmentByContentID(r.Context(), p.ID, id, cid)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, 404, "attachment not found")
		return
	}
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if !strings.HasPrefix(strings.ToLower(a.ContentType), "image/") {
		writeError(w, 404, "not an inline image")
		return
	}
	s.serveAttachment(w, r, a.Filename, a.ContentType, a.Data, false)
}

type updateEmailRequest struct {
	Seen *bool `json:"seen"`
}

func (s *Server) handleUpdateEmail(w http.ResponseWriter, r *http.Request) {
	p := projectFrom(r)
	id, err := emailIDFrom(r)
	if err != nil {
		writeError(w, 400, "invalid email id")
		return
	}
	var req updateEmailRequest
	if err := readJSON(r, &req, 1<<12); err != nil {
		writeError(w, 400, "invalid JSON body")
		return
	}
	if req.Seen != nil {
		if err := s.store.SetEmailSeen(r.Context(), p.ID, id, *req.Seen); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, 404, "email not found")
				return
			}
			writeError(w, 500, err.Error())
			return
		}
		s.ingest.Notify("updated", p.ID, id, 0)
	}
	w.WriteHeader(204)
}

func (s *Server) handleDeleteEmail(w http.ResponseWriter, r *http.Request) {
	p := projectFrom(r)
	id, err := emailIDFrom(r)
	if err != nil {
		writeError(w, 400, "invalid email id")
		return
	}
	if err := s.store.DeleteEmail(r.Context(), p.ID, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, 404, "email not found")
			return
		}
		writeError(w, 500, err.Error())
		return
	}
	s.audit(r, p, "email_delete", id.String(), nil)
	s.ingest.Notify("deleted", p.ID, id, 0)
	w.WriteHeader(204)
}

func (s *Server) handleDeleteAllEmails(w http.ResponseWriter, r *http.Request) {
	p := projectFrom(r)
	n, err := s.store.DeleteAllEmails(r.Context(), p.ID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	s.audit(r, p, "inbox_clear", p.Slug, map[string]any{"deleted": n})
	s.ingest.Notify("cleared", p.ID, uuid.Nil, 0)
	writeJSON(w, 200, map[string]any{"deleted": n})
}

func (s *Server) handleMarkAllRead(w http.ResponseWriter, r *http.Request) {
	p := projectFrom(r)
	if err := s.store.MarkAllSeen(r.Context(), p.ID); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	s.ingest.Notify("updated", p.ID, uuid.Nil, 0)
	w.WriteHeader(204)
}

// handleEvents streams inbox changes as Server-Sent Events.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	p := projectFrom(r)
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, 500, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	fmt.Fprintf(w, "event: hello\ndata: {\"project_id\":%q}\n\n", p.ID)
	flusher.Flush()

	ch, cancel := s.ingest.Bus().Subscribe(p.ID)
	defer cancel()
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case ev, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(ev)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, data)
			flusher.Flush()
		}
	}
}
