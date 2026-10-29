package store

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/google/uuid"

	"github.com/phongbui83/maildev/internal/model"
)

type AuditParams struct {
	ActorUserID *uuid.UUID
	Actor       string
	ProjectID   *uuid.UUID
	Action      string
	Target      string
	Detail      map[string]any
	IP          string
	Protocol    string // web (default), smtp, imap, pop3, api, sendgrid, ses, system
}

// Audit writes an audit entry. Failures are logged, never returned, so that an
// audit outage does not block the action itself.
func (s *Store) Audit(ctx context.Context, p AuditParams) {
	if p.Detail == nil {
		p.Detail = map[string]any{}
	}
	if p.Protocol == "" {
		p.Protocol = "web"
	}
	detail, _ := json.Marshal(p.Detail)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO audit_log (actor_user_id, actor, project_id, action, target, detail, ip, protocol)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, p.ActorUserID, p.Actor, p.ProjectID, p.Action, p.Target, detail, p.IP, p.Protocol)
	if err != nil {
		slog.Error("audit write failed", "action", p.Action, "err", err)
	}
}

type ListAuditParams struct {
	ProjectID *uuid.UUID
	Action    string
	Actor     string
	Protocol  string
	Limit     int
	Offset    int
}

func (s *Store) ListAudit(ctx context.Context, p ListAuditParams) ([]model.AuditEntry, int, error) {
	if p.Limit <= 0 || p.Limit > 500 {
		p.Limit = 100
	}
	where := `TRUE`
	args := []any{}
	if p.ProjectID != nil {
		args = append(args, *p.ProjectID)
		where += ` AND a.project_id = $` + itoaFn(len(args))
	}
	if p.Action != "" {
		args = append(args, p.Action)
		where += ` AND a.action = $` + itoaFn(len(args))
	}
	if p.Actor != "" {
		args = append(args, "%"+p.Actor+"%")
		where += ` AND a.actor ILIKE $` + itoaFn(len(args))
	}
	if p.Protocol != "" {
		args = append(args, p.Protocol)
		where += ` AND a.protocol = $` + itoaFn(len(args))
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM audit_log a WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, p.Limit, p.Offset)
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.at, a.actor_user_id, a.actor, a.project_id, COALESCE(p.slug, ''), a.action, a.target, a.detail, a.ip, a.protocol
		FROM audit_log a LEFT JOIN projects p ON p.id = a.project_id
		WHERE `+where+` ORDER BY a.at DESC, a.id DESC LIMIT $`+itoaFn(len(args)-1)+` OFFSET $`+itoaFn(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []model.AuditEntry{}
	for rows.Next() {
		var e model.AuditEntry
		var detail []byte
		if err := rows.Scan(&e.ID, &e.At, &e.ActorUserID, &e.Actor, &e.ProjectID, &e.ProjectSlug, &e.Action, &e.Target, &detail, &e.IP, &e.Protocol); err != nil {
			return nil, 0, err
		}
		_ = json.Unmarshal(detail, &e.Detail)
		out = append(out, e)
	}
	return out, total, rows.Err()
}
