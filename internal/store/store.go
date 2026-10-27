package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ Pool *pgxpool.Pool }

func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		return nil, e
	}
	cfg.MaxConns = 10
	cfg.ConnConfig.ConnectTimeout = 3 * time.Second
	p, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		return nil, e
	}
	return &Store{p}, nil
}

// WithTx rolls back every failed write, including its audit/outbox work. No
// network calls belong inside this callback. Lock intent before version writes.
func (s *Store) WithTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = fn(tx); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
