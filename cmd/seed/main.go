package main

import (
	"os"

	"github.com/ramst/pause/internal/bootstrap"
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
	if e = a.DB.Seed(ctx, a.Config.Environment, os.Getenv("SEED_PASSWORD"), a.Clock); e != nil {
		log.Error("seed_failed", "reason_code", "SEED_REJECTED")
		os.Exit(1)
	}
	log.Info("synthetic_accounts_seeded", "accounts", 6)
}
