package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/phongbui83/maildev/internal/model"
)

const userCols = `id, ldap_uuid, username, display_name, email, role, disabled, created_at, last_login_at, auth_source, must_change_password, locked_until, department`

func scanUserInto(row rowScanner, u *model.User, extra ...any) error {
	dest := []any{&u.ID, &u.LDAPUUID, &u.Username, &u.DisplayName, &u.Email, &u.Role, &u.Disabled, ts(&u.CreatedAt), nt(&u.LastLoginAt), &u.AuthSource, &u.MustChangePassword, nt(&u.LockedUntil), &u.Department}
	dest = append(dest, extra...)
	return row.Scan(dest...)
}

func scanUser(row rowScanner) (*model.User, error) {
	var u model.User
	if err := scanUserInto(row, &u); err != nil {
		return nil, wrapNotFound(err)
	}
	return &u, nil
}

// --- Local accounts ---

// CreateLocalUser creates a password account with the "user" role.
func (s *Store) CreateLocalUser(ctx context.Context, username, displayName, email, passwordHash string, mustChange bool, createdBy *uuid.UUID) (*model.User, error) {
	return s.createLocal(ctx, username, displayName, email, model.RoleUser, passwordHash, mustChange, createdBy)
}

// CreateLocalUserWithRole is used by the lite bootstrap to create the first admin.
func (s *Store) CreateLocalUserWithRole(ctx context.Context, username, displayName, email, role, passwordHash string, mustChange bool) (*model.User, error) {
	return s.createLocal(ctx, username, displayName, email, role, passwordHash, mustChange, nil)
}

func (s *Store) createLocal(ctx context.Context, username, displayName, email, role, passwordHash string, mustChange bool, createdBy *uuid.UUID) (*model.User, error) {
	id := newID()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO users (id, username, display_name, email, role, auth_source, password_hash, must_change_password, created_by, created_at)
		VALUES (?, ?, ?, ?, ?, 'local', ?, ?, ?, ?)`,
		id, strings.ToLower(strings.TrimSpace(username)), displayName, email, role, passwordHash, mustChange, createdBy, now())
	if isUniqueViolation(err) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	return s.GetUser(ctx, id)
}

// LocalCredentials returns the password hash of a local user.
func (s *Store) LocalCredentials(ctx context.Context, username string) (*model.User, string, error) {
	var u model.User
	var hash string
	row := s.db.QueryRowContext(ctx, `SELECT `+userCols+`, password_hash FROM users WHERE username = ? AND auth_source = 'local'`, strings.ToLower(username))
	if err := scanUserInto(row, &u, &hash); err != nil {
		return nil, "", wrapNotFound(err)
	}
	return &u, hash, nil
}

// RecordLoginFailure increments the counter and locks the account after maxFails.
func (s *Store) RecordLoginFailure(ctx context.Context, userID uuid.UUID, maxFails int, lock time.Duration) (locked bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var fails int
	if err := tx.QueryRowContext(ctx, `SELECT failed_logins FROM users WHERE id = ?`, userID).Scan(&fails); err != nil {
		return false, wrapNotFound(err)
	}
	fails++
	if fails >= maxFails {
		until := now().Add(lock)
		if _, err := tx.ExecContext(ctx, `UPDATE users SET failed_logins = 0, locked_until = ? WHERE id = ?`, until, userID); err != nil {
			return false, err
		}
		return true, tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET failed_logins = ? WHERE id = ?`, fails, userID); err != nil {
		return false, err
	}
	return false, tx.Commit()
}

func (s *Store) RecordLoginSuccess(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET failed_logins = 0, locked_until = NULL, last_login_at = ? WHERE id = ?`, now(), userID)
	return err
}

func (s *Store) SetPassword(ctx context.Context, userID uuid.UUID, hash string, mustChange bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash = ?, must_change_password = ?, failed_logins = 0, locked_until = NULL WHERE id = ? AND auth_source = 'local'`, hash, mustChange, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

func (s *Store) Unlock(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET failed_logins = 0, locked_until = NULL WHERE id = ?`, userID)
	return err
}

// UpsertUserFromDirectory creates or refreshes a directory user keyed by its
// UUID (kept for the dev authenticator and a possible LDAP setup).
func (s *Store) UpsertUserFromDirectory(ctx context.Context, ldapUUID uuid.UUID, username, displayName, email string) (*model.User, error) {
	username = strings.ToLower(username)
	n := now()
	res, err := s.db.ExecContext(ctx, `
		UPDATE users SET ldap_uuid = ?, display_name = COALESCE(NULLIF(?, ''), display_name), email = COALESCE(NULLIF(?, ''), email), last_login_at = ?
		WHERE username = ? AND auth_source = 'ldap' AND ldap_uuid IS NULL`, ldapUUID, displayName, email, n, username)
	if err != nil {
		return nil, err
	}
	if k, _ := res.RowsAffected(); k == 1 {
		return s.GetUserByUsername(ctx, username)
	}
	if reset, err := s.resetReusedUsername(ctx, ldapUUID, username, displayName, email); err != nil {
		return nil, err
	} else if reset != nil {
		return reset, nil
	}
	res, err = s.db.ExecContext(ctx, `UPDATE users SET username = ?, display_name = ?, email = ?, last_login_at = ? WHERE ldap_uuid = ?`, username, displayName, email, n, ldapUUID)
	if isUniqueViolation(err) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	if k, _ := res.RowsAffected(); k == 1 {
		return s.GetUserByUsername(ctx, username)
	}
	id := newID()
	_, err = s.db.ExecContext(ctx, `INSERT INTO users (id, ldap_uuid, username, display_name, email, auth_source, created_at, last_login_at) VALUES (?, ?, ?, ?, ?, 'ldap', ?, ?)`,
		id, ldapUUID, username, displayName, email, n, n)
	if isUniqueViolation(err) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	return s.GetUser(ctx, id)
}

func (s *Store) GetUser(ctx context.Context, id uuid.UUID) (*model.User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (*model.User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE username = ?`, strings.ToLower(username)))
}

func (s *Store) ListUsers(ctx context.Context) ([]model.User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY username`)
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

// CountAdmins tells the bootstrap whether a first admin must be created.
func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE role = 'admin' AND NOT disabled`).Scan(&n)
	return n, err
}

var ErrLocalRole = errors.New("local accounts can only have the user role")

func (s *Store) SetUserRole(ctx context.Context, id uuid.UUID, role string) error {
	if !s.lite && role != model.RoleUser {
		var src string
		if err := s.db.QueryRowContext(ctx, `SELECT auth_source FROM users WHERE id = ?`, id).Scan(&src); err != nil {
			return wrapNotFound(err)
		}
		if src == model.AuthLocal {
			return ErrLocalRole
		}
	}
	res, err := s.db.ExecContext(ctx, `UPDATE users SET role = ? WHERE id = ?`, role, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetUserDisabled(ctx context.Context, id uuid.UUID, disabled bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET disabled = ? WHERE id = ?`, disabled, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if disabled {
		_, err = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id)
	}
	return err
}

// Sessions

func (s *Store) CreateSession(ctx context.Context, id string, userID uuid.UUID, expires time.Time, ip, ua string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions (id, user_id, created_at, expires_at, ip, user_agent) VALUES (?, ?, ?, ?, ?, ?)`, id, userID, now(), expires.UTC(), ip, ua)
	return err
}

// GetSessionUser resolves a session id to its user and extends the expiry (sliding window).
func (s *Store) GetSessionUser(ctx context.Context, id string, ttl time.Duration) (*model.User, error) {
	n := now()
	var userID uuid.UUID
	err := s.db.QueryRowContext(ctx, `
		UPDATE sessions SET expires_at = max(expires_at, ?) WHERE id = ? AND expires_at > ? RETURNING user_id`, n.Add(ttl), id, n).Scan(&userID)
	if err != nil {
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
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	return err
}

func (s *Store) PurgeExpiredSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, now())
	return err
}

func (s *Store) DeleteUser(ctx context.Context, id uuid.UUID) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DirectoryUser is one row of a company user list to import (unused in the
// lite edition unless a directory URL is configured).
type DirectoryUser struct {
	Username, FullName, Email, Department string
}

func (s *Store) SyncDirectoryUsers(ctx context.Context, users []DirectoryUser) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	inserted := 0
	for _, u := range users {
		res, err := tx.ExecContext(ctx, `
			UPDATE users SET
				display_name = CASE WHEN ? <> '' THEN ? ELSE display_name END,
				email = CASE WHEN ? <> '' THEN ? ELSE email END,
				department = ?
			WHERE username = ? AND auth_source = 'ldap'`, u.FullName, u.FullName, u.Email, u.Email, u.Department, strings.ToLower(u.Username))
		if err != nil {
			return inserted, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			continue
		}
		res, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO users (id, username, display_name, email, department, auth_source, created_at) VALUES (?, ?, ?, ?, ?, 'ldap', ?)`,
			newID(), strings.ToLower(u.Username), u.FullName, u.Email, u.Department, now())
		if err != nil {
			return inserted, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			inserted++
		}
	}
	return inserted, tx.Commit()
}

func (s *Store) resetReusedUsername(ctx context.Context, newGUID uuid.UUID, username, displayName, email string) (*model.User, error) {
	var id, oldGUID uuid.UUID
	var oldRole string
	err := s.db.QueryRowContext(ctx, `SELECT id, ldap_uuid, role FROM users WHERE username = ? AND auth_source = 'ldap' AND ldap_uuid IS NOT NULL AND ldap_uuid <> ?`, username, newGUID).Scan(&id, &oldGUID, &oldRole)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM project_members WHERE user_id = ?`, id)
	if err != nil {
		return nil, err
	}
	removed, _ := res.RowsAffected()
	if _, err := tx.ExecContext(ctx, `DELETE FROM project_invites WHERE username = ?`, username); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET ldap_uuid = ?, role = 'user', disabled = 0, display_name = ?, email = ?, last_login_at = ? WHERE id = ?`, newGUID, displayName, email, now(), id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.Audit(ctx, AuditParams{Actor: "system", Protocol: "system", Action: "user_identity_reset", Target: username,
		Detail: map[string]any{"old_object_guid": oldGUID, "new_object_guid": newGUID, "old_role": oldRole, "memberships_removed": removed}})
	return s.GetUser(ctx, id)
}

func (s *Store) DisableMissingDirectoryUsers(ctx context.Context, present []string) ([]string, error) {
	keep := make(map[string]bool, len(present))
	for _, p := range present {
		keep[strings.ToLower(p)] = true
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, username FROM users WHERE auth_source = 'ldap' AND NOT disabled`)
	if err != nil {
		return nil, err
	}
	type row struct {
		id   uuid.UUID
		name string
	}
	var gone []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.name); err != nil {
			rows.Close()
			return nil, err
		}
		if !keep[r.name] {
			gone = append(gone, r)
		}
	}
	rows.Close()
	var out []string
	for _, g := range gone {
		if _, err := s.db.ExecContext(ctx, `UPDATE users SET disabled = 1 WHERE id = ?`, g.id); err != nil {
			return out, err
		}
		_, _ = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, g.id)
		out = append(out, g.name)
	}
	return out, nil
}
