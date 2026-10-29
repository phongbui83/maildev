package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/phongbui83/maildev/internal/auth"
	"github.com/phongbui83/maildev/internal/store"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := readJSON(r, &req, 1<<16); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	req.Username = strings.ToLower(strings.TrimSpace(req.Username))
	u, token, err := s.auth.Login(r.Context(), req.Username, req.Password, clientIP(r), r.UserAgent())
	if err != nil {
		s.store.Audit(context.Background(), store.AuditParams{Actor: req.Username, Action: "login_failed", IP: clientIP(r), Detail: map[string]any{"reason": err.Error()}})
		switch {
		case errors.Is(err, auth.ErrInvalidCredentials):
			writeError(w, http.StatusUnauthorized, "invalid username or password")
		case errors.Is(err, auth.ErrAccountLocked):
			writeError(w, http.StatusLocked, "account temporarily locked, try again in 15 minutes")
		case errors.Is(err, auth.ErrDirectoryUnavailable):
			writeError(w, http.StatusBadGateway, "directory service unavailable")
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	s.auth.SetCookie(w, token)
	r = r.WithContext(auth.WithUser(r.Context(), u))
	s.audit(r, nil, "login", u.Username, nil)
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(auth.CookieName); err == nil {
		_ = s.auth.Logout(r.Context(), c.Value)
	}
	if u := auth.UserFrom(r.Context()); u != nil {
		s.audit(r, nil, "logout", u.Username, nil)
	}
	s.auth.ClearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeError(w, http.StatusUnauthorized, "not logged in")
		return
	}
	writeJSON(w, http.StatusOK, u)
}
