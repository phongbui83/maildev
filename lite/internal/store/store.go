// Package store is the SQLite persistence layer of the lite edition. It exposes
// the same API as the PostgreSQL store of the full edition so that every other
// package (servers, HTTP API, auth, ingest, relay) is shared verbatim.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"modernc.org/sqlite"

	"github.com/phongbui83/maildev/internal/secrets"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict")

type Store struct {
	db  *sql.DB
	box *secrets.Box
	// lite relaxes the full edition's rules for password accounts: they may
	// hold admin/auditor and they auto-join projects like directory users.
	lite bool
}

// SetLite enables the lite-edition rules for local accounts.
func (s *Store) SetLite(v bool) { s.lite = v }

// Open opens (and creates) the SQLite database at path and applies the schema.
func Open(ctx context.Context, path string, box *secrets.Box) (*Store, error) {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Set("_txlock", "immediate")   // write transactions take the lock at BEGIN: no upgrade deadlocks
	q.Set("_time_format", "sqlite") // time.Time stored as "2006-01-02 15:04:05.999999999-07:00"
	dsn := "file:" + path + "?" + q.Encode()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetConnMaxLifetime(0)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	s := &Store{db: db, box: box}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// DB exposes the handle for tests and maintenance tools.
func (s *Store) DB() *sql.DB { return s.db }

// Ping is used by the readiness probe.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func wrapNotFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func isUniqueViolation(err error) bool {
	var se *sqlite.Error
	if errors.As(err, &se) {
		return se.Code() == 2067 || se.Code() == 1555 // SQLITE_CONSTRAINT_UNIQUE / _PRIMARYKEY
	}
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func now() time.Time { return time.Now().UTC() }

func newID() uuid.UUID { return uuid.New() }

// --- time scanning -------------------------------------------------------
// Columns declared TIMESTAMP come back as time.Time, but expressions (max(),
// UNION, RETURNING) lose the declared type and arrive as text, so every time
// column is read through these scanners.

var timeLayouts = []string{
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02T15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
	time.RFC3339Nano,
}

func parseTime(v any) (*time.Time, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case time.Time:
		t := x.UTC()
		return &t, nil
	case []byte:
		return parseTime(string(x))
	case string:
		if x == "" {
			return nil, nil
		}
		for _, l := range timeLayouts {
			if t, err := time.Parse(l, x); err == nil {
				t = t.UTC()
				return &t, nil
			}
		}
		return nil, fmt.Errorf("unparseable time %q", x)
	case int64:
		t := time.Unix(x, 0).UTC()
		return &t, nil
	}
	return nil, fmt.Errorf("unsupported time type %T", v)
}

type tsScanner struct{ dst *time.Time }

func (t tsScanner) Scan(v any) error {
	p, err := parseTime(v)
	if err != nil {
		return err
	}
	if p == nil {
		*t.dst = time.Time{}
	} else {
		*t.dst = *p
	}
	return nil
}

type ntScanner struct{ dst **time.Time }

func (t ntScanner) Scan(v any) error {
	p, err := parseTime(v)
	if err != nil {
		return err
	}
	*t.dst = p
	return nil
}

func ts(dst *time.Time) tsScanner  { return tsScanner{dst} }
func nt(dst **time.Time) ntScanner { return ntScanner{dst} }

// utc normalises optional times passed as arguments.
func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// placeholders returns "?,?,?" for n values.
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// --- schema --------------------------------------------------------------

var migrations = []struct {
	version string
	sql     string
}{
	{"0001_init", schemaV1},
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMP NOT NULL)`); err != nil {
		return err
	}
	for _, m := range migrations {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version = ?`, m.version).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", m.version, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, m.version, now()); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

const schemaV1 = `
CREATE TABLE IF NOT EXISTS users (
    id                   TEXT PRIMARY KEY,
    ldap_uuid            TEXT UNIQUE,
    username             TEXT UNIQUE NOT NULL,
    display_name         TEXT NOT NULL DEFAULT '',
    email                TEXT NOT NULL DEFAULT '',
    department           TEXT NOT NULL DEFAULT '',
    role                 TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('admin', 'auditor', 'user')),
    disabled             INTEGER NOT NULL DEFAULT 0,
    auth_source          TEXT NOT NULL DEFAULT 'local' CHECK (auth_source IN ('ldap', 'local')),
    password_hash        TEXT NOT NULL DEFAULT '',
    must_change_password INTEGER NOT NULL DEFAULT 0,
    failed_logins        INTEGER NOT NULL DEFAULT 0,
    locked_until         TIMESTAMP,
    created_by           TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at           TIMESTAMP NOT NULL,
    last_login_at        TIMESTAMP
);

CREATE TABLE IF NOT EXISTS sessions (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMP NOT NULL,
    expires_at TIMESTAMP NOT NULL,
    ip         TEXT NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS sessions_user_idx ON sessions(user_id);
CREATE INDEX IF NOT EXISTS sessions_expires_idx ON sessions(expires_at);

CREATE TABLE IF NOT EXISTS projects (
    id             TEXT PRIMARY KEY,
    slug           TEXT UNIQUE NOT NULL,
    name           TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
    auto_join      INTEGER NOT NULL DEFAULT 0,
    relay_enabled  INTEGER NOT NULL DEFAULT 0,
    retention_days INTEGER NOT NULL DEFAULT 14 CHECK (retention_days >= 1 AND retention_days <= 180),
    max_emails     INTEGER NOT NULL DEFAULT 5000 CHECK (max_emails >= 10),
    uid_validity   INTEGER NOT NULL,
    next_uid       INTEGER NOT NULL DEFAULT 1,
    created_by     TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at     TIMESTAMP NOT NULL,
    updated_at     TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS project_members (
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       TEXT NOT NULL CHECK (role IN ('owner', 'member')),
    added_by   TEXT REFERENCES users(id) ON DELETE SET NULL,
    added_at   TIMESTAMP NOT NULL,
    PRIMARY KEY (project_id, user_id)
);
CREATE INDEX IF NOT EXISTS project_members_user_idx ON project_members(user_id);

CREATE TABLE IF NOT EXISTS project_invites (
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    username   TEXT NOT NULL,
    role       TEXT NOT NULL CHECK (role IN ('owner', 'member')),
    invited_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    invited_at TIMESTAMP NOT NULL,
    PRIMARY KEY (project_id, username)
);

CREATE TABLE IF NOT EXISTS project_credentials (
    id           TEXT PRIMARY KEY,
    project_id   TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    kind         TEXT NOT NULL CHECK (kind IN ('smtp', 'api', 'ses')),
    label        TEXT NOT NULL DEFAULT '',
    username     TEXT NOT NULL,
    secret_enc   BLOB NOT NULL,
    created_by   TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMP NOT NULL,
    revoked_at   TIMESTAMP,
    last_used_at TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS project_credentials_active_username ON project_credentials(kind, username) WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS project_credentials_project_idx ON project_credentials(project_id);

CREATE TABLE IF NOT EXISTS project_relay_config (
    project_id         TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    kind               TEXT NOT NULL CHECK (kind IN ('smtp', 'ses', 'sendgrid')),
    host               TEXT NOT NULL DEFAULT '',
    port               INTEGER NOT NULL DEFAULT 587,
    security           TEXT NOT NULL DEFAULT 'starttls' CHECK (security IN ('none', 'starttls', 'tls')),
    tls_insecure       INTEGER NOT NULL DEFAULT 0,
    username           TEXT NOT NULL DEFAULT '',
    secret_enc         BLOB,
    region             TEXT NOT NULL DEFAULT '',
    access_key_id      TEXT NOT NULL DEFAULT '',
    endpoint           TEXT NOT NULL DEFAULT '',
    from_override      TEXT NOT NULL DEFAULT '',
    allowed_recipients TEXT NOT NULL DEFAULT '',
    updated_by         TEXT REFERENCES users(id) ON DELETE SET NULL,
    updated_at         TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS emails (
    id                 TEXT PRIMARY KEY,
    project_id         TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    uid                INTEGER NOT NULL,
    credential_id      TEXT REFERENCES project_credentials(id) ON DELETE SET NULL,
    source             TEXT NOT NULL,
    client_ip          TEXT NOT NULL DEFAULT '',
    received_at        TIMESTAMP NOT NULL,
    message_id         TEXT NOT NULL DEFAULT '',
    subject            TEXT NOT NULL DEFAULT '',
    from_addrs         TEXT NOT NULL DEFAULT '[]',
    to_addrs           TEXT NOT NULL DEFAULT '[]',
    cc_addrs           TEXT NOT NULL DEFAULT '[]',
    bcc_addrs          TEXT NOT NULL DEFAULT '[]',
    envelope_from      TEXT NOT NULL DEFAULT '',
    envelope_to        TEXT NOT NULL DEFAULT '[]',
    sent_at            TIMESTAMP,
    size               INTEGER NOT NULL,
    text_body          TEXT NOT NULL DEFAULT '',
    html_body          TEXT NOT NULL DEFAULT '',
    headers            TEXT NOT NULL DEFAULT '[]',
    attachment_count   INTEGER NOT NULL DEFAULT 0,
    seen               INTEGER NOT NULL DEFAULT 0,
    flagged            INTEGER NOT NULL DEFAULT 0,
    deleted            INTEGER NOT NULL DEFAULT 0,
    raw                BLOB NOT NULL,
    addr_text          TEXT NOT NULL DEFAULT '',
    smime              TEXT NOT NULL DEFAULT '',
    smime_valid        INTEGER,
    smime_signer       TEXT NOT NULL DEFAULT '',
    smime_error        TEXT NOT NULL DEFAULT '',
    relay_status       TEXT NOT NULL DEFAULT 'none' CHECK (relay_status IN ('none', 'pending', 'sent', 'failed', 'rejected')),
    relay_requested_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    relay_requested_at TIMESTAMP,
    relay_decided_by   TEXT REFERENCES users(id) ON DELETE SET NULL,
    relay_decided_at   TIMESTAMP,
    relay_error        TEXT NOT NULL DEFAULT '',
    relay_remote_id    TEXT NOT NULL DEFAULT '',
    relay_note         TEXT NOT NULL DEFAULT '',
    UNIQUE (project_id, uid)
);
CREATE INDEX IF NOT EXISTS emails_project_received_idx ON emails(project_id, received_at DESC);
CREATE INDEX IF NOT EXISTS emails_relay_pending_idx ON emails(project_id, relay_requested_at DESC) WHERE relay_status <> 'none';

CREATE TABLE IF NOT EXISTS attachments (
    id           TEXT PRIMARY KEY,
    email_id     TEXT NOT NULL REFERENCES emails(id) ON DELETE CASCADE,
    filename     TEXT NOT NULL DEFAULT '',
    content_type TEXT NOT NULL DEFAULT 'application/octet-stream',
    size         INTEGER NOT NULL,
    content_id   TEXT NOT NULL DEFAULT '',
    inline       INTEGER NOT NULL DEFAULT 0,
    data         BLOB NOT NULL
);
CREATE INDEX IF NOT EXISTS attachments_email_idx ON attachments(email_id);

CREATE TABLE IF NOT EXISTS audit_log (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    at            TIMESTAMP NOT NULL,
    actor_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    actor         TEXT NOT NULL DEFAULT '',
    project_id    TEXT REFERENCES projects(id) ON DELETE SET NULL,
    action        TEXT NOT NULL,
    target        TEXT NOT NULL DEFAULT '',
    detail        TEXT NOT NULL DEFAULT '{}',
    ip            TEXT NOT NULL DEFAULT '',
    protocol      TEXT NOT NULL DEFAULT 'web'
);
CREATE INDEX IF NOT EXISTS audit_log_project_idx ON audit_log(project_id, at DESC);
CREATE INDEX IF NOT EXISTS audit_log_at_idx ON audit_log(at DESC);
CREATE INDEX IF NOT EXISTS audit_log_protocol_idx ON audit_log(protocol, at DESC);
`
