package main

import (
	"flag"
	"github.com/ramst/pause/internal/bootstrap"
	"os"
)

func main() {
	scenario := flag.String("scenario", "f04", "f04 or benign, isolated synthetic accounts")
	flag.Parse()
	ctx, stop := bootstrap.Context()
	defer stop()
	log := bootstrap.Logger()
	s, err := bootstrap.Load(ctx)
	if err != nil {
		log.Error("scenario_seed_failed", "reason_code", "CONFIGURATION_ERROR")
		os.Exit(1)
	}
	defer s.DB.Pool.Close()
	email, err := s.SeedPatterns(ctx, *scenario, os.Getenv("SEED_PASSWORD"))
	if err != nil {
		log.Error("scenario_seed_failed", "reason_code", "SEED_REJECTED")
		os.Exit(1)
	}
	log.Info("synthetic_scenario_ready", "senior_email", email, "history_outcome", "SYNTHETIC_ONLY")
}
