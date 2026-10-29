package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/phongbui83/maildev/internal/model"
)

const emailListCols = `e.id, e.project_id, e.uid, e.credential_id, e.source, e.client_ip, e.received_at, e.message_id, e.subject,
	e.from_addrs, e.to_addrs, e.cc_addrs, e.bcc_addrs, e.envelope_from, e.envelope_to, e.sent_at, e.size,
	e.attachment_count, e.seen, e.flagged, e.deleted, substr(e.text_body, 1, 160), e.smime, e.smime_valid, e.smime_signer, e.smime_error,
	e.relay_status, COALESCE(ru.username, ''), e.relay_requested_at, COALESCE(du.username, ''), e.relay_decided_at, e.relay_error, e.relay_remote_id, e.relay_note`

// emailJoins must accompany emailListCols.
const emailJoins = ` LEFT JOIN users ru ON ru.id = e.relay_requested_by LEFT JOIN users du ON du.id = e.relay_decided_by`

func emailListDest(e *model.Email, from, to, cc, bcc, envTo *[]byte) []any {
	return []any{&e.ID, &e.ProjectID, &e.UID, &e.CredentialID, &e.Source, &e.ClientIP, ts(&e.ReceivedAt), &e.MessageID, &e.Subject,
		from, to, cc, bcc, &e.EnvelopeFrom, envTo, nt(&e.SentAt), &e.Size,
		&e.AttachmentCount, &e.Seen, &e.Flagged, &e.Deleted, &e.Snippet, &e.SMIME, &e.SMIMEValid, &e.SMIMESigner, &e.SMIMEError,
		&e.RelayStatus, &e.RelayRequestedBy, nt(&e.RelayRequestedAt), &e.RelayDecidedBy, nt(&e.RelayDecidedAt), &e.RelayError, &e.RelayRemoteID, &e.RelayNote}
}

func decodeAddrs(e *model.Email, from, to, cc, bcc, envTo []byte) {
	_ = json.Unmarshal(from, &e.From)
	_ = json.Unmarshal(to, &e.To)
	_ = json.Unmarshal(cc, &e.Cc)
	_ = json.Unmarshal(bcc, &e.Bcc)
	_ = json.Unmarshal(envTo, &e.EnvelopeTo)
	if e.EnvelopeTo == nil {
		e.EnvelopeTo = []string{}
	}
}

func scanEmailList(row rowScanner) (*model.Email, error) {
	var e model.Email
	var from, to, cc, bcc, envTo []byte
	if err := row.Scan(emailListDest(&e, &from, &to, &cc, &bcc, &envTo)...); err != nil {
		return nil, wrapNotFound(err)
	}
	decodeAddrs(&e, from, to, cc, bcc, envTo)
	e.Snippet = strings.Join(strings.Fields(e.Snippet), " ")
	return &e, nil
}

type InsertEmailParams struct {
	Email        *model.Email
	Attachments  []model.Attachment
	RelayPending bool // queue for owner approval (project has relay enabled)
}

// InsertEmail stores a parsed message and its attachments, allocating the IMAP UID.
func (s *Store) InsertEmail(ctx context.Context, p InsertEmailParams, raw []byte) (*model.Email, error) {
	e := p.Email
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	uid, err := s.NextUID(ctx, tx, e.ProjectID)
	if err != nil {
		return nil, err
	}
	e.UID = uid
	from, _ := json.Marshal(nz(e.From))
	to, _ := json.Marshal(nz(e.To))
	cc, _ := json.Marshal(nz(e.Cc))
	bcc, _ := json.Marshal(nz(e.Bcc))
	headers, _ := json.Marshal(e.Headers)
	if e.Headers == nil {
		headers = []byte("[]")
	}
	if e.EnvelopeTo == nil {
		e.EnvelopeTo = []string{}
	}
	envTo, _ := json.Marshal(e.EnvelopeTo)
	relayStatus := model.RelayNone
	var relayRequestedAt *time.Time
	if p.RelayPending {
		relayStatus = model.RelayPending
		n := now()
		relayRequestedAt = &n
	}
	e.RelayStatus = relayStatus
	e.RelayRequestedAt = relayRequestedAt
	e.ID = newID()
	e.ReceivedAt = now()
	e.Size = len(raw)
	e.AttachmentCount = len(p.Attachments)

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO emails (id, project_id, uid, credential_id, source, client_ip, received_at, message_id, subject,
			from_addrs, to_addrs, cc_addrs, bcc_addrs, envelope_from, envelope_to, sent_at, size,
			text_body, html_body, headers, attachment_count, raw, addr_text, relay_status, relay_requested_at, smime, smime_valid, smime_signer, smime_error)
		VALUES (?,?,?,?,?,?,?,?,?, ?,?,?,?,?,?,?,?, ?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.ID, e.ProjectID, uid, e.CredentialID, e.Source, e.ClientIP, e.ReceivedAt, e.MessageID, e.Subject,
		string(from), string(to), string(cc), string(bcc), e.EnvelopeFrom, string(envTo), utc(e.SentAt), len(raw),
		e.TextBody, e.HTMLBody, string(headers), len(p.Attachments), raw, addrText(e), relayStatus, relayRequestedAt, e.SMIME, e.SMIMEValid, e.SMIMESigner, e.SMIMEError,
	); err != nil {
		return nil, err
	}
	for i := range p.Attachments {
		a := &p.Attachments[i]
		a.EmailID = e.ID
		a.ID = newID()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO attachments (id, email_id, filename, content_type, size, content_id, inline, data)
			VALUES (?,?,?,?,?,?,?,?)`, a.ID, e.ID, a.Filename, a.ContentType, len(a.Data), a.ContentID, a.Inline, a.Data); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return e, nil
}

func nz(a []model.Address) []model.Address {
	if a == nil {
		return []model.Address{}
	}
	return a
}

func addrText(e *model.Email) string {
	var b strings.Builder
	for _, list := range [][]model.Address{e.From, e.To, e.Cc, e.Bcc} {
		for _, a := range list {
			b.WriteString(a.Name)
			b.WriteByte(' ')
			b.WriteString(a.Address)
			b.WriteByte(' ')
			b.WriteString(strings.NewReplacer("@", " ", ".", " ").Replace(a.Address))
			b.WriteByte(' ')
		}
	}
	b.WriteString(e.EnvelopeFrom)
	b.WriteByte(' ')
	b.WriteString(strings.Join(e.EnvelopeTo, " "))
	return b.String()
}

type ListEmailsParams struct {
	ProjectID uuid.UUID
	Query     string
	Unread    bool
	Limit     int
	Offset    int
}

func (s *Store) ListEmails(ctx context.Context, p ListEmailsParams) ([]model.Email, int, error) {
	if p.Limit <= 0 || p.Limit > 200 {
		p.Limit = 50
	}
	where := `e.project_id = ?`
	args := []any{p.ProjectID}
	if p.Unread {
		where += ` AND NOT e.seen`
	}
	if q := strings.ToLower(strings.TrimSpace(p.Query)); q != "" {
		// Every word must appear in subject, addresses or body.
		for _, w := range strings.Fields(q) {
			args = append(args, w, w, w)
			where += ` AND (instr(lower(e.subject), ?) > 0 OR instr(lower(e.addr_text), ?) > 0 OR instr(lower(e.text_body), ?) > 0)`
		}
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM emails e WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, p.Limit, p.Offset)
	rows, err := s.db.QueryContext(ctx, `SELECT `+emailListCols+` FROM emails e`+emailJoins+` WHERE `+where+
		` ORDER BY e.received_at DESC, e.uid DESC LIMIT ? OFFSET ?`, args...)
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

// GetEmail returns the full email (bodies, headers, attachment metadata) without raw bytes.
func (s *Store) GetEmail(ctx context.Context, projectID, id uuid.UUID) (*model.Email, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+emailListCols+`, e.text_body, e.html_body, e.headers FROM emails e`+emailJoins+` WHERE e.project_id = ? AND e.id = ?`, projectID, id)
	var e model.Email
	var from, to, cc, bcc, envTo, headers []byte
	dest := append(emailListDest(&e, &from, &to, &cc, &bcc, &envTo), &e.TextBody, &e.HTMLBody, &headers)
	if err := row.Scan(dest...); err != nil {
		return nil, wrapNotFound(err)
	}
	decodeAddrs(&e, from, to, cc, bcc, envTo)
	_ = json.Unmarshal(headers, &e.Headers)
	e.Snippet = ""

	rows, err := s.db.QueryContext(ctx, `SELECT id, email_id, filename, content_type, size, content_id, inline FROM attachments WHERE email_id = ? ORDER BY filename`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	e.Attachments = []model.Attachment{}
	for rows.Next() {
		var a model.Attachment
		if err := rows.Scan(&a.ID, &a.EmailID, &a.Filename, &a.ContentType, &a.Size, &a.ContentID, &a.Inline); err != nil {
			return nil, err
		}
		e.Attachments = append(e.Attachments, a)
	}
	return &e, rows.Err()
}

func (s *Store) GetRawEmail(ctx context.Context, projectID, id uuid.UUID) ([]byte, error) {
	var raw []byte
	if err := s.db.QueryRowContext(ctx, `SELECT raw FROM emails WHERE project_id = ? AND id = ?`, projectID, id).Scan(&raw); err != nil {
		return nil, wrapNotFound(err)
	}
	return raw, nil
}

func (s *Store) GetAttachment(ctx context.Context, projectID, emailID, attID uuid.UUID) (*model.Attachment, error) {
	return s.attachment(ctx, projectID, `a.id = ?`, emailID, attID)
}

func (s *Store) GetAttachmentByContentID(ctx context.Context, projectID, emailID uuid.UUID, cid string) (*model.Attachment, error) {
	return s.attachment(ctx, projectID, `a.content_id = ?`, emailID, cid)
}

func (s *Store) attachment(ctx context.Context, projectID uuid.UUID, cond string, emailID uuid.UUID, arg any) (*model.Attachment, error) {
	var a model.Attachment
	err := s.db.QueryRowContext(ctx, `
		SELECT a.id, a.email_id, a.filename, a.content_type, a.size, a.content_id, a.inline, a.data
		FROM attachments a JOIN emails e ON e.id = a.email_id
		WHERE e.project_id = ? AND a.email_id = ? AND `+cond+` LIMIT 1`, projectID, emailID, arg).
		Scan(&a.ID, &a.EmailID, &a.Filename, &a.ContentType, &a.Size, &a.ContentID, &a.Inline, &a.Data)
	if err != nil {
		return nil, wrapNotFound(err)
	}
	return &a, nil
}

func (s *Store) SetEmailSeen(ctx context.Context, projectID, id uuid.UUID, seen bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE emails SET seen = ? WHERE project_id = ? AND id = ?`, seen, projectID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) MarkAllSeen(ctx context.Context, projectID uuid.UUID) error {
	_, err := s.db.ExecContext(ctx, `UPDATE emails SET seen = 1 WHERE project_id = ? AND NOT seen`, projectID)
	return err
}

func (s *Store) DeleteEmail(ctx context.Context, projectID, id uuid.UUID) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM emails WHERE project_id = ? AND id = ?`, projectID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteAllEmails(ctx context.Context, projectID uuid.UUID) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM emails WHERE project_id = ?`, projectID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

type ProjectStats struct {
	Total    int        `json:"total"`
	Unread   int        `json:"unread"`
	Bytes    int64      `json:"bytes"`
	Newest   *time.Time `json:"newest,omitempty"`
	Oldest   *time.Time `json:"oldest,omitempty"`
	LastSeen *time.Time `json:"last_credential_use,omitempty"`
}

func (s *Store) ProjectStats(ctx context.Context, projectID uuid.UUID) (*ProjectStats, error) {
	var st ProjectStats
	err := s.db.QueryRowContext(ctx, `
		SELECT count(*), COALESCE(sum(CASE WHEN seen THEN 0 ELSE 1 END), 0), COALESCE(sum(size), 0), max(received_at), min(received_at),
		       (SELECT max(last_used_at) FROM project_credentials c WHERE c.project_id = ?)
		FROM emails WHERE project_id = ?`, projectID, projectID).Scan(&st.Total, &st.Unread, &st.Bytes, nt(&st.Newest), nt(&st.Oldest), nt(&st.LastSeen))
	return &st, err
}

// ApplyRetention deletes emails older than each active project's retention
// window and trims the mailbox to max_emails. It returns the number of deleted rows.
func (s *Store) ApplyRetention(ctx context.Context) (int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, retention_days, max_emails FROM projects WHERE status = 'active'`)
	if err != nil {
		return 0, err
	}
	type proj struct {
		id        uuid.UUID
		days, max int
	}
	var projects []proj
	for rows.Next() {
		var p proj
		if err := rows.Scan(&p.id, &p.days, &p.max); err != nil {
			rows.Close()
			return 0, err
		}
		projects = append(projects, p)
	}
	rows.Close()
	var total int64
	for _, p := range projects {
		cutoff := now().Add(-time.Duration(p.days) * 24 * time.Hour)
		res, err := s.db.ExecContext(ctx, `DELETE FROM emails WHERE project_id = ? AND received_at < ?`, p.id, cutoff)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
		res, err = s.db.ExecContext(ctx, `
			DELETE FROM emails WHERE project_id = ? AND uid <= (
				SELECT x.uid FROM emails x WHERE x.project_id = ? ORDER BY x.uid DESC LIMIT 1 OFFSET ?)`, p.id, p.id, p.max)
		if err != nil {
			return total, err
		}
		n, _ = res.RowsAffected()
		total += n
	}
	return total, nil
}

// IMAP helpers

type IMAPEntry struct {
	ID         uuid.UUID
	UID        int64
	Size       int
	ReceivedAt time.Time
	Seen       bool
	Flagged    bool
	Deleted    bool
}

func (s *Store) IMAPList(ctx context.Context, projectID uuid.UUID) ([]IMAPEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, uid, size, received_at, seen, flagged, deleted FROM emails WHERE project_id = ? ORDER BY uid`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IMAPEntry
	for rows.Next() {
		var e IMAPEntry
		if err := rows.Scan(&e.ID, &e.UID, &e.Size, ts(&e.ReceivedAt), &e.Seen, &e.Flagged, &e.Deleted); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) IMAPSetFlags(ctx context.Context, projectID uuid.UUID, uid int64, seen, flagged, deleted bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE emails SET seen = ?, flagged = ?, deleted = ? WHERE project_id = ? AND uid = ?`, seen, flagged, deleted, projectID, uid)
	return err
}

func (s *Store) IMAPExpunge(ctx context.Context, projectID uuid.UUID, uids []int64) error {
	if len(uids) == 0 {
		return nil
	}
	args := []any{projectID}
	for _, u := range uids {
		args = append(args, u)
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM emails WHERE project_id = ? AND deleted AND uid IN (`+placeholders(len(uids))+`)`, args...)
	return err
}

func (s *Store) GetRawEmailByUID(ctx context.Context, projectID uuid.UUID, uid int64) ([]byte, error) {
	var raw []byte
	if err := s.db.QueryRowContext(ctx, `SELECT raw FROM emails WHERE project_id = ? AND uid = ?`, projectID, uid).Scan(&raw); err != nil {
		return nil, wrapNotFound(err)
	}
	return raw, nil
}

func (s *Store) ProjectUIDState(ctx context.Context, projectID uuid.UUID) (uidValidity uint32, nextUID int64, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT uid_validity, next_uid FROM projects WHERE id = ?`, projectID).Scan(&uidValidity, &nextUID)
	return uidValidity, nextUID, wrapNotFound(err)
}
