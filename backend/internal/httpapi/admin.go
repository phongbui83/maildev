package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/phongbui83/maildev/internal/auth"
	"github.com/phongbui83/maildev/internal/model"
	"github.com/phongbui83/maildev/internal/store"
)

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if users == nil {
		users = []model.User{}
	}
	writeJSON(w, 200, users)
}

type updateUserRequest struct {
	Role     *string `json:"role"`
	Disabled *bool   `json:"disabled"`
	Unlock   bool    `json:"unlock"`
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	actor := auth.UserFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		writeError(w, 400, "invalid user id")
		return
	}
	var req updateUserRequest
	if err := readJSON(r, &req, 1<<12); err != nil {
		writeError(w, 400, "invalid JSON body")
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
	if req.Role != nil {
		switch *req.Role {
		case model.RoleAdmin, model.RoleAuditor, model.RoleUser:
		default:
			writeError(w, 400, "role must be admin, auditor or user")
			return
		}
		if target.ID == actor.ID && *req.Role != model.RoleAdmin {
			writeError(w, 400, "you cannot remove your own admin role")
			return
		}
		if err := s.store.SetUserRole(r.Context(), id, *req.Role); err != nil {
			if errors.Is(err, store.ErrLocalRole) {
				writeError(w, 400, "external (local) accounts cannot be admin or auditor")
				return
			}
			writeError(w, 500, err.Error())
			return
		}
		s.audit(r, nil, "user_role_change", target.Username, map[string]any{"from": target.Role, "to": *req.Role})
	}
	if req.Disabled != nil {
		if target.ID == actor.ID {
			writeError(w, 400, "you cannot disable yourself")
			return
		}
		if err := s.store.SetUserDisabled(r.Context(), id, *req.Disabled); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		s.audit(r, nil, "user_disabled_change", target.Username, map[string]any{"disabled": *req.Disabled})
	}
	if req.Unlock {
		if err := s.store.Unlock(r.Context(), id); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		s.audit(r, nil, "user_unlock", target.Username, nil)
	}
	u, _ := s.store.GetUser(r.Context(), id)
	writeJSON(w, 200, u)
}

func (s *Server) handleLookupUser(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.URL.Query().Get("username"))
	if username == "" {
		writeError(w, 400, "username is required")
		return
	}
	id, err := s.auth.Authenticator().Lookup(r.Context(), username)
	if err != nil {
		writeError(w, 502, err.Error())
		return
	}
	if id == nil {
		writeJSON(w, 200, map[string]any{"found": false, "lookup_available": s.cfg.LDAP.BindDN != "" || s.cfg.AuthMode == "dev"})
		return
	}
	writeJSON(w, 200, map[string]any{"found": true, "username": id.Username, "display_name": id.DisplayName, "email": id.Email, "ldap_uuid": id.UUID})
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	var pid *uuid.UUID
	if v := r.URL.Query().Get("project_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeError(w, 400, "invalid project_id")
			return
		}
		pid = &id
	}
	s.serveAudit(w, r, pid)
}

func (s *Server) serveAudit(w http.ResponseWriter, r *http.Request, projectID *uuid.UUID) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	entries, total, err := s.store.ListAudit(r.Context(), store.ListAuditParams{
		ProjectID: projectID, Action: q.Get("action"), Actor: q.Get("actor"), Protocol: q.Get("protocol"), Limit: limit, Offset: offset,
	})
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"items": entries, "total": total})
}

// handleDeleteUser removes an account. Directory users are re-created (without
// roles or memberships) at their next login; external accounts are gone for good.
func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	actor := auth.UserFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "userID"))
	if err != nil {
		writeError(w, 400, "invalid user id")
		return
	}
	if id == actor.ID {
		writeError(w, 400, "you cannot delete yourself")
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
	if err := s.store.DeleteUser(r.Context(), id); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	s.audit(r, nil, "user_delete", target.Username, map[string]any{"source": target.AuthSource, "role": target.Role})
	w.WriteHeader(204)
}
