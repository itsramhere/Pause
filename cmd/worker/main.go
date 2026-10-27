package main

import (
	"os"
	"time"

	"github.com/ramst/pause/internal/bootstrap"
	"github.com/ramst/pause/internal/store"
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
	jobs := &store.Jobs{DB: a.DB, Clock: a.Clock}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	log.Info("worker_started", "projection_dispatch", "UNAVAILABLE")
	for {
		if e = a.TickPayments(ctx); e != nil {
			log.Error("payment_scan_failed", "reason_code", "DATABASE_UNAVAILABLE")
		}
		for n := 0; n < 20; n++ {
			worked, err := a.WorkPaymentOnce(ctx)
			if err != nil {
				log.Error("payment_job_failed", "reason_code", "PAYMENT_WORKER_ERROR")
				break
			}
			if !worked {
				break
			}
		}
		if e = a.CleanupMessages(ctx); e != nil {
			log.Error("retention_cleanup_failed", "reason_code", "DATABASE_UNAVAILABLE")
		}
		if e = jobs.ReapExhausted(ctx); e != nil {
			log.Error("job_cleanup_failed")
		}
		// Only expired transient records are pruned; unresolved provider operations,
		// audit events and projection tombstones remain durable for reconciliation.
		if _, e = a.DB.Pool.Exec(ctx, `WITH sessions_deleted AS (DELETE FROM sessions WHERE expires_at<=$1), buckets_deleted AS (DELETE FROM login_buckets WHERE window_start<$2) DELETE FROM idempotency_records WHERE expires_at<=$1`, a.Clock.Now(), a.Clock.Now().Add(-24*time.Hour)); e != nil {
			log.Error("transient_cleanup_failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
