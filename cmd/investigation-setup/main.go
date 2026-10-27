package main

import (
	"github.com/ramst/pause/internal/bootstrap"
	"os"
)

func main() {
	ctx, stop := bootstrap.Context()
	defer stop()
	a, e := bootstrap.Load(ctx)
	if e == nil {
		defer a.DB.Pool.Close()
		if a.Investigation == nil {
			bootstrap.Logger().Error("investigation_not_configured")
			os.Exit(1)
		}
		e = a.Investigation.Ingest(ctx)
		if e == nil {
			e = a.ApproveReleases(ctx)
		}
	}
	if e != nil {
		bootstrap.Logger().Error("investigation_setup_failed", "reason", e.Error())
		os.Exit(1)
	}
	bootstrap.Logger().Info("approved_public_investigation_ready")
}
