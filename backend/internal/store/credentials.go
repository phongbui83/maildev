package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/phongbui83/maildev/internal/model"
	"github.com/phongbui83/maildev/internal/secrets"
)

const credCols = `id, project_id, kind, label, username, secret_enc, created_at, revoked_at, last_used_at`

func (s *Store) scanCredential(row pgx.Row, reveal bool) (*model.Credential, error) {
	var c model.Credential
	var enc []byte
	if err := row.Scan(&c.ID, &c.ProjectID, &c.Kind, &c.Label, &c.Username, &enc, &c.CreatedAt, &c.RevokedAt, &c.LastUsedAt); err != nil {
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
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `UPDATE project_credentials SET revoked_at = now() WHERE project_id = $1 AND revoked_at IS NULL`, project.ID); err != nil {
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
		row := tx.QueryRow(ctx, `
			INSERT INTO project_credentials (project_id, kind, label, username, secret_enc, created_by)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+credCols,
			project.ID, sp.kind, sp.label, sp.username, enc, createdBy)
		c, err := s.scanCredential(row, false)
		if err != nil {
			return nil, err
		}
		c.Secret = sp.secret
		out = append(out, *c)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// ListActiveCredentials returns the project's live credentials with secrets decrypted.
func (s *Store) ListActiveCredentials(ctx context.Context, projectID uuid.UUID) ([]model.Credential, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+credCols+` FROM project_credentials WHERE project_id = $1 AND revoked_at IS NULL ORDER BY kind`, projectID)
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

// Authenticate verifies a username/secret pair of the given kind and returns the
// credential plus its project. The project must be active.
func (s *Store) Authenticate(ctx context.Context, kind, username, secret string) (*model.Credential, *model.Project, error) {
	username = strings.TrimSpace(username)
	if kind == model.CredSMTP {
		username = strings.ToLower(username)
	}
	row := s.pool.QueryRow(ctx, `SELECT `+credCols+` FROM project_credentials WHERE kind = $1 AND username = $2 AND revoked_at IS NULL`, kind, username)
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
	_, _ = s.pool.Exec(ctx, `UPDATE project_credentials SET last_used_at = now() WHERE id = $1`, c.ID)
	return c, p, nil
}

// AuthenticateAPIKey splits a full API key "md_<slug>_<id>.<secret>" ... actually the
// key is "<username>.<secret>", where username is the key id.
func (s *Store) AuthenticateAPIKey(ctx context.Context, key string) (*model.Credential, *model.Project, error) {
	key = strings.TrimSpace(key)
	i := strings.LastIndex(key, ".")
	if i <= 0 || i == len(key)-1 {
		return nil, nil, ErrNotFound
	}
	return s.Authenticate(ctx, model.CredAPI, key[:i], key[i+1:])
}

// LookupSESKey returns the credential for an SES-style access key id without
// checking the secret (SigV4 verification is intentionally skipped: the key only
// routes the message to its project).
func (s *Store) LookupSESKey(ctx context.Context, accessKeyID string) (*model.Credential, *model.Project, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+credCols+` FROM project_credentials WHERE kind = 'ses' AND username = $1 AND revoked_at IS NULL`, accessKeyID)
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
	_, _ = s.pool.Exec(ctx, `UPDATE project_credentials SET last_used_at = now() WHERE id = $1`, c.ID)
	return c, p, nil
}

// LookupAPIKeyID returns the active API credential with the given key id, its
// secret decrypted, plus the project. Used by signature-based APIs (Azure ACS)
// that must recompute an HMAC over the request before trusting it.
func (s *Store) LookupAPIKeyID(ctx context.Context, keyID string) (*model.Credential, *model.Project, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+credCols+` FROM project_credentials WHERE kind = 'api' AND username = $1 AND revoked_at IS NULL`, keyID)
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

// OpenSecret decrypts a stored credential secret (used by signature-based APIs
// that must recompute an HMAC over the request).
func (s *Store) OpenSecret(enc []byte) (string, error) {
	b, err := s.box.Open(enc)
	return string(b), err
}
