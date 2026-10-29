// Local operator tool. No public mutation endpoint or financial replay exists.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"github.com/ramst/pause/internal/bootstrap"
	"os"
	"time"
)

func main() {
	reconcile := flag.String("reconcile", "", "queue provider-read reconciliation for one intent UUID")
	retry := flag.Bool("retry-private", false, "retry failed private projections using current canonical consent")
	retryVoid := flag.String("retry-void", "", "retry only an exhausted void job for an intent, preserving provider request ID")
	flag.Parse()
	ctx, stop := bootstrap.Context()
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	s, err := bootstrap.Load(ctx)
	if err != nil {
		bootstrap.Logger().Error("ops_failed", "reason_code", "CONFIGURATION_ERROR")
		os.Exit(1)
	}
	defer s.DB.Pool.Close()
	if *reconcile != "" {
		err = s.QueueReconciliation(ctx, *reconcile)
	}
	if err == nil && *retryVoid != "" {
		err = s.RetryVoid(ctx, *retryVoid)
	}
	if err == nil && *retry {
		err = s.RetryHistoryCleanup(ctx)
	}
	if err != nil {
		bootstrap.Logger().Error("ops_failed", "reason_code", "RECOVERY_REJECTED")
		os.Exit(1)
	}
	out, err := s.Operations(ctx, "", "")
	if err != nil {
		bootstrap.Logger().Error("ops_failed", "reason_code", "DATABASE_UNAVAILABLE")
		os.Exit(1)
	}
	_ = json.NewEncoder(os.Stdout).Encode(out)
}
