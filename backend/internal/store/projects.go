package store

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/phongbui83/maildev/internal/model"
)

const projectCols = `p.id, p.slug, p.name, p.description, p.status, p.auto_join, p.relay_enabled, p.retention_days, p.max_emails, p.uid_validity, p.created_at, p.updated_at`

func scanProject(row pgx.Row) (*model.Project, error) {
	var p model.Project
	err := row.Scan(&p.ID, &p.Slug, &p.Name, &p.Description, &p.Status, &p.AutoJoin, &p.RelayEnabled, &p.RetentionDays, &p.MaxEmails, &p.UIDValidity, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, wrapNotFound(err)
	}
	return &p, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
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
	row := s.pool.QueryRow(ctx, `
		INSERT INTO projects AS p (slug, name, description, auto_join, retention_days, max_emails, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+projectCols, strings.ToLower(p.Slug), p.Name, p.Description, p.AutoJoin, p.RetentionDays, p.MaxEmails, p.CreatedBy)
	proj, err := scanProject(row)
	if isUniqueViolation(err) {
		return nil, ErrConflict
	}
	return proj, err
}

func (s *Store) GetProject(ctx context.Context, id uuid.UUID) (*model.Project, error) {
	return scanProject(s.pool.QueryRow(ctx, `SELECT `+projectCols+` FROM projects p WHERE p.id = $1`, id))
}

func (s *Store) GetProjectBySlug(ctx context.Context, slug string) (*model.Project, error) {
	return scanProject(s.pool.QueryRow(ctx, `SELECT `+projectCols+` FROM projects p WHERE p.slug = $1`, strings.ToLower(slug)))
}

type UpdateProjectParams struct {
	Name, Description *string
	RetentionDays     *int
	MaxEmails         *int
	Status            *string
}

func (s *Store) UpdateProject(ctx context.Context, id uuid.UUID, u UpdateProjectParams) (*model.Project, error) {
	row := s.pool.QueryRow(ctx, `
		UPDATE projects AS p SET
			name = COALESCE($2, name),
			description = COALESCE($3, description),
			retention_days = COALESCE($4, retention_days),
			max_emails = COALESCE($5, max_emails),
			status = COALESCE($6, status),
			updated_at = now()
		WHERE id = $1
		RETURNING `+projectCols, id, u.Name, u.Description, u.RetentionDays, u.MaxEmails, u.Status)
	return scanProject(row)
}

func (s *Store) DeleteProject(ctx context.Context, id uuid.UUID) error {
	ct, err := s.pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListProjectsForUser returns projects visible to the user. Admins and auditors see all.
func (s *Store) ListProjectsForUser(ctx context.Context, u *model.User) ([]model.Project, error) {
	var rows pgx.Rows
	var err error
	counts := `(SELECT count(*) FROM emails e WHERE e.project_id = p.id) AS email_count,
	           (SELECT count(*) FROM emails e WHERE e.project_id = p.id AND NOT e.seen) AS unread_count`
	if u.IsAdmin() || u.IsAuditor() {
		rows, err = s.pool.Query(ctx, `
			SELECT `+projectCols+`, COALESCE(m.role, ''), `+counts+`
			FROM projects p LEFT JOIN project_members m ON m.project_id = p.id AND m.user_id = $1
			ORDER BY p.status, p.name`, u.ID)
	} else {
		rows, err = s.pool.Query(ctx, `
			SELECT `+projectCols+`, m.role, `+counts+`
			FROM projects p JOIN project_members m ON m.project_id = p.id AND m.user_id = $1
			ORDER BY p.status, p.name`, u.ID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Project
	for rows.Next() {
		var p model.Project
		if err := rows.Scan(&p.ID, &p.Slug, &p.Name, &p.Description, &p.Status, &p.AutoJoin, &p.RelayEnabled, &p.RetentionDays, &p.MaxEmails, &p.UIDValidity, &p.CreatedAt, &p.UpdatedAt, &p.MyRole, &p.EmailCount, &p.UnreadCount); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) ListAllProjects(ctx context.Context) ([]model.Project, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+projectCols+` FROM projects p ORDER BY p.name`)
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
func (s *Store) NextUID(ctx context.Context, tx pgx.Tx, projectID uuid.UUID) (int64, error) {
	var uid int64
	err := tx.QueryRow(ctx, `UPDATE projects SET next_uid = next_uid + 1 WHERE id = $1 RETURNING next_uid - 1`, projectID).Scan(&uid)
	return uid, wrapNotFound(err)
}

// Members

func (s *Store) ListMembers(ctx context.Context, projectID uuid.UUID) ([]model.Member, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT m.project_id, m.user_id, u.username, u.display_name, u.email, m.role, m.added_at, false
		FROM project_members m JOIN users u ON u.id = m.user_id
		WHERE m.project_id = $1
		UNION ALL
		SELECT i.project_id, '00000000-0000-0000-0000-000000000000'::uuid, i.username, '', '', i.role, i.invited_at, true
		FROM project_invites i WHERE i.project_id = $1
		ORDER BY 6, 3`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Member
	for rows.Next() {
		var m model.Member
		if err := rows.Scan(&m.ProjectID, &m.UserID, &m.Username, &m.DisplayName, &m.Email, &m.Role, &m.AddedAt, &m.Pending); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) MemberRole(ctx context.Context, projectID, userID uuid.UUID) (string, error) {
	var role string
	err := s.pool.QueryRow(ctx, `SELECT role FROM project_members WHERE project_id = $1 AND user_id = $2`, projectID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return role, err
}

func (s *Store) UpsertMember(ctx context.Context, projectID, userID uuid.UUID, role string, addedBy *uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO project_members (project_id, user_id, role, added_by) VALUES ($1, $2, $3, $4)
		ON CONFLICT (project_id, user_id) DO UPDATE SET role = EXCLUDED.role`, projectID, userID, role, addedBy)
	return err
}

func (s *Store) RemoveMember(ctx context.Context, projectID, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM project_members WHERE project_id = $1 AND user_id = $2`, projectID, userID)
	return err
}

func (s *Store) UpsertInvite(ctx context.Context, projectID uuid.UUID, username, role string, invitedBy *uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO project_invites (project_id, username, role, invited_by) VALUES ($1, $2, $3, $4)
		ON CONFLICT (project_id, username) DO UPDATE SET role = EXCLUDED.role`, projectID, strings.ToLower(username), role, invitedBy)
	return err
}

func (s *Store) RemoveInvite(ctx context.Context, projectID uuid.UUID, username string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM project_invites WHERE project_id = $1 AND username = $2`, projectID, strings.ToLower(username))
	return err
}

// MaterializeMembershipsOnLogin converts pending invites into memberships and
// applies auto-join projects (members for everyone, owners for admins).
func (s *Store) MaterializeMembershipsOnLogin(ctx context.Context, u *model.User) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO project_members (project_id, user_id, role, added_by)
		SELECT i.project_id, $1, i.role, i.invited_by FROM project_invites i WHERE i.username = $2
		ON CONFLICT (project_id, user_id) DO UPDATE SET role = EXCLUDED.role`, u.ID, u.Username); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM project_invites WHERE username = $1`, u.Username); err != nil {
		return err
	}

	if u.IsLocal() {
		// External accounts never join shared projects automatically.
		return tx.Commit(ctx)
	}
	autoRole := model.MemberMember
	if u.IsAdmin() {
		autoRole = model.MemberOwner
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO project_members (project_id, user_id, role)
		SELECT p.id, $1, $2 FROM projects p WHERE p.auto_join AND p.status = 'active'
		ON CONFLICT (project_id, user_id) DO UPDATE SET role = CASE WHEN EXCLUDED.role = 'owner' THEN 'owner' ELSE project_members.role END`,
		u.ID, autoRole); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
