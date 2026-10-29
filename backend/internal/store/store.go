// Package store is the PostgreSQL persistence layer.
package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/phongbui83/maildev/internal/secrets"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict")

type Store struct {
	pool *pgxpool.Pool
	box  *secrets.Box
	enc  Encryptor // nil = store in clear
}

func New(pool *pgxpool.Pool, box *secrets.Box) *Store {
	return &Store{pool: pool, box: box}
}

func (s *Store) Pool() *pgxpool.Pool { return s.pool }

func wrapNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// Ping is used by the readiness probe.
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }
