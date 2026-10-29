package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/phongbui83/maildev/internal/auth"
	"github.com/phongbui83/maildev/internal/model"
	"github.com/phongbui83/maildev/internal/relay"
	"github.com/phongbui83/maildev/internal/store"
)

// Relay: admins enable it per project and configure the target; members may
// request a relay; owners (or admins) approve or reject, one by one or in batch.
// Every step is audited.

func (s *Server) handleGetRelay(w http.ResponseWriter, r *http.Request) {
	p := projectFrom(r)
	if !accessFrom(r).canManage() && !auth.UserFrom(r.Context()).IsAuditor() {
		writeError(w, 403, "owner, admin or auditor role required")
		return
	}
	cfg, err := s.store.GetRelayConfig(r.Context(), p.ID, false)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, 200, map[string]any{"enabled": p.RelayEnabled, "config": nil})
		return
	}
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"enabled": p.RelayEnabled, "config": cfg})
}

type relayUpdateRequest struct {
	Enabled           *bool  `json:"enabled"`
	Kind              string `json:"kind"`
	Host              string `json:"host"`
	Port              int    `json:"port"`
	Security          string `json:"security"`
	TLSInsecure       bool   `json:"tls_insecure"`
	Username          string `json:"username"`
	Secret            string `json:"secret"` // empty keeps the stored secret
	Region            string `json:"region"`
	AccessKeyID       string `json:"access_key_id"`
	Endpoint          string `json:"endpoint"`
	FromOverride      string `json:"from_override"`
	AllowedRecipients string `json:"allowed_recipients"`
}

// handleUpdateRelay (admin only) stores the target and/or toggles relay.
func (s *Server) handleUpdateRelay(w http.ResponseWriter, r *http.Request) {
	p := projectFrom(r)
	u := auth.UserFrom(r.Context())
	var req relayUpdateRequest
	if err := readJSON(r, &req, 1<<16); err != nil {
		writeError(w, 400, "invalid JSON body")
		return
	}
	if req.Kind != "" {
		cfg := &model.RelayConfig{
			ProjectID: p.ID, Kind: req.Kind, Host: strings.TrimSpace(req.Host), Port: req.Port, Security: req.Security, TLSInsecure: req.TLSInsecure,
			Username: strings.TrimSpace(req.Username), Secret: req.Secret, Region: strings.TrimSpace(req.Region),
			AccessKeyID: strings.TrimSpace(req.AccessKeyID), Endpoint: strings.TrimSpace(req.Endpoint),
			FromOverride: strings.TrimSpace(req.FromOverride), AllowedRecipients: strings.TrimSpace(req.AllowedRecipients),
		}
		if cfg.Security == "" {
			cfg.Security = "starttls"
		}
		if cfg.Port == 0 {
			cfg.Port = 587
		}
		if err := relay.Validate(cfg); err != nil {
			writeError(w, 400, err.Error())
			return
		}
		uid := u.ID
		if err := s.store.UpsertRelayConfig(r.Context(), cfg, &uid); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		s.audit(r, p, "relay_config_update", p.Slug, map[string]any{
			"kind": cfg.Kind, "host": cfg.Host, "port": cfg.Port, "security": cfg.Security, "tls_insecure": cfg.TLSInsecure, "username": cfg.Username,
			"region": cfg.Region, "access_key_id": cfg.AccessKeyID, "endpoint": cfg.Endpoint,
			"from_override": cfg.FromOverride, "allowed_recipients": cfg.AllowedRecipients, "secret_changed": req.Secret != "",
		})
	}
	if req.Enabled != nil && *req.Enabled != p.RelayEnabled {
		if *req.Enabled {
			cfg, err := s.store.GetRelayConfig(r.Context(), p.ID, false)
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, 400, "configure a relay target before enabling relay")
				return
			}
			if err != nil {
				writeError(w, 500, err.Error())
				return
			}
			if cfg.Kind != model.RelaySES && !cfg.HasSecret && cfg.Username != "" {
				writeError(w, 400, "relay target has no password / API key")
				return
			}
		}
		if err := s.store.SetRelayEnabled(r.Context(), p.ID, *req.Enabled); err != nil {
			writeError(w, 500, err.Error())
			return
		}
		action := "relay_disable"
		if *req.Enabled {
			action = "relay_enable"
		}
		s.audit(r, p, action, p.Slug, nil)
		p.RelayEnabled = *req.Enabled
		s.ingest.Notify("project", p.ID, uuid.Nil, 0)
	}
	s.handleGetRelay(w, r)
}

// handleTestRelay (admin) checks connectivity/authentication for SMTP targets.
func (s *Server) handleTestRelay(w http.ResponseWriter, r *http.Request) {
	p := projectFrom(r)
	cfg, err := s.store.GetRelayConfig(r.Context(), p.ID, true)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, 400, "no relay target configured")
		return
	}
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var testErr error
	switch cfg.Kind {
	case model.RelaySMTP:
		testErr = relay.TestSMTP(ctx, cfg)
	default:
		writeJSON(w, 200, map[string]any{"ok": true, "message": "configuration saved; " + cfg.Kind + " targets are verified on first approved relay"})
		return
	}
	s.audit(r, p, "relay_test", p.Slug, map[string]any{"ok": testErr == nil, "error": errString(testErr)})
	if testErr != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "message": testErr.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "message": "connected and authenticated to " + cfg.Host})
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *Server) handleRelayQueue(w http.ResponseWriter, r *http.Request) {
	p := projectFrom(r)
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	items, total, err := s.store.ListRelayQueue(r.Context(), p.ID, q.Get("status"), limit, offset)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total})
}

// handleRelayRequest (member+) queues a captured message for owner approval.
func (s *Server) handleRelayRequest(w http.ResponseWriter, r *http.Request) {
	p := projectFrom(r)
	u := auth.UserFrom(r.Context())
	if !p.RelayEnabled {
		writeError(w, 400, "relay is not enabled for this project")
		return
	}
	id, err := emailIDFrom(r)
	if err != nil {
		writeError(w, 400, "invalid email id")
		return
	}
	if err := s.store.RequestRelay(r.Context(), p.ID, id, u.ID); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, 409, "message is already pending or sent")
			return
		}
		writeError(w, 500, err.Error())
		return
	}
	s.audit(r, p, "relay_request", id.String(), nil)
	s.ingest.Notify("relay", p.ID, id, 0)
	w.WriteHeader(204)
}

type relayDecisionRequest struct {
	IDs  []uuid.UUID `json:"ids"`
	All  bool        `json:"all"` // every pending message
	Note string      `json:"note"`
}

type relayOutcome struct {
	ID       uuid.UUID `json:"id"`
	Status   string    `json:"status"`
	Error    string    `json:"error,omitempty"`
	RemoteID string    `json:"remote_id,omitempty"`
}

func (s *Server) collectDecisionIDs(r *http.Request, p *model.Project) ([]uuid.UUID, *relayDecisionRequest, error) {
	var req relayDecisionRequest
	if eid := strings.TrimSpace(chiURLParam(r, "emailID")); eid != "" {
		id, err := uuid.Parse(eid)
		if err != nil {
			return nil, nil, errors.New("invalid email id")
		}
		_ = readJSON(r, &req, 1<<14) // optional note
		return []uuid.UUID{id}, &req, nil
	}
	if err := readJSON(r, &req, 1<<20); err != nil {
		return nil, nil, errors.New("invalid JSON body")
	}
	if req.All {
		ids, err := s.store.PendingRelayIDs(r.Context(), p.ID)
		if err != nil {
			return nil, nil, err
		}
		return ids, &req, nil
	}
	if len(req.IDs) == 0 {
		return nil, nil, errors.New("ids is required (or all: true)")
	}
	if len(req.IDs) > 500 {
		return nil, nil, errors.New("at most 500 messages per batch")
	}
	return req.IDs, &req, nil
}

// handleRelayApprove (owner/admin) sends one message or a batch. Messages are
// processed sequentially; the response lists the outcome of each.
func (s *Server) handleRelayApprove(w http.ResponseWriter, r *http.Request) {
	p := projectFrom(r)
	u := auth.UserFrom(r.Context())
	if !p.RelayEnabled {
		writeError(w, 400, "relay is not enabled for this project")
		return
	}
	cfg, err := s.store.GetRelayConfig(r.Context(), p.ID, true)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, 400, "no relay target configured")
		return
	}
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	ids, req, err := s.collectDecisionIDs(r, p)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	outcomes := make([]relayOutcome, 0, len(ids))
	for _, id := range ids {
		outcomes = append(outcomes, s.relayOne(r, p, u, cfg, id, req.Note))
	}
	s.ingest.Notify("relay", p.ID, uuid.Nil, 0)
	writeJSON(w, 200, map[string]any{"results": outcomes})
}

func (s *Server) relayOne(r *http.Request, p *model.Project, u *model.User, cfg *model.RelayConfig, id uuid.UUID, note string) relayOutcome {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	e, raw, err := s.store.EmailForRelay(ctx, p.ID, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return relayOutcome{ID: id, Status: "not_found"}
		}
		return relayOutcome{ID: id, Status: "error", Error: err.Error()}
	}
	if e.RelayStatus == model.RelaySent {
		return relayOutcome{ID: id, Status: "already_sent", RemoteID: e.RelayRemoteID}
	}
	s.audit(r, p, "relay_approve", id.String(), map[string]any{"subject": e.Subject, "recipients": relay.Recipients(e), "note": note})

	res, sendErr := relay.Send(ctx, cfg, e, raw)
	d := store.RelayDecision{By: u.ID, Note: note}
	if sendErr != nil {
		d.Status = model.RelayFailed
		d.Error = sendErr.Error()
		_ = s.store.RecordRelayDecision(ctx, p.ID, id, d)
		s.audit(r, p, "relay_failed", id.String(), map[string]any{"error": sendErr.Error(), "kind": cfg.Kind})
		return relayOutcome{ID: id, Status: model.RelayFailed, Error: sendErr.Error()}
	}
	d.Status = model.RelaySent
	d.RemoteID = res.RemoteID
	_ = s.store.RecordRelayDecision(ctx, p.ID, id, d)
	s.audit(r, p, "relay_sent", id.String(), map[string]any{"kind": cfg.Kind, "remote_id": res.RemoteID, "detail": res.Detail, "recipients": relay.Recipients(e), "from": relay.Sender(cfg, e)})
	return relayOutcome{ID: id, Status: model.RelaySent, RemoteID: res.RemoteID}
}

// handleRelayReject (owner/admin) marks messages as rejected, one or in batch.
func (s *Server) handleRelayReject(w http.ResponseWriter, r *http.Request) {
	p := projectFrom(r)
	u := auth.UserFrom(r.Context())
	ids, req, err := s.collectDecisionIDs(r, p)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	outcomes := make([]relayOutcome, 0, len(ids))
	for _, id := range ids {
		if err := s.store.RecordRelayDecision(r.Context(), p.ID, id, store.RelayDecision{Status: model.RelayRejected, By: u.ID, Note: req.Note}); err != nil {
			outcomes = append(outcomes, relayOutcome{ID: id, Status: "error", Error: err.Error()})
			continue
		}
		s.audit(r, p, "relay_reject", id.String(), map[string]any{"note": req.Note})
		outcomes = append(outcomes, relayOutcome{ID: id, Status: model.RelayRejected})
	}
	s.ingest.Notify("relay", p.ID, uuid.Nil, 0)
	writeJSON(w, 200, map[string]any{"results": outcomes})
}
