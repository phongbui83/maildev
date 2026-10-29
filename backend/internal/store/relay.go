package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/phongbui83/maildev/internal/model"
)

func (s *Store) SetRelayEnabled(ctx context.Context, projectID uuid.UUID, enabled bool) error {
	ct, err := s.pool.Exec(ctx, `UPDATE projects SET relay_enabled = $2, updated_at = now() WHERE id = $1`, projectID, enabled)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetRelayConfig returns the project's relay target. withSecret decrypts the stored
// password / API key for use by the sender; API responses must not use it.
func (s *Store) GetRelayConfig(ctx context.Context, projectID uuid.UUID, withSecret bool) (*model.RelayConfig, error) {
	var c model.RelayConfig
	var enc []byte
	err := s.pool.QueryRow(ctx, `
		SELECT r.project_id, r.kind, r.host, r.port, r.security, r.tls_insecure, r.username, r.secret_enc, r.region, r.access_key_id,
		       r.endpoint, r.from_override, r.allowed_recipients, COALESCE(u.username, ''), r.updated_at
		FROM project_relay_config r LEFT JOIN users u ON u.id = r.updated_by WHERE r.project_id = $1`, projectID).
		Scan(&c.ProjectID, &c.Kind, &c.Host, &c.Port, &c.Security, &c.TLSInsecure, &c.Username, &enc, &c.Region, &c.AccessKeyID,
			&c.Endpoint, &c.FromOverride, &c.AllowedRecipients, &c.UpdatedBy, &c.UpdatedAt)
	if err != nil {
		return nil, wrapNotFound(err)
	}
	c.HasSecret = len(enc) > 0
	if withSecret && c.HasSecret {
		plain, err := s.box.Open(enc)
		if err != nil {
			return nil, fmt.Errorf("decrypt relay secret: %w", err)
		}
		c.Secret = string(plain)
	}
	return &c, nil
}

// UpsertRelayConfig stores the target. An empty Secret keeps the existing one.
func (s *Store) UpsertRelayConfig(ctx context.Context, c *model.RelayConfig, updatedBy *uuid.UUID) error {
	var enc []byte
	if c.Secret != "" {
		var err error
		if enc, err = s.box.Seal([]byte(c.Secret)); err != nil {
			return err
		}
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO project_relay_config (project_id, kind, host, port, security, username, secret_enc, region, access_key_id, endpoint, from_override, allowed_recipients, updated_by, tls_insecure, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14, now())
		ON CONFLICT (project_id) DO UPDATE SET
			kind = EXCLUDED.kind, host = EXCLUDED.host, port = EXCLUDED.port, security = EXCLUDED.security, tls_insecure = EXCLUDED.tls_insecure, username = EXCLUDED.username,
			secret_enc = COALESCE(EXCLUDED.secret_enc, project_relay_config.secret_enc),
			region = EXCLUDED.region, access_key_id = EXCLUDED.access_key_id, endpoint = EXCLUDED.endpoint,
			from_override = EXCLUDED.from_override, allowed_recipients = EXCLUDED.allowed_recipients,
			updated_by = EXCLUDED.updated_by, updated_at = now()`,
		c.ProjectID, c.Kind, c.Host, c.Port, c.Security, c.Username, enc, c.Region, c.AccessKeyID, c.Endpoint, c.FromOverride, c.AllowedRecipients, updatedBy, c.TLSInsecure)
	return err
}

func (s *Store) DeleteRelayConfig(ctx context.Context, projectID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM project_relay_config WHERE project_id = $1`, projectID)
	return err
}

// ListRelayQueue lists emails with a relay status (pending first, newest first).
func (s *Store) ListRelayQueue(ctx context.Context, projectID uuid.UUID, status string, limit, offset int) ([]model.Email, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	where := `e.project_id = $1 AND e.relay_status <> 'none'`
	args := []any{projectID}
	if status != "" {
		args = append(args, status)
		where += ` AND e.relay_status = $2`
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM emails e WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, offset)
	rows, err := s.pool.Query(ctx, `SELECT `+emailListCols+` FROM emails e`+emailJoins+` WHERE `+where+
		` ORDER BY (e.relay_status = 'pending') DESC, COALESCE(e.relay_decided_at, e.relay_requested_at, e.received_at) DESC LIMIT $`+itoaFn(len(args)-1)+` OFFSET $`+itoaFn(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []model.Email{}
	for rows.Next() {
		e, err := scanEmailList(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *e)
	}
	return out, total, rows.Err()
}

// PendingRelayIDs returns every email awaiting approval (for "approve all").
func (s *Store) PendingRelayIDs(ctx context.Context, projectID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM emails WHERE project_id = $1 AND relay_status = 'pending' ORDER BY relay_requested_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *Store) RequestRelay(ctx context.Context, projectID, emailID, userID uuid.UUID) error {
	ct, err := s.pool.Exec(ctx, `
		UPDATE emails SET relay_status = 'pending', relay_requested_by = $3, relay_requested_at = now(),
			relay_decided_by = NULL, relay_decided_at = NULL, relay_error = '', relay_note = ''
		WHERE project_id = $1 AND id = $2 AND relay_status IN ('none', 'failed', 'rejected')`, projectID, emailID, userID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrConflict
	}
	return nil
}

type RelayDecision struct {
	Status   string // sent, failed, rejected
	By       uuid.UUID
	Error    string
	RemoteID string
	Note     string
}

func (s *Store) RecordRelayDecision(ctx context.Context, projectID, emailID uuid.UUID, d RelayDecision) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE emails SET relay_status = $3, relay_decided_by = $4, relay_decided_at = now(), relay_error = $5, relay_remote_id = $6, relay_note = $7
		WHERE project_id = $1 AND id = $2`, projectID, emailID, d.Status, d.By, d.Error, d.RemoteID, d.Note)
	return err
}

// EmailForRelay loads what the senders need: parsed fields, raw bytes and attachment data.
func (s *Store) EmailForRelay(ctx context.Context, projectID, emailID uuid.UUID) (*model.Email, []byte, error) {
	e, err := s.GetEmail(ctx, projectID, emailID)
	if err != nil {
		return nil, nil, err
	}
	raw, err := s.GetRawEmail(ctx, projectID, emailID)
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id, filename, content_type, size, content_id, inline, data, encrypted FROM attachments WHERE email_id = $1 ORDER BY filename`, emailID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	e.Attachments = nil
	for rows.Next() {
		var a model.Attachment
		var encrypted bool
		if err := rows.Scan(&a.ID, &a.Filename, &a.ContentType, &a.Size, &a.ContentID, &a.Inline, &a.Data, &encrypted); err != nil {
			return nil, nil, err
		}
		if a.Data, err = s.decryptRaw(ctx, projectID, a.Data, encrypted); err != nil {
			return nil, nil, err
		}
		a.EmailID = emailID
		e.Attachments = append(e.Attachments, a)
	}
	return e, raw, rows.Err()
}
