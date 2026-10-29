package store

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// Encryptor is implemented by the envelope-encryption service. When nil, data is
// stored in clear (the pre-encryption behaviour).
type Encryptor interface {
	Encrypt(ctx context.Context, projectID uuid.UUID, plaintext []byte) ([]byte, error)
	Decrypt(ctx context.Context, projectID uuid.UUID, data []byte) ([]byte, error)
}

// SetEncryptor enables at-rest encryption for new messages and transparent
// decryption on read.
func (s *Store) SetEncryptor(e Encryptor) { s.enc = e }

func (s *Store) Encrypting() bool { return s.enc != nil }

// GetProjectKey / SetProjectKey implement encryption.KeyStore.
func (s *Store) GetProjectKey(ctx context.Context, projectID uuid.UUID) (string, string, error) {
	var slug, wrapped string
	err := s.pool.QueryRow(ctx, `SELECT slug, dek_wrapped FROM projects WHERE id = $1`, projectID).Scan(&slug, &wrapped)
	return slug, wrapped, wrapNotFound(err)
}

func (s *Store) SetProjectKey(ctx context.Context, projectID uuid.UUID, wrapped string) error {
	_, err := s.pool.Exec(ctx, `UPDATE projects SET dek_wrapped = $2 WHERE id = $1 AND dek_wrapped = ''`, projectID, wrapped)
	return err
}

type bodyBlob struct {
	Text string `json:"t"`
	HTML string `json:"h"`
}

func (s *Store) sealBody(ctx context.Context, projectID uuid.UUID, text, html string) ([]byte, error) {
	b, _ := json.Marshal(bodyBlob{Text: text, HTML: html})
	return s.enc.Encrypt(ctx, projectID, b)
}

func (s *Store) openBody(ctx context.Context, projectID uuid.UUID, blob []byte) (string, string, error) {
	if len(blob) == 0 {
		return "", "", nil
	}
	b, err := s.enc.Decrypt(ctx, projectID, blob)
	if err != nil {
		return "", "", err
	}
	var bb bodyBlob
	if err := json.Unmarshal(b, &bb); err != nil {
		return "", "", err
	}
	return bb.Text, bb.HTML, nil
}

var errEncryptedNoKey = errors.New("message is encrypted but encryption is not configured")

// decryptRaw returns the raw message bytes in clear.
func (s *Store) decryptRaw(ctx context.Context, projectID uuid.UUID, raw []byte, encrypted bool) ([]byte, error) {
	if !encrypted {
		return raw, nil
	}
	if s.enc == nil {
		return nil, errEncryptedNoKey
	}
	return s.enc.Decrypt(ctx, projectID, raw)
}

// EncryptBacklog encrypts messages stored before encryption was enabled, in
// batches, until none are left or ctx ends. Returns the number processed.
func (s *Store) EncryptBacklog(ctx context.Context, batch int) (int, error) {
	if s.enc == nil {
		return 0, nil
	}
	total := 0
	for {
		rows, err := s.pool.Query(ctx, `SELECT id, project_id, raw, text_body, html_body FROM emails WHERE NOT encrypted LIMIT $1`, batch)
		if err != nil {
			return total, err
		}
		type item struct {
			id, pid    uuid.UUID
			raw        []byte
			text, html string
		}
		var items []item
		for rows.Next() {
			var it item
			if err := rows.Scan(&it.id, &it.pid, &it.raw, &it.text, &it.html); err != nil {
				rows.Close()
				return total, err
			}
			items = append(items, it)
		}
		rows.Close()
		if len(items) == 0 {
			return total, nil
		}
		for _, it := range items {
			if err := s.encryptOne(ctx, it.id, it.pid, it.raw, it.text, it.html); err != nil {
				slog.Warn("backlog encryption failed", "email", it.id, "err", err)
				// Skip this row for now; it stays unencrypted and is retried next run.
				time.Sleep(100 * time.Millisecond)
				continue
			}
			total++
		}
		if len(items) < batch {
			return total, nil
		}
	}
}

func (s *Store) encryptOne(ctx context.Context, id, pid uuid.UUID, raw []byte, text, html string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	rawEnc, err := s.enc.Encrypt(ctx, pid, raw)
	if err != nil {
		return err
	}
	bodyEnc, err := s.sealBody(ctx, pid, text, html)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE emails SET raw = $2, body_enc = $3, text_body = '', html_body = '', encrypted = true WHERE id = $1 AND NOT encrypted`, id, rawEnc, bodyEnc); err != nil {
		return err
	}
	arows, err := tx.Query(ctx, `SELECT id, data FROM attachments WHERE email_id = $1 AND NOT encrypted`, id)
	if err != nil {
		return err
	}
	type att struct {
		id   uuid.UUID
		data []byte
	}
	var atts []att
	for arows.Next() {
		var a att
		if err := arows.Scan(&a.id, &a.data); err != nil {
			arows.Close()
			return err
		}
		atts = append(atts, a)
	}
	arows.Close()
	for _, a := range atts {
		enc, err := s.enc.Encrypt(ctx, pid, a.data)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE attachments SET data = $2, encrypted = true WHERE id = $1`, a.id, enc); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
