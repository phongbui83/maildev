package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/phongbui83/maildev/internal/model"
)

const userCols = `id, ldap_uuid, username, display_name, email, role, disabled, created_at, last_login_at, auth_source, must_change_password, locked_until, department`

func scanUser(row pgx.Row) (*model.User, error) {
	var u model.User
	err := row.Scan(&u.ID, &u.LDAPUUID, &u.Username, &u.DisplayName, &u.Email, &u.Role, &u.Disabled, &u.CreatedAt, &u.LastLoginAt, &u.AuthSource, &u.MustChangePassword, &u.LockedUntil, &u.Department)
	if err != nil {
		return nil, wrapNotFound(err)
	}
	return &u, nil
}

// --- Local (non-directory) accounts ---

// CreateLocalUser creates an external account. Local users always have the "user" role.
func (s *Store) CreateLocalUser(ctx context.Context, username, displayName, email, passwordHash string, mustChange bool, createdBy *uuid.UUID) (*model.User, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO users (username, display_name, email, role, auth_source, password_hash, must_change_password, created_by)
		VALUES ($1, $2, $3, 'user', 'local', $4, $5, $6)
		RETURNING `+userCols, strings.ToLower(strings.TrimSpace(username)), displayName, email, passwordHash, mustChange, createdBy)
	u, err := scanUser(row)
	if isUniqueViolation(err) {
		return nil, ErrConflict
	}
	return u, err
}

// LocalCredentials returns the password hash of a local user (empty for directory users).
func (s *Store) LocalCredentials(ctx context.Context, username string) (*model.User, string, error) {
	var u model.User
	var hash string
	err := s.pool.QueryRow(ctx, `SELECT `+userCols+`, password_hash FROM users WHERE username = $1 AND auth_source = 'local'`, strings.ToLower(username)).
		Scan(&u.ID, &u.LDAPUUID, &u.Username, &u.DisplayName, &u.Email, &u.Role, &u.Disabled, &u.CreatedAt, &u.LastLoginAt, &u.AuthSource, &u.MustChangePassword, &u.LockedUntil, &u.Department, &hash)
	if err != nil {
		return nil, "", wrapNotFound(err)
	}
	return &u, hash, nil
}

// RecordLoginFailure increments the counter and locks the account after maxFails.
func (s *Store) RecordLoginFailure(ctx context.Context, userID uuid.UUID, maxFails int, lock time.Duration) (locked bool, err error) {
	err = s.pool.QueryRow(ctx, `
		UPDATE users SET
			locked_until = CASE WHEN failed_logins + 1 >= $2 THEN now() + $3::interval ELSE locked_until END,
			failed_logins = CASE WHEN failed_logins + 1 >= $2 THEN 0 ELSE failed_logins + 1 END
		WHERE id = $1 RETURNING locked_until IS NOT NULL AND locked_until > now()`, userID, maxFails, lock).Scan(&locked)
	return locked, err
}

func (s *Store) RecordLoginSuccess(ctx context.Context, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET failed_logins = 0, locked_until = NULL, last_login_at = now() WHERE id = $1`, userID)
	return err
}

func (s *Store) SetPassword(ctx context.Context, userID uuid.UUID, hash string, mustChange bool) error {
	ct, err := s.pool.Exec(ctx, `UPDATE users SET password_hash = $2, must_change_password = $3, failed_logins = 0, locked_until = NULL WHERE id = $1 AND auth_source = 'local'`, userID, hash, mustChange)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	// Changing a password ends every other session.
	_, err = s.pool.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
	return err
}

func (s *Store) Unlock(ctx context.Context, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET failed_logins = 0, locked_until = NULL WHERE id = $1`, userID)
	return err
}

// UpsertUserFromDirectory creates or refreshes a user keyed by its directory UUID and
// records the login time. Role is never downgraded here.
func (s *Store) UpsertUserFromDirectory(ctx context.Context, ldapUUID uuid.UUID, username, displayName, email string) (*model.User, error) {
	username = strings.ToLower(username)
	// Pre-imported directory row (from the company user list) without a GUID yet.
	claimed := s.pool.QueryRow(ctx, `
		UPDATE users SET ldap_uuid = $1, display_name = COALESCE(NULLIF($3, ''), display_name), email = COALESCE(NULLIF($4, ''), email), last_login_at = now()
		WHERE username = $2 AND auth_source = 'ldap' AND ldap_uuid IS NULL
		RETURNING `+userCols, ldapUUID, username, displayName, email)
	if u, err := scanUser(claimed); err == nil {
		return u, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	// Username reused by a different AD object (employee left, new hire got the
	// same sAMAccountName): the row is reset to a fresh identity so nothing is
	// inherited; auto-join then only grants the demo project.
	if reset, err := s.resetReusedUsername(ctx, ldapUUID, username, displayName, email); err != nil {
		return nil, err
	} else if reset != nil {
		return reset, nil
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO users (ldap_uuid, username, display_name, email, last_login_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (ldap_uuid) DO UPDATE SET
			username = EXCLUDED.username,
			display_name = EXCLUDED.display_name,
			email = EXCLUDED.email,
			last_login_at = now()
		RETURNING `+userCols, ldapUUID, username, displayName, email)
	u, err := scanUser(row)
	if isUniqueViolation(err) {
		return nil, ErrConflict // a local account already uses this username
	}
	return u, err
}

func (s *Store) GetUser(ctx context.Context, id uuid.UUID) (*model.User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (*model.User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE username = $1`, strings.ToLower(username)))
}

func (s *Store) ListUsers(ctx context.Context) ([]model.User, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+userCols+` FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

var ErrLocalRole = errors.New("local accounts can only have the user role")

func (s *Store) SetUserRole(ctx context.Context, id uuid.UUID, role string) error {
	ct, err := s.pool.Exec(ctx, `UPDATE users SET role = $2 WHERE id = $1`, id, role)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.ConstraintName == "users_local_role_chk" {
			return ErrLocalRole
		}
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetUserDisabled(ctx context.Context, id uuid.UUID, disabled bool) error {
	ct, err := s.pool.Exec(ctx, `UPDATE users SET disabled = $2 WHERE id = $1`, id, disabled)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	if disabled {
		_, err = s.pool.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, id)
	}
	return err
}

// Sessions

func (s *Store) CreateSession(ctx context.Context, id string, userID uuid.UUID, expires time.Time, ip, ua string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO sessions (id, user_id, expires_at, ip, user_agent) VALUES ($1, $2, $3, $4, $5)`, id, userID, expires, ip, ua)
	return err
}

// GetSessionUser resolves a session id to its user and extends the expiry (sliding window).
func (s *Store) GetSessionUser(ctx context.Context, id string, ttl time.Duration) (*model.User, error) {
	row := s.pool.QueryRow(ctx, `
		UPDATE sessions SET expires_at = GREATEST(expires_at, now() + $2::interval)
		WHERE id = $1 AND expires_at > now()
		RETURNING user_id`, id, ttl)
	var userID uuid.UUID
	if err := row.Scan(&userID); err != nil {
		return nil, wrapNotFound(err)
	}
	u, err := s.GetUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if u.Disabled {
		return nil, ErrNotFound
	}
	return u, nil
}

func (s *Store) DeleteSession(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, id)
	return err
}

func (s *Store) PurgeExpiredSessions(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`)
	return err
}

func (s *Store) DeleteUser(ctx context.Context, id uuid.UUID) error {
	ct, err := s.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DirectoryUser is one row of the company user list to import.
type DirectoryUser struct {
	Username, FullName, Email, Department string
}

// SyncDirectoryUsers inserts directory users that do not exist yet and refreshes
// name/email/department of existing directory users. Local accounts and roles
// are never touched. Returns the number of inserted rows.
func (s *Store) SyncDirectoryUsers(ctx context.Context, users []DirectoryUser) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	inserted := 0
	for _, u := range users {
		ct, err := tx.Exec(ctx, `
			INSERT INTO users (username, display_name, email, department, auth_source)
			VALUES ($1, $2, $3, $4, 'ldap')
			ON CONFLICT (username) DO UPDATE SET
				display_name = CASE WHEN users.auth_source = 'ldap' AND EXCLUDED.display_name <> '' THEN EXCLUDED.display_name ELSE users.display_name END,
				email        = CASE WHEN users.auth_source = 'ldap' AND EXCLUDED.email <> '' THEN EXCLUDED.email ELSE users.email END,
				department   = CASE WHEN users.auth_source = 'ldap' THEN EXCLUDED.department ELSE users.department END
			WHERE users.auth_source = 'ldap'`, strings.ToLower(u.Username), u.FullName, u.Email, u.Department)
		if err != nil {
			return inserted, err
		}
		if ct.String() == "INSERT 0 1" {
			inserted++
		}
	}
	return inserted, tx.Commit(ctx)
}

func (s *Store) resetReusedUsername(ctx context.Context, newGUID uuid.UUID, username, displayName, email string) (*model.User, error) {
	var id, oldGUID uuid.UUID
	var oldRole string
	err := s.pool.QueryRow(ctx, `SELECT id, ldap_uuid, role FROM users WHERE username = $1 AND auth_source = 'ldap' AND ldap_uuid IS NOT NULL AND ldap_uuid <> $2`, username, newGUID).Scan(&id, &oldGUID, &oldRole)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var removed int64
	ct, err := tx.Exec(ctx, `DELETE FROM project_members WHERE user_id = $1`, id)
	if err != nil {
		return nil, err
	}
	removed = ct.RowsAffected()
	if _, err := tx.Exec(ctx, `DELETE FROM project_invites WHERE username = $1`, username); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, id); err != nil {
		return nil, err
	}
	row := tx.QueryRow(ctx, `
		UPDATE users SET ldap_uuid = $2, role = 'user', disabled = false, display_name = $3, email = $4, last_login_at = now()
		WHERE id = $1 RETURNING `+userCols, id, newGUID, displayName, email)
	u, err := scanUser(row)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	s.Audit(ctx, AuditParams{Actor: "system", Protocol: "system", Action: "user_identity_reset", Target: username,
		Detail: map[string]any{"old_object_guid": oldGUID, "new_object_guid": newGUID, "old_role": oldRole, "memberships_removed": removed}})
	return u, nil
}

// DisableMissingDirectoryUsers disables directory accounts that are no longer in
// the company list (people who left). Returns the usernames disabled.
func (s *Store) DisableMissingDirectoryUsers(ctx context.Context, present []string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		UPDATE users SET disabled = true WHERE auth_source = 'ldap' AND NOT disabled AND username <> ALL($1) RETURNING username`, present)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	if len(out) > 0 {
		_, _ = s.pool.Exec(ctx, `DELETE FROM sessions WHERE user_id IN (SELECT id FROM users WHERE username = ANY($1))`, out)
	}
	return out, rows.Err()
}
