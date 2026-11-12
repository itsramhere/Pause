package main

import (
	"net/url"
	"os"
	"strings"

	"github.com/ramst/pause/internal/bootstrap"
	"github.com/ramst/pause/migrations"
)

func main() {
	log := bootstrap.Logger()
	ctx, stop := bootstrap.Context()
	defer stop()
	a, e := bootstrap.Load(ctx)
	if e != nil {
		log.Error("configuration_failed", "reason", e.Error())
		os.Exit(1)
	}
	defer a.DB.Pool.Close()

	if u, err := url.Parse(a.Config.DatabaseURL); err == nil && strings.Contains(u.Host, "-pooler") {
		log.Warn("neon_pooler_detected", "hint", "Neon connection pooler (-pooler) detected; migrations use advisory locks which can fail with PgBouncer. Use the direct (non-pooler) Neon host for migrations.")
	}

	if e = migrations.Apply(ctx, a.DB.Pool); e != nil {
		if u, err := url.Parse(a.Config.DatabaseURL); err == nil && strings.Contains(u.Host, "-pooler") {
			log.Error("migration_failed", "reason_code", "DATABASE_MIGRATION_FAILED", "error", e.Error(), "hint", "Neon pooled connection (-pooler) detected. Re-run migrations using the direct (non-pooler) Neon host.")
		} else {
			log.Error("migration_failed", "reason_code", "DATABASE_MIGRATION_FAILED", "error", e.Error())
		}
		os.Exit(1)
	}
	log.Info("migrations_applied", "version", migrations.Version)
}
