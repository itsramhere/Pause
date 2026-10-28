package main

import (
	"encoding/json"
	"flag"
	"github.com/ramst/pause/internal/bootstrap"
	"os"
)

func main() {
	scenario := flag.String("scenario", "stopped", "stopped or positive")
	flag.Parse()
	ctx, stop := bootstrap.Context()
	defer stop()
	s, e := bootstrap.Load(ctx)
	if e != nil {
		bootstrap.Logger().Error("demo_seed_failed", "reason_code", "CONFIGURATION_ERROR")
		os.Exit(1)
	}
	defer s.DB.Pool.Close()
	out, e := s.LaunchDemo(ctx, *scenario, "explicit-cli")
	if e != nil {
		bootstrap.Logger().Error("demo_seed_failed", "reason_code", "LAUNCH_REJECTED")
		os.Exit(1)
	}
	// Contains disposable credentials: keep this output private, never commit it.
	_ = json.NewEncoder(os.Stdout).Encode(out)
}
