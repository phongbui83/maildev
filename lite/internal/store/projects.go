package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/phongbui83/maildev/internal/model"
)

const projectCols = `p.id, p.slug, p.name, p.description, p.status, p.auto_join, p.relay_enabled, p.retention_days, p.max_emails, p.uid_validity, p.created_at, p.updated_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanProject(row rowScanner) (*model.Project, error) {
	var p model.Project
	err := row.Scan(&p.ID, &p.Slug, &p.Name, &p.Description, &p.Status, &p.AutoJoin, &p.RelayEnabled, &p.RetentionDays, &p.MaxEmails, &p.UIDValidity, ts(&p.CreatedAt), ts(&p.UpdatedAt))
	if err != nil {
		return nil, wrapNotFound(err)
	}
	return &p, nil
}

type CreateProjectParams struct {
	Slug, Name, Description string
	AutoJoin                bool
	RetentionDays           int
	MaxEmails               int
	CreatedBy               *uuid.UUID
}

func (s *Store) CreateProject(ctx context.Context, p CreateProjectParams) (*model.Project, error) {
	if p.RetentionDays <= 0 {
		p.RetentionDays = 14
	}
	if p.MaxEmails <= 0 {
		p.MaxEmails = 5000
	}
	id := newID()
	n := now()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO projects (id, slug, name, description, auto_join, retention_days, max_emails, uid_validity, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, strings.ToLower(p.Slug), p.Name, p.Description, p.AutoJoin, p.RetentionDays, p.MaxEmails, uint32(n.Unix()), p.CreatedBy, n, n)
	if isUniqueViolation(err) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	return s.GetProject(ctx, id)
}

func (s *Store) GetProject(ctx context.Context, id uuid.UUID) (*model.Project, error) {
	return scanProject(s.db.QueryRowContext(ctx, `SELECT `+projectCols+` FROM projects p WHERE p.id = ?`, id))
}

func (s *Store) GetProjectBySlug(ctx context.Context, slug string) (*model.Project, error) {
	return scanProject(s.db.QueryRowContext(ctx, `SELECT `+projectCols+` FROM projects p WHERE p.slug = ?`, strings.ToLower(slug)))
}

type UpdateProjectParams struct {
	Name, Description *string
	RetentionDays     *int
	MaxEmails         *int
	Status            *string
}

func (s *Store) UpdateProject(ctx context.Context, id uuid.UUID, u UpdateProjectParams) (*model.Project, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE projects SET
			name = COALESCE(?, name),
			description = COALESCE(?, description),
			retention_days = COALESCE(?, retention_days),
			max_emails = COALESCE(?, max_emails),
			status = COALESCE(?, status),
			updated_at = ?
		WHERE id = ?`, u.Name, u.Description, u.RetentionDays, u.MaxEmails, u.Status, now(), id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return s.GetProject(ctx, id)
}

func (s *Store) DeleteProject(ctx context.Context, id uuid.UUID) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListProjectsForUser returns projects visible to the user. Admins and auditors see all.
func (s *Store) ListProjectsForUser(ctx context.Context, u *model.User) ([]model.Project, error) {
	counts := `(SELECT count(*) FROM emails e WHERE e.project_id = p.id) AS email_count,
	           (SELECT count(*) FROM emails e WHERE e.project_id = p.id AND NOT e.seen) AS unread_count`
	var rows *sql.Rows
	var err error
	if u.IsAdmin() || u.IsAuditor() {
		rows, err = s.db.QueryContext(ctx, `
			SELECT `+projectCols+`, COALESCE(m.role, ''), `+counts+`
			FROM projects p LEFT JOIN project_members m ON m.project_id = p.id AND m.user_id = ?
			ORDER BY p.status, p.name`, u.ID)
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT `+projectCols+`, m.role, `+counts+`
			FROM projects p JOIN project_members m ON m.project_id = p.id AND m.user_id = ?
			ORDER BY p.status, p.name`, u.ID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Project
	for rows.Next() {
		var p model.Project
		if err := rows.Scan(&p.ID, &p.Slug, &p.Name, &p.Description, &p.Status, &p.AutoJoin, &p.RelayEnabled, &p.RetentionDays, &p.MaxEmails, &p.UIDValidity, ts(&p.CreatedAt), ts(&p.UpdatedAt), &p.MyRole, &p.EmailCount, &p.UnreadCount); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) ListAllProjects(ctx context.Context) ([]model.Project, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+projectCols+` FROM projects p ORDER BY p.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// NextUID atomically allocates the next IMAP UID for a project.
func (s *Store) NextUID(ctx context.Context, tx *sql.Tx, projectID uuid.UUID) (int64, error) {
	var uid int64
	err := tx.QueryRowContext(ctx, `UPDATE projects SET next_uid = next_uid + 1 WHERE id = ? RETURNING next_uid - 1`, projectID).Scan(&uid)
	return uid, wrapNotFound(err)
}

// Members

func (s *Store) ListMembers(ctx context.Context, projectID uuid.UUID) ([]model.Member, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.project_id, m.user_id, u.username, u.display_name, u.email, m.role, m.added_at, 0
		FROM project_members m JOIN users u ON u.id = m.user_id
		WHERE m.project_id = ?
		UNION ALL
		SELECT i.project_id, '00000000-0000-0000-0000-000000000000', i.username, '', '', i.role, i.invited_at, 1
		FROM project_invites i WHERE i.project_id = ?
		ORDER BY 6, 3`, projectID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Member
	for rows.Next() {
		var m model.Member
		if err := rows.Scan(&m.ProjectID, &m.UserID, &m.Username, &m.DisplayName, &m.Email, &m.Role, ts(&m.AddedAt), &m.Pending); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) MemberRole(ctx context.Context, projectID, userID uuid.UUID) (string, error) {
	var role string
	err := s.db.QueryRowContext(ctx, `SELECT role FROM project_members WHERE project_id = ? AND user_id = ?`, projectID, userID).Scan(&role)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return role, err
}

func (s *Store) UpsertMember(ctx context.Context, projectID, userID uuid.UUID, role string, addedBy *uuid.UUID) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO project_members (project_id, user_id, role, added_by, added_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (project_id, user_id) DO UPDATE SET role = excluded.role`, projectID, userID, role, addedBy, now())
	return err
}

func (s *Store) RemoveMember(ctx context.Context, projectID, userID uuid.UUID) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM project_members WHERE project_id = ? AND user_id = ?`, projectID, userID)
	return err
}

func (s *Store) UpsertInvite(ctx context.Context, projectID uuid.UUID, username, role string, invitedBy *uuid.UUID) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO project_invites (project_id, username, role, invited_by, invited_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (project_id, username) DO UPDATE SET role = excluded.role`, projectID, strings.ToLower(username), role, invitedBy, now())
	return err
}

func (s *Store) RemoveInvite(ctx context.Context, projectID uuid.UUID, username string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM project_invites WHERE project_id = ? AND username = ?`, projectID, strings.ToLower(username))
	return err
}

// MaterializeMembershipsOnLogin converts pending invites into memberships and
// applies auto-join projects: everyone becomes a member, admins owners. In the
// lite edition local accounts join too (every account is local and the single
// project is meant for all of them); otherwise they never auto-join.
func (s *Store) MaterializeMembershipsOnLogin(ctx context.Context, u *model.User) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	n := now()
	rows, err := tx.QueryContext(ctx, `SELECT project_id, role, invited_by FROM project_invites WHERE username = ?`, u.Username)
	if err != nil {
		return err
	}
	type inv struct {
		pid       uuid.UUID
		role      string
		invitedBy *uuid.UUID
	}
	var invites []inv
	for rows.Next() {
		var i inv
		if err := rows.Scan(&i.pid, &i.role, &i.invitedBy); err != nil {
			rows.Close()
			return err
		}
		invites = append(invites, i)
	}
	rows.Close()
	for _, i := range invites {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO project_members (project_id, user_id, role, added_by, added_at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (project_id, user_id) DO UPDATE SET role = excluded.role`, i.pid, u.ID, i.role, i.invitedBy, n); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM project_invites WHERE username = ?`, u.Username); err != nil {
		return err
	}
	if u.IsLocal() && !s.lite {
		return tx.Commit()
	}
	autoRole := model.MemberMember
	if u.IsAdmin() {
		autoRole = model.MemberOwner
	}
	prow, err := tx.QueryContext(ctx, `SELECT id FROM projects WHERE auto_join AND status = 'active'`)
	if err != nil {
		return err
	}
	var auto []uuid.UUID
	for prow.Next() {
		var id uuid.UUID
		if err := prow.Scan(&id); err != nil {
			prow.Close()
			return err
		}
		auto = append(auto, id)
	}
	prow.Close()
	for _, pid := range auto {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO project_members (project_id, user_id, role, added_at) VALUES (?, ?, ?, ?)
			ON CONFLICT (project_id, user_id) DO UPDATE SET role = CASE WHEN excluded.role = 'owner' THEN 'owner' ELSE project_members.role END`,
			pid, u.ID, autoRole, n); err != nil {
			return err
		}
	}
	return tx.Commit()
}

var _ = time.Second
