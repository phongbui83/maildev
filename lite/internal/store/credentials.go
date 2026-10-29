package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/phongbui83/maildev/internal/model"
	"github.com/phongbui83/maildev/internal/secrets"
)

const credCols = `id, project_id, kind, label, username, secret_enc, created_at, revoked_at, last_used_at`

func (s *Store) scanCredential(row rowScanner, reveal bool) (*model.Credential, error) {
	var c model.Credential
	var enc []byte
	if err := row.Scan(&c.ID, &c.ProjectID, &c.Kind, &c.Label, &c.Username, &enc, ts(&c.CreatedAt), nt(&c.RevokedAt), nt(&c.LastUsedAt)); err != nil {
		return nil, wrapNotFound(err)
	}
	if reveal {
		plain, err := s.box.Open(enc)
		if err != nil {
			return nil, fmt.Errorf("decrypt credential %s: %w", c.ID, err)
		}
		c.Secret = string(plain)
	}
	return &c, nil
}

// InitCredentials revokes any active credentials of the project and issues a fresh
// SMTP/IMAP login, a generic API key and an SES-style key pair.
func (s *Store) InitCredentials(ctx context.Context, project *model.Project, createdBy *uuid.UUID) ([]model.Credential, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	n := now()
	if _, err := tx.ExecContext(ctx, `UPDATE project_credentials SET revoked_at = ? WHERE project_id = ? AND revoked_at IS NULL`, n, project.ID); err != nil {
		return nil, err
	}
	type spec struct{ kind, label, username, secret string }
	specs := []spec{
		{model.CredSMTP, "SMTP / IMAP login", project.Slug, secrets.Password(28)},
		{model.CredAPI, "REST / SendGrid-compatible API key", "md_" + project.Slug + "_" + secrets.KeyID(10), secrets.Password(40)},
		{model.CredSES, "AWS SES-compatible access key", secrets.AWSAccessKeyID(), secrets.AWSSecret()},
	}
	var out []model.Credential
	for _, sp := range specs {
		enc, err := s.box.Seal([]byte(sp.secret))
		if err != nil {
			return nil, err
		}
		id := newID()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO project_credentials (id, project_id, kind, label, username, secret_enc, created_by, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, project.ID, sp.kind, sp.label, sp.username, enc, createdBy, n); err != nil {
			return nil, err
		}
		out = append(out, model.Credential{ID: id, ProjectID: project.ID, Kind: sp.kind, Label: sp.label, Username: sp.username, Secret: sp.secret, CreatedAt: n})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

// ListActiveCredentials returns the project's live credentials with secrets decrypted.
func (s *Store) ListActiveCredentials(ctx context.Context, projectID uuid.UUID) ([]model.Credential, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+credCols+` FROM project_credentials WHERE project_id = ? AND revoked_at IS NULL ORDER BY kind`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Credential
	for rows.Next() {
		c, err := s.scanCredential(rows, true)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (s *Store) touchCredential(ctx context.Context, id uuid.UUID) {
	_, _ = s.db.ExecContext(ctx, `UPDATE project_credentials SET last_used_at = ? WHERE id = ?`, now(), id)
}

// Authenticate verifies a username/secret pair of the given kind and returns the
// credential plus its project. The project must be active.
func (s *Store) Authenticate(ctx context.Context, kind, username, secret string) (*model.Credential, *model.Project, error) {
	username = strings.TrimSpace(username)
	if kind == model.CredSMTP {
		username = strings.ToLower(username)
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+credCols+` FROM project_credentials WHERE kind = ? AND username = ? AND revoked_at IS NULL`, kind, username)
	c, err := s.scanCredential(row, true)
	if err != nil {
		return nil, nil, err
	}
	if !secrets.Equal(c.Secret, secret) {
		return nil, nil, ErrNotFound
	}
	p, err := s.GetProject(ctx, c.ProjectID)
	if err != nil {
		return nil, nil, err
	}
	if p.Status != "active" {
		return nil, nil, ErrNotFound
	}
	c.Secret = ""
	s.touchCredential(ctx, c.ID)
	return c, p, nil
}

// AuthenticateAPIKey checks a full key "<key id>.<secret>".
func (s *Store) AuthenticateAPIKey(ctx context.Context, key string) (*model.Credential, *model.Project, error) {
	key = strings.TrimSpace(key)
	i := strings.LastIndex(key, ".")
	if i <= 0 || i == len(key)-1 {
		return nil, nil, ErrNotFound
	}
	return s.Authenticate(ctx, model.CredAPI, key[:i], key[i+1:])
}

// LookupSESKey returns the credential for an SES-style access key id without
// checking the secret (the key only routes the message to its project).
func (s *Store) LookupSESKey(ctx context.Context, accessKeyID string) (*model.Credential, *model.Project, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+credCols+` FROM project_credentials WHERE kind = 'ses' AND username = ? AND revoked_at IS NULL`, accessKeyID)
	c, err := s.scanCredential(row, false)
	if err != nil {
		return nil, nil, err
	}
	p, err := s.GetProject(ctx, c.ProjectID)
	if err != nil {
		return nil, nil, err
	}
	if p.Status != "active" {
		return nil, nil, ErrNotFound
	}
	s.touchCredential(ctx, c.ID)
	return c, p, nil
}

// LookupAPIKeyID returns the active API credential with its secret decrypted
// (signature-based APIs recompute an HMAC over the request).
func (s *Store) LookupAPIKeyID(ctx context.Context, keyID string) (*model.Credential, *model.Project, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+credCols+` FROM project_credentials WHERE kind = 'api' AND username = ? AND revoked_at IS NULL`, keyID)
	c, err := s.scanCredential(row, true)
	if err != nil {
		return nil, nil, err
	}
	p, err := s.GetProject(ctx, c.ProjectID)
	if err != nil {
		return nil, nil, err
	}
	if p.Status != "active" {
		return nil, nil, ErrNotFound
	}
	return c, p, nil
}

// OpenSecret decrypts a stored credential secret.
func (s *Store) OpenSecret(enc []byte) (string, error) {
	b, err := s.box.Open(enc)
	return string(b), err
}
