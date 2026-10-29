package store

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/phongbui83/maildev/internal/model"
)

const emailListCols = `e.id, e.project_id, e.uid, e.credential_id, e.source, e.client_ip, e.received_at, e.message_id, e.subject,
	e.from_addrs, e.to_addrs, e.cc_addrs, e.bcc_addrs, e.envelope_from, e.envelope_to, e.sent_at, e.size,
	e.attachment_count, e.seen, e.flagged, e.deleted, left(e.text_body, 160), e.smime, e.smime_valid, e.smime_signer, e.smime_error,
	e.relay_status, COALESCE(ru.username, ''), e.relay_requested_at, COALESCE(du.username, ''), e.relay_decided_at, e.relay_error, e.relay_remote_id, e.relay_note`

// emailJoins must accompany emailListCols.
const emailJoins = ` LEFT JOIN users ru ON ru.id = e.relay_requested_by LEFT JOIN users du ON du.id = e.relay_decided_by`

func scanEmailList(row pgx.Row) (*model.Email, error) {
	var e model.Email
	var from, to, cc, bcc []byte
	if err := row.Scan(&e.ID, &e.ProjectID, &e.UID, &e.CredentialID, &e.Source, &e.ClientIP, &e.ReceivedAt, &e.MessageID, &e.Subject,
		&from, &to, &cc, &bcc, &e.EnvelopeFrom, &e.EnvelopeTo, &e.SentAt, &e.Size,
		&e.AttachmentCount, &e.Seen, &e.Flagged, &e.Deleted, &e.Snippet, &e.SMIME, &e.SMIMEValid, &e.SMIMESigner, &e.SMIMEError,
		&e.RelayStatus, &e.RelayRequestedBy, &e.RelayRequestedAt, &e.RelayDecidedBy, &e.RelayDecidedAt, &e.RelayError, &e.RelayRemoteID, &e.RelayNote); err != nil {
		return nil, wrapNotFound(err)
	}
	_ = json.Unmarshal(from, &e.From)
	_ = json.Unmarshal(to, &e.To)
	_ = json.Unmarshal(cc, &e.Cc)
	_ = json.Unmarshal(bcc, &e.Bcc)
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

	// At-rest encryption: raw bytes, bodies and attachments are sealed under the
	// project's data key; metadata (subject, addresses, headers) stays searchable.
	// This runs BEFORE the transaction: wrapping a new data key updates the
	// project row, which the transaction below locks (NextUID).
	storedRaw, storedText, storedHTML := raw, e.TextBody, e.HTMLBody
	var bodyEnc []byte
	attData := make([][]byte, len(p.Attachments))
	for i := range p.Attachments {
		attData[i] = p.Attachments[i].Data
	}
	if s.enc != nil {
		var err error
		if storedRaw, err = s.enc.Encrypt(ctx, e.ProjectID, raw); err != nil {
			return nil, err
		}
		if bodyEnc, err = s.sealBody(ctx, e.ProjectID, e.TextBody, e.HTMLBody); err != nil {
			return nil, err
		}
		storedText, storedHTML = "", ""
		for i := range p.Attachments {
			if attData[i], err = s.enc.Encrypt(ctx, e.ProjectID, p.Attachments[i].Data); err != nil {
				return nil, err
			}
		}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

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
	addrText := addrText(e)
	if e.EnvelopeTo == nil {
		e.EnvelopeTo = []string{}
	}
	relayStatus := model.RelayNone
	var relayRequestedAt *time.Time
	if p.RelayPending {
		relayStatus = model.RelayPending
		now := time.Now()
		relayRequestedAt = &now
	}
	e.RelayStatus = relayStatus
	e.RelayRequestedAt = relayRequestedAt

	err = tx.QueryRow(ctx, `
		INSERT INTO emails (project_id, uid, credential_id, source, client_ip, message_id, subject,
			from_addrs, to_addrs, cc_addrs, bcc_addrs, envelope_from, envelope_to, sent_at, size,
			text_body, html_body, headers, attachment_count, raw, addr_text, relay_status, relay_requested_at, smime, smime_valid, smime_signer, smime_error,
			encrypted, body_enc)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29)
		RETURNING id, received_at`,
		e.ProjectID, uid, e.CredentialID, e.Source, e.ClientIP, e.MessageID, e.Subject,
		from, to, cc, bcc, e.EnvelopeFrom, e.EnvelopeTo, e.SentAt, len(raw),
		storedText, storedHTML, headers, len(p.Attachments), storedRaw, addrText, relayStatus, relayRequestedAt, e.SMIME, e.SMIMEValid, e.SMIMESigner, e.SMIMEError,
		s.enc != nil, bodyEnc,
	).Scan(&e.ID, &e.ReceivedAt)
	if err != nil {
		return nil, err
	}
	e.Size = len(raw)
	e.AttachmentCount = len(p.Attachments)

	for i := range p.Attachments {
		a := &p.Attachments[i]
		a.EmailID = e.ID
		data := attData[i]
		if err := tx.QueryRow(ctx, `
			INSERT INTO attachments (email_id, filename, content_type, size, content_id, inline, data, encrypted)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
			e.ID, a.Filename, a.ContentType, len(a.Data), a.ContentID, a.Inline, data, s.enc != nil).Scan(&a.ID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
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
			// also index local part and domain separately so "john" matches john@x
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
	where := `e.project_id = $1`
	args := []any{p.ProjectID}
	if p.Unread {
		where += ` AND NOT e.seen`
	}
	if q := strings.TrimSpace(p.Query); q != "" {
		args = append(args, q, "%"+q+"%")
		where += ` AND (e.search @@ plainto_tsquery('simple', $2) OR e.subject ILIKE $3 OR e.addr_text ILIKE $3)`
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM emails e WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, p.Limit, p.Offset)
	rows, err := s.pool.Query(ctx, `SELECT `+emailListCols+` FROM emails e`+emailJoins+` WHERE `+where+
		` ORDER BY e.received_at DESC, e.uid DESC LIMIT $`+itoa(len(args)-1)+` OFFSET $`+itoa(len(args)), args...)
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

func itoa(i int) string { return strconv.Itoa(i) }

var itoaFn = strconv.Itoa

// GetEmail returns the full email (bodies, headers, attachment metadata) without raw bytes.
func (s *Store) GetEmail(ctx context.Context, projectID, id uuid.UUID) (*model.Email, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+emailListCols+`, e.text_body, e.html_body, e.headers, e.encrypted, e.body_enc FROM emails e`+emailJoins+` WHERE e.project_id = $1 AND e.id = $2`, projectID, id)
	var e model.Email
	var from, to, cc, bcc, headers, bodyEnc []byte
	var encrypted bool
	if err := row.Scan(&e.ID, &e.ProjectID, &e.UID, &e.CredentialID, &e.Source, &e.ClientIP, &e.ReceivedAt, &e.MessageID, &e.Subject,
		&from, &to, &cc, &bcc, &e.EnvelopeFrom, &e.EnvelopeTo, &e.SentAt, &e.Size,
		&e.AttachmentCount, &e.Seen, &e.Flagged, &e.Deleted, &e.Snippet, &e.SMIME, &e.SMIMEValid, &e.SMIMESigner, &e.SMIMEError,
		&e.RelayStatus, &e.RelayRequestedBy, &e.RelayRequestedAt, &e.RelayDecidedBy, &e.RelayDecidedAt, &e.RelayError, &e.RelayRemoteID, &e.RelayNote,
		&e.TextBody, &e.HTMLBody, &headers, &encrypted, &bodyEnc); err != nil {
		return nil, wrapNotFound(err)
	}
	if encrypted {
		if s.enc == nil {
			return nil, errEncryptedNoKey
		}
		text, html, derr := s.openBody(ctx, projectID, bodyEnc)
		if derr != nil {
			return nil, derr
		}
		e.TextBody, e.HTMLBody = text, html
	}
	_ = json.Unmarshal(from, &e.From)
	_ = json.Unmarshal(to, &e.To)
	_ = json.Unmarshal(cc, &e.Cc)
	_ = json.Unmarshal(bcc, &e.Bcc)
	_ = json.Unmarshal(headers, &e.Headers)
	e.Snippet = ""

	rows, err := s.pool.Query(ctx, `SELECT id, email_id, filename, content_type, size, content_id, inline FROM attachments WHERE email_id = $1 ORDER BY filename`, id)
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
	var encrypted bool
	if err := s.pool.QueryRow(ctx, `SELECT raw, encrypted FROM emails WHERE project_id = $1 AND id = $2`, projectID, id).Scan(&raw, &encrypted); err != nil {
		return nil, wrapNotFound(err)
	}
	return s.decryptRaw(ctx, projectID, raw, encrypted)
}

func (s *Store) GetAttachment(ctx context.Context, projectID, emailID, attID uuid.UUID) (*model.Attachment, error) {
	return s.attachment(ctx, projectID, `a.id = $3`, emailID, attID)
}

func (s *Store) GetAttachmentByContentID(ctx context.Context, projectID, emailID uuid.UUID, cid string) (*model.Attachment, error) {
	return s.attachment(ctx, projectID, `a.content_id = $3`, emailID, cid)
}

func (s *Store) attachment(ctx context.Context, projectID uuid.UUID, cond string, emailID uuid.UUID, arg any) (*model.Attachment, error) {
	var a model.Attachment
	var encrypted bool
	err := s.pool.QueryRow(ctx, `
		SELECT a.id, a.email_id, a.filename, a.content_type, a.size, a.content_id, a.inline, a.data, a.encrypted
		FROM attachments a JOIN emails e ON e.id = a.email_id
		WHERE e.project_id = $1 AND a.email_id = $2 AND `+cond+` LIMIT 1`, projectID, emailID, arg).
		Scan(&a.ID, &a.EmailID, &a.Filename, &a.ContentType, &a.Size, &a.ContentID, &a.Inline, &a.Data, &encrypted)
	if err != nil {
		return nil, wrapNotFound(err)
	}
	if a.Data, err = s.decryptRaw(ctx, projectID, a.Data, encrypted); err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *Store) SetEmailSeen(ctx context.Context, projectID, id uuid.UUID, seen bool) error {
	ct, err := s.pool.Exec(ctx, `UPDATE emails SET seen = $3 WHERE project_id = $1 AND id = $2`, projectID, id, seen)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) MarkAllSeen(ctx context.Context, projectID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE emails SET seen = true WHERE project_id = $1 AND NOT seen`, projectID)
	return err
}

func (s *Store) DeleteEmail(ctx context.Context, projectID, id uuid.UUID) error {
	ct, err := s.pool.Exec(ctx, `DELETE FROM emails WHERE project_id = $1 AND id = $2`, projectID, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteAllEmails(ctx context.Context, projectID uuid.UUID) (int64, error) {
	ct, err := s.pool.Exec(ctx, `DELETE FROM emails WHERE project_id = $1`, projectID)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
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
	err := s.pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE NOT seen), COALESCE(sum(size), 0), max(received_at), min(received_at),
		       (SELECT max(last_used_at) FROM project_credentials c WHERE c.project_id = $1)
		FROM emails WHERE project_id = $1`, projectID).Scan(&st.Total, &st.Unread, &st.Bytes, &st.Newest, &st.Oldest, &st.LastSeen)
	return &st, err
}

// Retention helpers

// ApplyRetention deletes emails older than the project's retention window and trims
// the mailbox to max_emails. It returns the number of deleted rows.
func (s *Store) ApplyRetention(ctx context.Context) (int64, error) {
	var total int64
	ct, err := s.pool.Exec(ctx, `
		DELETE FROM emails e USING projects p
		WHERE e.project_id = p.id AND p.status = 'active' AND e.received_at < now() - (p.retention_days || ' days')::interval`)
	if err != nil {
		return 0, err
	}
	total += ct.RowsAffected()
	ct, err = s.pool.Exec(ctx, `
		DELETE FROM emails WHERE id IN (
			SELECT e.id FROM emails e JOIN projects p ON p.id = e.project_id
			WHERE p.status = 'active' AND e.uid <= (
				SELECT x.uid FROM emails x WHERE x.project_id = p.id
				ORDER BY x.uid DESC OFFSET p.max_emails LIMIT 1
			)
		)`)
	if err != nil {
		return total, err
	}
	return total + ct.RowsAffected(), nil
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
	rows, err := s.pool.Query(ctx, `SELECT id, uid, size, received_at, seen, flagged, deleted FROM emails WHERE project_id = $1 ORDER BY uid`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IMAPEntry
	for rows.Next() {
		var e IMAPEntry
		if err := rows.Scan(&e.ID, &e.UID, &e.Size, &e.ReceivedAt, &e.Seen, &e.Flagged, &e.Deleted); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) IMAPSetFlags(ctx context.Context, projectID uuid.UUID, uid int64, seen, flagged, deleted bool) error {
	_, err := s.pool.Exec(ctx, `UPDATE emails SET seen = $3, flagged = $4, deleted = $5 WHERE project_id = $1 AND uid = $2`, projectID, uid, seen, flagged, deleted)
	return err
}

func (s *Store) IMAPExpunge(ctx context.Context, projectID uuid.UUID, uids []int64) error {
	if len(uids) == 0 {
		return nil
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM emails WHERE project_id = $1 AND uid = ANY($2) AND deleted`, projectID, uids)
	return err
}

func (s *Store) GetRawEmailByUID(ctx context.Context, projectID uuid.UUID, uid int64) ([]byte, error) {
	var raw []byte
	var encrypted bool
	if err := s.pool.QueryRow(ctx, `SELECT raw, encrypted FROM emails WHERE project_id = $1 AND uid = $2`, projectID, uid).Scan(&raw, &encrypted); err != nil {
		return nil, wrapNotFound(err)
	}
	return s.decryptRaw(ctx, projectID, raw, encrypted)
}

func (s *Store) ProjectUIDState(ctx context.Context, projectID uuid.UUID) (uidValidity uint32, nextUID int64, err error) {
	err = s.pool.QueryRow(ctx, `SELECT uid_validity, next_uid FROM projects WHERE id = $1`, projectID).Scan(&uidValidity, &nextUID)
	return uidValidity, nextUID, wrapNotFound(err)
}
