package migrations

import (
	"context"
	"embed"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ramst/pause/internal/domain"
)

//go:embed *.sql
var files embed.FS

const Version = 7

func Apply(ctx context.Context, pool *pgxpool.Pool) error {
	tx, e := pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(70127101)`); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(version integer PRIMARY KEY, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now())`); e != nil {
		return e
	}
	entries, e := files.ReadDir(".")
	if e != nil {
		return e
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		var version int
		if _, e = fmt.Sscanf(entry.Name(), "%d_", &version); e != nil {
			return e
		}
		raw, e := files.ReadFile(entry.Name())
		if e != nil {
			return e
		}
		hash := domain.Hash(raw)
		var old string
		e = tx.QueryRow(ctx, `SELECT checksum FROM schema_migrations WHERE version=$1`, version).Scan(&old)
		if e == nil {
			if old != hash {
				return fmt.Errorf("migration %d checksum mismatch", version)
			}
			continue
		}
		if e != pgx.ErrNoRows {
			return e
		}
		if _, e = tx.Exec(ctx, string(raw)); e != nil {
			return fmt.Errorf("migration %d: %w", version, e)
		}
		if _, e = tx.Exec(ctx, `INSERT INTO schema_migrations(version,checksum) VALUES($1,$2)`, version, hash); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
func Current(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	var n int
	e := pool.QueryRow(ctx, `SELECT COALESCE(max(version),0) FROM schema_migrations`).Scan(&n)
	return n, e
}
