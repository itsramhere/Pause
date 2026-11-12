package store

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestNeonDatabaseURLParsing(t *testing.T) {
	neonDSN := "postgres://user:password@ep-cool-cloud-123456.us-east-2.aws.neon.tech/pause?sslmode=require&pool_max_conns=5"
	cfg, err := pgxpool.ParseConfig(neonDSN)
	if err != nil {
		t.Fatalf("failed to parse neon DSN: %v", err)
	}
	if cfg.MaxConns != 5 {
		t.Fatalf("expected MaxConns=5, got %d", cfg.MaxConns)
	}
	if cfg.ConnConfig.TLSConfig == nil {
		t.Fatalf("expected TLSConfig to be configured for sslmode=require")
	}

	// Verify that Open constructs the pool successfully without immediate connection error
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := Open(ctx, neonDSN)
	if err != nil {
		t.Fatalf("store.Open failed with neon DSN: %v", err)
	}
	defer s.Pool.Close()

	if s.Pool.Config().MaxConns != 5 {
		t.Fatalf("expected pool MaxConns=5, got %d", s.Pool.Config().MaxConns)
	}
}

func TestNeonPoolerHostURLParsing(t *testing.T) {
	neonPoolerDSN := "postgres://user:password@ep-cool-cloud-123456-pooler.us-east-2.aws.neon.tech/pause?sslmode=require&pool_max_conns=8"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := Open(ctx, neonPoolerDSN)
	if err != nil {
		t.Fatalf("store.Open failed with neon pooler DSN: %v", err)
	}
	defer s.Pool.Close()

	if s.Pool.Config().MaxConns != 8 {
		t.Fatalf("expected pool MaxConns=8, got %d", s.Pool.Config().MaxConns)
	}
}
