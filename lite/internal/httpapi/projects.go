package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/phongbui83/maildev/internal/auth"
	"github.com/phongbui83/maildev/internal/model"
	"github.com/phongbui83/maildev/internal/store"
)

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	list, err := s.store.ListProjectsForUser(r.Context(), u)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if list == nil {
		list = []model.Project{}
	}
	writeJSON(w, 200, list)
}

type createProjectRequest struct {
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	OwnerUsername string `json:"owner_username"`
	RetentionDays int    `json:"retention_days"`
	MaxEmails     int    `json:"max_emails"`
	Init          bool   `json:"init"`
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	u := auth.UserFrom(r.Context())
	if s.cfg.Lite() {
		writeError(w, 403, "the lite edition has a single project; the full edition is needed for more")
		return
	}
	var req createProjectRequest
	if err := readJSON(r, &req, 1<<16); err != nil {
		writeError(w, 400, "invalid JSON body")
		return
	}
	req.Slug = strings.ToLower(strings.TrimSpace(req.Slug))
	req.Name = strings.TrimSpace(req.Name)
	if !slugRe.MatchString(req.Slug) {
		writeError(w, 400, "slug must be 2-63 chars of a-z, 0-9 and '-'")
		return
	}
	if req.Name == "" {
		req.Name = req.Slug
	}
	uid := u.ID
	p, err := s.store.CreateProject(r.Context(), store.CreateProjectParams{
		Slug: req.Slug, Name: req.Name, Description: req.Description,
		RetentionDays: req.RetentionDays, MaxEmails: req.MaxEmails, CreatedBy: &uid,
	})
	if errors.Is(err, store.ErrConflict) {
		writeError(w, 409, "a project with this slug already exists")
		return
	}
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	s.audit(r, p, "project_create", p.Slug, map[string]any{"name": p.Name})

	if owner := strings.ToLower(strings.TrimSpace(req.OwnerUsername)); owner != "" {
		if _, err := s.addMember(r, p, owner, model.MemberOwner); err != nil {
			status := 500
			if errors.Is(err, errUnknownUser) || errors.Is(err, errLocalOwner) {
				status = 400
			}
			if errors.Is(err, errDirectoryUnavailable) {
				status = 503
			}
			writeError(w, status, "project created but owner could not be added: "+err.Error())
			return
		}
	}
	if req.Init {
		if _, err := s.store.InitCredentials(r.Context(), p, &uid); err != nil {
			writeError(w, 500, "project created but credentials could not be generated: "+err.Error())
			return
		}
		s.audit(r, p, "credentials_init", p.Slug, nil)
	}
	writeJSON(w, 201, p)
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	p := projectFrom(r)
	p.MyRole = accessFrom(r).role
	writeJSON(w, 200, p)
}

type updateProjectRequest struct {
	Name          *string `json:"name"`
	Description   *string `json:"description"`
	RetentionDays *int    `json:"retention_days"`
	MaxEmails     *int    `json:"max_emails"`
	Status        *string `json:"status"`
}

func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	p := projectFrom(r)
	var req updateProjectRequest
	if err := readJSON(r, &req, 1<<16); err != nil {
		writeError(w, 400, "invalid JSON body")
		return
	}
	if req.Status != nil && !auth.UserFrom(r.Context()).IsAdmin() {
		writeError(w, 403, "only admins can change project status")
		return
	}
	if req.Status != nil && *req.Status != "active" && *req.Status != "archived" {
		writeError(w, 400, "status must be active or archived")
		return
	}
	if (req.RetentionDays != nil || req.MaxEmails != nil) && !auth.UserFrom(r.Context()).IsAdmin() {
		writeError(w, 403, "only admins can change retention limits")
		return
	}
	if req.RetentionDays != nil && (*req.RetentionDays < 1 || *req.RetentionDays > 180) {
		writeError(w, 400, "retention_days must be between 1 and 180")
		return
	}
	if req.MaxEmails != nil && (*req.MaxEmails < 10 || *req.MaxEmails > 100000) {
		writeError(w, 400, "max_emails must be between 10 and 100000")
		return
	}
	up, err := s.store.UpdateProject(r.Context(), p.ID, store.UpdateProjectParams{
		Name: req.Name, Description: req.Description, RetentionDays: req.RetentionDays, MaxEmails: req.MaxEmails, Status: req.Status,
	})
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	s.audit(r, p, "project_update", p.Slug, map[string]any{"changes": req})
	up.MyRole = accessFrom(r).role
	writeJSON(w, 200, up)
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	p := projectFrom(r)
	if p.AutoJoin {
		writeError(w, 400, "the auto-join demo project cannot be deleted; archive it instead")
		return
	}
	if err := s.store.DeleteProject(r.Context(), p.ID); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	shredded := false
	if s.enc != nil {
		if err := s.enc.Shred(r.Context(), p.Slug, p.ID); err != nil {
			slog.Warn("transit key not deleted", "project", p.Slug, "err", err)
		} else {
			shredded = true
		}
	}
	s.audit(r, nil, "project_delete", p.Slug, map[string]any{"project_id": p.ID, "key_shredded": shredded})
	w.WriteHeader(204)
}

func (s *Server) handleInitCredentials(w http.ResponseWriter, r *http.Request) {
	p := projectFrom(r)
	u := auth.UserFrom(r.Context())
	existing, err := s.store.ListActiveCredentials(r.Context(), p.ID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	reinit := r.URL.Query().Get("reinit") == "1" || r.URL.Query().Get("reinit") == "true"
	if len(existing) > 0 && !reinit {
		writeError(w, 409, "credentials already exist; pass ?reinit=1 to rotate them")
		return
	}
	uid := u.ID
	creds, err := s.store.InitCredentials(r.Context(), p, &uid)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	action := "credentials_init"
	if len(existing) > 0 {
		action = "credentials_reinit"
	}
	s.audit(r, p, action, p.Slug, map[string]any{"revoked": len(existing)})
	s.ingest.Notify("credentials", p.ID, uuid.Nil, 0)
	writeJSON(w, 200, creds)
}

func (s *Server) handleListCredentials(w http.ResponseWriter, r *http.Request) {
	p := projectFrom(r)
	creds, err := s.store.ListActiveCredentials(r.Context(), p.ID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if creds == nil {
		creds = []model.Credential{}
	}
	s.audit(r, p, "credentials_view", p.Slug, nil)
	writeJSON(w, 200, creds)
}

// Members

func (s *Server) handleListMembers(w http.ResponseWriter, r *http.Request) {
	p := projectFrom(r)
	members, err := s.store.ListMembers(r.Context(), p.ID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if members == nil {
		members = []model.Member{}
	}
	if s.dir.Enabled() {
		for i := range members {
			if members[i].Pending {
				if du, ok := s.dir.Lookup(members[i].Username); ok {
					members[i].DisplayName, members[i].Email = du.FullName, du.Email
				}
			}
		}
	}
	writeJSON(w, 200, members)
}

type addMemberRequest struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

// addMember verifies that the account exists, then adds an existing user
// directly or records an invite that is applied on the user's first login.
// Rules: the username must be a known directory (AD) account or an existing
// local account; local (external) accounts can only be members, never owners.
// Returns true when the membership was immediate.
func (s *Server) addMember(r *http.Request, p *model.Project, username, role string) (bool, error) {
	actor := auth.UserFrom(r.Context())
	actorID := actor.ID
	username = strings.ToLower(strings.TrimSpace(username))
	target, err := s.store.GetUserByUsername(r.Context(), username)
	if err == nil {
		if target.IsLocal() && role == model.MemberOwner && !s.cfg.Lite() {
			return false, errLocalOwner
		}
		if target.Disabled {
			return false, errUnknownUser
		}
		if err := s.store.UpsertMember(r.Context(), p.ID, target.ID, role, &actorID); err != nil {
			return false, err
		}
		s.audit(r, p, "member_add", username, map[string]any{"role": role, "source": target.AuthSource})
		return true, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return false, err
	}
	// Not logged in yet: must exist in the company directory; without a directory
	// fall back to the LDAP lookup (service bind) or, in dev auth mode, accept.
	known := false
	if s.dir.Enabled() {
		if !s.dir.Ready() {
			return false, errDirectoryUnavailable
		}
		_, known = s.dir.Lookup(username)
	} else if id, lerr := s.auth.Authenticator().Lookup(r.Context(), username); lerr == nil && id != nil {
		known = true
	}
	if !known {
		return false, errUnknownUser
	}
	if err := s.store.UpsertInvite(r.Context(), p.ID, username, role, &actorID); err != nil {
		return false, err
	}
	s.audit(r, p, "member_invite", username, map[string]any{"role": role})
	return false, nil
}

var errUnknownUser = errors.New("user not found in directory")
var errLocalOwner = errors.New("external (local) accounts can only be members")
var errDirectoryUnavailable = errors.New("company directory not loaded yet, try again shortly")

func (s *Server) handleAddMember(w http.ResponseWriter, r *http.Request) {
	p := projectFrom(r)
	var req addMemberRequest
	if err := readJSON(r, &req, 1<<16); err != nil {
		writeError(w, 400, "invalid JSON body")
		return
	}
	if req.Role == "" {
		req.Role = model.MemberMember
	}
	if req.Role != model.MemberOwner && req.Role != model.MemberMember {
		writeError(w, 400, "role must be owner or member")
		return
	}
	if strings.TrimSpace(req.Username) == "" {
		writeError(w, 400, "username is required")
		return
	}
	immediate, err := s.addMember(r, p, req.Username, req.Role)
	if errors.Is(err, errUnknownUser) {
		writeError(w, 404, "user not found: must be an Active Directory account or an existing external account")
		return
	}
	if errors.Is(err, errLocalOwner) {
		writeError(w, 400, "external (local) accounts can only be members, not owners")
		return
	}
	if errors.Is(err, errDirectoryUnavailable) {
		writeError(w, 503, err.Error())
		return
	}
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"username": strings.ToLower(req.Username), "role": req.Role, "pending": !immediate})
}

func (s *Server) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	p := projectFrom(r)
	username := strings.ToLower(chi.URLParam(r, "username"))
	actor := auth.UserFrom(r.Context())
	if target, err := s.store.GetUserByUsername(r.Context(), username); err == nil {
		if target.ID == actor.ID && !actor.IsAdmin() {
			writeError(w, 400, "you cannot remove yourself")
			return
		}
		if err := s.store.RemoveMember(r.Context(), p.ID, target.ID); err != nil {
			writeError(w, 500, err.Error())
			return
		}
	}
	if err := s.store.RemoveInvite(r.Context(), p.ID, username); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	s.audit(r, p, "member_remove", username, nil)
	w.WriteHeader(204)
}

func (s *Server) handleProjectStats(w http.ResponseWriter, r *http.Request) {
	p := projectFrom(r)
	st, err := s.store.ProjectStats(r.Context(), p.ID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, st)
}

func (s *Server) handleProjectAudit(w http.ResponseWriter, r *http.Request) {
	p := projectFrom(r)
	if !accessFrom(r).canManage() && !auth.UserFrom(r.Context()).IsAuditor() {
		writeError(w, 403, "owner, admin or auditor role required")
		return
	}
	pid := p.ID
	s.serveAudit(w, r, &pid)
}
