package main

import (
	"os"

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
	if e = migrations.Apply(ctx, a.DB.Pool); e != nil {
		log.Error("migration_failed", "reason_code", "DATABASE_MIGRATION_FAILED")
		os.Exit(1)
	}
	log.Info("migrations_applied", "version", migrations.Version)
}
