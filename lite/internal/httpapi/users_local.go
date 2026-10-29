package httpapi

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/phongbui83/maildev/internal/auth"
	"github.com/phongbui83/maildev/internal/secrets"
	"github.com/phongbui83/maildev/internal/store"
)

// Local accounts are for people without an Active Directory login (customers).
// They only ever hold the "user" role, never auto-join shared projects, and can
// be made owner or member of specific projects like anyone else.

var localUsernameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._@-]{2,63}$`)

type createLocalUserRequest struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Password    string `json:"password"` // empty: generate one and return it once
}

func (s *Server) handleCreateLocalUser(w http.ResponseWriter, r *http.Request) {
	actor := auth.UserFrom(r.Context())
	var req createLocalUserRequest
	if err := readJSON(r, &req, 1<<14); err != nil {
		writeError(w, 400, "invalid JSON body")
		return
	}
	req.Username = strings.ToLower(strings.TrimSpace(req.Username))
	if !localUsernameRe.MatchString(req.Username) {
		writeError(w, 400, "username must be 3-64 chars of a-z, 0-9, '.', '_', '@' or '-'")
		return
	}
	generated := false
	if req.Password == "" {
		req.Password = secrets.Password(16) + "!"
		generated = true
	} else if err := auth.CheckPasswordPolicy(req.Password); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	actorID := actor.ID
	u, err := s.store.CreateLocalUser(r.Context(), req.Username, strings.TrimSpace(req.DisplayName), strings.TrimSpace(req.Email), hash, true, &actorID)
	if errors.Is(err, store.ErrConflict) {
		writeError(w, 409, "username already exists")
		return
	}
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	s.audit(r, nil, "local_user_create", u.Username, map[string]any{"email": u.Email, "generated_password": generated})
	resp := map[string]any{"user": u}
	if generated {
		resp["password"] = req.Password // shown once to the admin
	}
	writeJSON(w, 201, resp)
}

type resetPasswordRequest struct {
	Password string `json:"password"` // empty: generate
}

// handleResetPassword (admin) sets a new temporary password for a local account.
func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		writeError(w, 400, "invalid user id")
		return
	}
	target, err := s.store.GetUser(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, 404, "user not found")
		return
	}
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if !target.IsLocal() {
		writeError(w, 400, "directory accounts change their password in Active Directory")
		return
	}
	var req resetPasswordRequest
	_ = readJSON(r, &req, 1<<12)
	generated := false
	if req.Password == "" {
		req.Password = secrets.Password(16) + "!"
		generated = true
	} else if err := auth.CheckPasswordPolicy(req.Password); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if err := s.store.SetPassword(r.Context(), id, hash, true); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	s.audit(r, nil, "password_reset", target.Username, map[string]any{"generated_password": generated})
	resp := map[string]any{"ok": true}
	if generated {
		resp["password"] = req.Password
	}
	writeJSON(w, 200, resp)
}

type changePasswordRequest struct {
	Current string `json:"current_password"`
	New     string `json:"new_password"`
}

// handleChangePassword lets a local user change their own password.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if !u.IsLocal() {
		writeError(w, 400, "directory accounts change their password in Active Directory")
		return
	}
	var req changePasswordRequest
	if err := readJSON(r, &req, 1<<12); err != nil {
		writeError(w, 400, "invalid JSON body")
		return
	}
	_, hash, err := s.store.LocalCredentials(r.Context(), u.Username)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if !auth.VerifyPassword(req.Current, hash) {
		s.audit(r, nil, "password_change_failed", u.Username, nil)
		writeError(w, 401, "current password is incorrect")
		return
	}
	if err := auth.CheckPasswordPolicy(req.New); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if req.New == req.Current {
		writeError(w, 400, "new password must differ from the current one")
		return
	}
	newHash, err := auth.HashPassword(req.New)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if err := s.store.SetPassword(r.Context(), u.ID, newHash, false); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	// SetPassword ended all sessions; issue a fresh one so the user stays logged in.
	_, token, err := s.auth.LoginLocalSession(r.Context(), u, clientIP(r), r.UserAgent())
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	s.auth.SetCookie(w, token)
	s.audit(r, nil, "password_change", u.Username, nil)
	w.WriteHeader(204)
}
