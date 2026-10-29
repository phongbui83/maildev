// Package encryption implements per-project envelope encryption: each project has
// a 256-bit data key (DEK) that is generated locally, wrapped by the project's
// OpenBao Transit key and stored on the project row. Message bodies, raw bytes
// and attachments are encrypted with AES-GCM under the DEK. Deleting the Transit
// key makes every message of the project unreadable (crypto-shredding).
package encryption

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/google/uuid"

	"github.com/phongbui83/maildev/internal/openbao"
)

// KeyStore persists wrapped keys (implemented by the Postgres store).
type KeyStore interface {
	GetProjectKey(ctx context.Context, projectID uuid.UUID) (slug, wrapped string, err error)
	SetProjectKey(ctx context.Context, projectID uuid.UUID, wrapped string) error
}

type Service struct {
	bao   *openbao.Client
	keys  KeyStore
	mu    sync.Mutex
	cache map[uuid.UUID]cipher.AEAD
}

func New(bao *openbao.Client, keys KeyStore) *Service {
	return &Service{bao: bao, keys: keys, cache: map[uuid.UUID]cipher.AEAD{}}
}

func newAEAD(dek []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// aead returns the project's cipher, creating and wrapping a DEK on first use.
func (s *Service) aead(ctx context.Context, projectID uuid.UUID) (cipher.AEAD, error) {
	s.mu.Lock()
	if a, ok := s.cache[projectID]; ok {
		s.mu.Unlock()
		return a, nil
	}
	s.mu.Unlock()

	slug, wrapped, err := s.keys.GetProjectKey(ctx, projectID)
	if err != nil {
		return nil, err
	}
	var dek []byte
	if wrapped == "" {
		if err := s.bao.EnsureKey(ctx, slug); err != nil {
			return nil, fmt.Errorf("transit key for %s: %w", slug, err)
		}
		dek = make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, dek); err != nil {
			return nil, err
		}
		wrapped, err = s.bao.Wrap(ctx, slug, dek)
		if err != nil {
			return nil, fmt.Errorf("wrap data key for %s: %w", slug, err)
		}
		if err := s.keys.SetProjectKey(ctx, projectID, wrapped); err != nil {
			return nil, err
		}
	} else {
		dek, err = s.bao.Unwrap(ctx, slug, wrapped)
		if err != nil {
			return nil, fmt.Errorf("unwrap data key for %s: %w", slug, err)
		}
	}
	a, err := newAEAD(dek)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.cache[projectID] = a
	s.mu.Unlock()
	return a, nil
}

// Encrypt returns nonce||ciphertext for the project.
func (s *Service) Encrypt(ctx context.Context, projectID uuid.UUID, plaintext []byte) ([]byte, error) {
	a, err := s.aead(ctx, projectID)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return append(nonce, a.Seal(nil, nonce, plaintext, nil)...), nil
}

func (s *Service) Decrypt(ctx context.Context, projectID uuid.UUID, data []byte) ([]byte, error) {
	a, err := s.aead(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if len(data) < a.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	return a.Open(nil, data[:a.NonceSize()], data[a.NonceSize():], nil)
}

// Forget drops the cached key (after a project is deleted or its key rotated).
func (s *Service) Forget(projectID uuid.UUID) {
	s.mu.Lock()
	delete(s.cache, projectID)
	s.mu.Unlock()
}

// Shred destroys the project's Transit key. Call after deleting the project row.
func (s *Service) Shred(ctx context.Context, slug string, projectID uuid.UUID) error {
	s.Forget(projectID)
	return s.bao.DeleteKey(ctx, slug)
}

// Ping checks OpenBao connectivity.
func (s *Service) Ping(ctx context.Context) error { return s.bao.Ping(ctx) }
