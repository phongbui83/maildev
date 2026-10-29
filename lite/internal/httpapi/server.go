// Package httpapi serves the REST API used by the web UI plus the provider
// compatible ingestion endpoints (generic JSON, SendGrid, AWS SES).
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/phongbui83/maildev/internal/auth"
	"github.com/phongbui83/maildev/internal/config"
	"github.com/phongbui83/maildev/internal/directory"
	"github.com/phongbui83/maildev/internal/encryption"
	"github.com/phongbui83/maildev/internal/ingest"
	"github.com/phongbui83/maildev/internal/model"
	"github.com/phongbui83/maildev/internal/store"
)

type Server struct {
	cfg    *config.Config
	store  *store.Store
	auth   *auth.Service
	ingest *ingest.Ingestor
	enc    *encryption.Service  // nil when encryption is disabled
	dir    *directory.Directory // nil when no directory URL is configured
	mux    *chi.Mux
}

func New(cfg *config.Config, st *store.Store, a *auth.Service, in *ingest.Ingestor, enc *encryption.Service, dir *directory.Directory) *Server {
	s := &Server{cfg: cfg, store: st, auth: a, ingest: in, enc: enc, dir: dir}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(requestLogger)
	r.Use(s.auth.Middleware)

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	r.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := s.store.Ping(ctx); err != nil {
			http.Error(w, "db: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ready"))
	})

	// Provider-compatible ingestion (credential auth, no session).
	r.Post("/api/v1/send", s.handleGenericSend)
	r.Post("/sendgrid/v3/mail/send", s.handleSendGrid)
	r.Post("/v3/mail/send", s.handleSendGrid)
	r.Post("/ses", s.handleSESv1)
	r.Post("/ses/", s.handleSESv1)
	r.Post("/ses/v2/email/outbound-emails", s.handleSESv2)
	r.Post("/v2/email/outbound-emails", s.handleSESv2)
	// Azure Communication Services Email, Microsoft Graph, Gmail (key id in the path).
	r.Post("/azure/{keyID}/emails:send", s.handleAzureSend)
	r.Get("/azure/{keyID}/emails/operations/{opID}", s.handleAzureStatus)
	r.Post("/graph/{keyID}/v1.0/users/{user}/sendMail", s.handleGraphSend)
	r.Post("/graph/{keyID}/v1.0/me/sendMail", s.handleGraphSend)
	r.Post("/graph/{keyID}/beta/users/{user}/sendMail", s.handleGraphSend)
	r.Post("/graph/{keyID}/beta/me/sendMail", s.handleGraphSend)
	r.Post("/gmail/{keyID}/gmail/v1/users/{user}/messages/send", s.handleGmailSend)
	r.Post("/gmail/{keyID}/upload/gmail/v1/users/{user}/messages/send", s.handleGmailSend)

	r.Route("/api", func(r chi.Router) {
		r.Get("/config", s.handleConfig)
		r.Post("/auth/login", s.handleLogin)
		r.Post("/auth/logout", s.handleLogout)
		r.Get("/auth/me", s.handleMe)
		r.With(s.requireUser).Post("/auth/password", s.handleChangePassword)

		r.Group(func(r chi.Router) {
			r.Use(s.requireUser)

			r.Get("/directory/users", s.handleDirectoryUsers)
			r.Get("/projects", s.handleListProjects)
			r.With(s.requireAdmin).Post("/projects", s.handleCreateProject)

			r.Route("/projects/{projectID}", func(r chi.Router) {
				r.Use(s.loadProject)
				r.Get("/", s.handleGetProject)
				r.With(s.requireManage).Patch("/", s.handleUpdateProject)
				r.With(s.requireAdmin).Delete("/", s.handleDeleteProject)
				r.With(s.requireManage, s.requireActive).Post("/init", s.handleInitCredentials)
				r.Get("/credentials", s.handleListCredentials)
				r.Get("/members", s.handleListMembers)
				r.With(s.requireManage).Post("/members", s.handleAddMember)
				r.With(s.requireManage).Delete("/members/{username}", s.handleRemoveMember)
				r.Get("/stats", s.handleProjectStats)
				r.Get("/events", s.handleEvents)
				r.Get("/audit", s.handleProjectAudit)

				r.Get("/relay", s.handleGetRelay)
				r.With(s.requireAdmin).Put("/relay", s.handleUpdateRelay)
				r.With(s.requireAdmin).Post("/relay/test", s.handleTestRelay)
				r.With(s.requireManageOrAuditor).Get("/relay/queue", s.handleRelayQueue)
				r.With(s.requireManage, s.requireActive).Post("/relay/approve", s.handleRelayApprove)
				r.With(s.requireManage, s.requireActive).Post("/relay/reject", s.handleRelayReject)

				r.Get("/emails", s.handleListEmails)
				r.With(s.requireWrite).Delete("/emails", s.handleDeleteAllEmails)
				r.With(s.requireWrite).Post("/emails/mark-all-read", s.handleMarkAllRead)
				r.Route("/emails/{emailID}", func(r chi.Router) {
					r.Get("/", s.handleGetEmail)
					r.Get("/raw", s.handleRawEmail)
					r.Get("/html", s.handleHTMLEmail)
					r.Get("/attachments/{attachmentID}", s.handleAttachment)
					r.Get("/cid/{cid}", s.handleCIDAttachment)
					r.With(s.requireWrite).Patch("/", s.handleUpdateEmail)
					r.With(s.requireWrite).Delete("/", s.handleDeleteEmail)
					r.With(s.requireWrite).Post("/relay/request", s.handleRelayRequest)
					r.With(s.requireManage, s.requireActive).Post("/relay/approve", s.handleRelayApprove)
					r.With(s.requireManage, s.requireActive).Post("/relay/reject", s.handleRelayReject)
				})
			})

			r.Route("/admin", func(r chi.Router) {
				r.Use(s.requireAdminOrAuditor)
				r.Get("/users", s.handleListUsers)
				r.With(s.requireAdmin).Patch("/users/{userID}", s.handleUpdateUser)
				r.With(s.requireAdmin).Post("/local-users", s.handleCreateLocalUser)
				r.With(s.requireAdmin).Post("/users/{userID}/reset-password", s.handleResetPassword)
				r.With(s.requireAdmin).Get("/lookup-user", s.handleLookupUser)
				r.Get("/audit", s.handleAudit) // admins (Logs tab) and auditors
				r.With(s.requireAdmin).Delete("/users/{userID}", s.handleDeleteUser)
			})
		})
	})
	s.mux = r
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type apiError struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, apiError{Error: msg})
}

func readJSON(r *http.Request, v any, maxBytes int64) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxBytes))
	return dec.Decode(v)
}

func clientIP(r *http.Request) string {
	ip := r.RemoteAddr
	if h, _, err := net.SplitHostPort(ip); err == nil {
		ip = h
	}
	return ip
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		if strings.HasPrefix(r.URL.Path, "/healthz") || strings.HasPrefix(r.URL.Path, "/readyz") {
			return
		}
		slog.Info("http", "method", r.Method, "path", r.URL.Path, "status", ww.Status(), "bytes", ww.BytesWritten(), "ms", time.Since(start).Milliseconds(), "ip", clientIP(r))
	})
}

// handleDirectoryUsers backs the member picker: directory accounts matching q
// plus local (external) accounts, which are not in the company directory.
func (s *Server) handleDirectoryUsers(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	type entry struct {
		Username   string `json:"username"`
		FullName   string `json:"fullname"`
		Email      string `json:"email"`
		Department string `json:"department"`
		Source     string `json:"source"`
	}
	out := []entry{}
	if s.dir.Enabled() {
		for _, u := range s.dir.Search(q, 50) {
			out = append(out, entry{u.Username, u.FullName, u.Email, u.Department, "ldap"})
		}
	}
	if users, err := s.store.ListUsers(r.Context()); err == nil {
		for _, u := range users {
			if !u.IsLocal() || u.Disabled {
				continue
			}
			if q == "" || strings.Contains(u.Username, q) || strings.Contains(strings.ToLower(u.DisplayName), q) || strings.Contains(strings.ToLower(u.Email), q) {
				out = append(out, entry{u.Username, u.DisplayName, u.Email, "", "local"})
			}
		}
	}
	writeJSON(w, 200, map[string]any{"items": out, "directory": s.dir.Ready()})
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"public_url":        s.cfg.PublicURL,
		"mail_host":         s.cfg.MailHost,
		"smtp_ports":        publicPorts(s.cfg.PublicSMTPPorts, s.cfg.SMTPAddrs),
		"smtps_ports":       publicPorts(s.cfg.PublicSMTPSPorts, s.cfg.SMTPSAddrs),
		"imap_ports":        publicPorts(s.cfg.PublicIMAPPorts, s.cfg.IMAPAddrs),
		"imaps_ports":       publicPorts(s.cfg.PublicIMAPSPorts, s.cfg.IMAPSAddrs),
		"pop3_ports":        publicPorts(s.cfg.PublicPOP3Ports, s.cfg.POP3Addrs),
		"pop3s_ports":       publicPorts(s.cfg.PublicPOP3SPorts, s.cfg.POP3SAddrs),
		"auth_mode":         s.cfg.AuthMode,
		"edition":           s.cfg.Edition,
		"encryption":        s.enc != nil,
		"directory":         s.dir.Ready(),
		"max_message_bytes": s.cfg.MaxMessageBytes,
	})
}

// publicPorts prefers the configured public ports (load balancer) over listener ports.
func publicPorts(public, addrs []string) []string {
	if len(public) > 0 {
		return public
	}
	return ports(addrs)
}

func ports(addrs []string) []string {
	out := []string{}
	for _, a := range addrs {
		if _, p, err := net.SplitHostPort(a); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// --- auth / access middleware ---

type ctxKey int

const (
	projectKey ctxKey = iota
	accessKey
)

type access struct {
	role string // admin, auditor, owner, member, ""
}

func (a access) canRead() bool { return a.role != "" }
func (a access) canWrite() bool {
	return a.role == model.RoleAdmin || a.role == model.MemberOwner || a.role == model.MemberMember
}
func (a access) canManage() bool { return a.role == model.RoleAdmin || a.role == model.MemberOwner }

func (s *Server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth.UserFrom(r.Context()) == nil {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := auth.UserFrom(r.Context())
		if u == nil || !u.IsAdmin() {
			writeError(w, http.StatusForbidden, "admin role required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireAuditor: the global audit log (all projects) is for auditors only; admins
// and owners read the audit trail of the projects they manage.
func (s *Server) requireAuditor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := auth.UserFrom(r.Context())
		if u == nil || !u.IsAuditor() {
			writeError(w, http.StatusForbidden, "auditor role required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireAdminOrAuditor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := auth.UserFrom(r.Context())
		if u == nil || !(u.IsAdmin() || u.IsAuditor()) {
			writeError(w, http.StatusForbidden, "admin or auditor role required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// loadProject resolves {projectID} (uuid or slug), computes the caller's access
// level and rejects callers without read access.
func (s *Server) loadProject(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := auth.UserFrom(r.Context())
		ref := chi.URLParam(r, "projectID")
		var p *model.Project
		var err error
		if id, perr := uuid.Parse(ref); perr == nil {
			p, err = s.store.GetProject(r.Context(), id)
		} else {
			p, err = s.store.GetProjectBySlug(r.Context(), ref)
		}
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		var acc access
		switch {
		case u.IsAdmin():
			acc.role = model.RoleAdmin
		case u.IsAuditor():
			acc.role = model.RoleAuditor
		default:
			role, err := s.store.MemberRole(r.Context(), p.ID, u.ID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			acc.role = role
		}
		if !acc.canRead() {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		ctx := context.WithValue(r.Context(), projectKey, p)
		ctx = context.WithValue(ctx, accessKey, acc)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func projectFrom(r *http.Request) *model.Project {
	p, _ := r.Context().Value(projectKey).(*model.Project)
	return p
}

func accessFrom(r *http.Request) access {
	a, _ := r.Context().Value(accessKey).(access)
	return a
}

func (s *Server) requireWrite(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !accessFrom(r).canWrite() {
			writeError(w, http.StatusForbidden, "read-only access")
			return
		}
		if projectFrom(r).Status != "active" {
			writeError(w, http.StatusForbidden, "project is archived: inbox is read-only")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireActive blocks management actions that make no sense on an archived project.
func (s *Server) requireActive(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if projectFrom(r).Status != "active" {
			writeError(w, http.StatusForbidden, "project is archived")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireManage(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !accessFrom(r).canManage() {
			writeError(w, http.StatusForbidden, "owner or admin role required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireManageOrAuditor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !accessFrom(r).canManage() && !auth.UserFrom(r.Context()).IsAuditor() {
			writeError(w, http.StatusForbidden, "owner, admin or auditor role required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func chiURLParam(r *http.Request, key string) string { return chi.URLParam(r, key) }

func (s *Server) audit(r *http.Request, project *model.Project, action, target string, detail map[string]any) {
	u := auth.UserFrom(r.Context())
	p := store.AuditParams{Action: action, Target: target, Detail: detail, IP: clientIP(r)}
	if u != nil {
		id := u.ID
		p.ActorUserID = &id
		p.Actor = u.Username
	}
	if project != nil {
		id := project.ID
		p.ProjectID = &id
	}
	s.store.Audit(context.Background(), p)
}
