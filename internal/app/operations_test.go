package app

import (
	"context"
	"github.com/google/uuid"
	"github.com/ramst/pause/internal/domain"
	"testing"
	"time"
)

func TestP06OperationsRecoveryIsReadFirstAndScoped(t *testing.T) {
	f := setupPayments(t)
	ctx := context.Background()
	i := f.held(t)
	out, err := f.s.OperationalStatus(ctx, actor(domain.Reviewer))
	if err != nil {
		t.Fatal(err)
	}
	if out.WorkerHealthy {
		t.Fatal("missing worker reported healthy")
	}
	if err = f.s.Heartbeat(ctx, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	out, err = f.s.OperationalStatus(ctx, actor(domain.Reviewer))
	if err != nil || !out.WorkerHealthy {
		t.Fatal(out, err)
	}
	if _, err = f.s.OperationalStatus(ctx, actor(domain.Senior)); err == nil {
		t.Fatal("senior operations access")
	}
	f.clock.now = f.clock.now.Add(31 * time.Minute)
	out, err = f.s.OperationalStatus(ctx, actor(domain.Reviewer))
	if err != nil || out.OverdueVoids != 1 || out.WorkerHealthy {
		t.Fatal(out, err)
	}
	foreign, err := f.s.Operations(ctx, uuid.NewString(), uuid.NewString())
	if err != nil || foreign.OverdueVoids != 0 {
		t.Fatal(foreign, err)
	}
	if err = f.s.QueueReconciliation(ctx, i.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.s.QueueReconciliation(ctx, i.ID); err != nil {
		t.Fatal(err)
	}
	var captures, reconcile int
	if err = f.s.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM payment_operations WHERE kind='CAPTURE'`).Scan(&captures); err != nil || captures != 0 {
		t.Fatal(captures, err)
	}
	if err = f.s.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind='PAYMENT_RECONCILE'`).Scan(&reconcile); err != nil || reconcile != 1 {
		t.Fatal(reconcile, err)
	}
	f.work(t)
	if got := f.status(t, i.ID); got != domain.Voided {
		t.Fatal(got)
	}
	if f.s.ReleaseReady(ctx) == nil {
		t.Fatal("unconfigured analysis must not be ready")
	}
}

func TestP06OnlyExhaustedVoidCanBeRetried(t *testing.T) {
	f := setupPayments(t)
	ctx := context.Background()
	i := f.held(t)
	if err := f.s.RetryVoid(ctx, i.ID); err == nil {
		t.Fatal("retry without an exhausted void")
	}
	if _, err := f.s.Cancel(ctx, actor(domain.Senior), i.ID, 1, "retry-void-fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.DB.Pool.Exec(ctx, `UPDATE jobs j SET failed_at=$2,attempts=max_attempts FROM payment_operations o WHERE j.object_id=o.id AND o.intent_id=$1 AND o.kind='VOID'`, i.ID, f.clock.Now()); err != nil {
		t.Fatal(err)
	}
	var before, after string
	if err := f.s.DB.Pool.QueryRow(ctx, `SELECT provider_request_id FROM payment_operations WHERE intent_id=$1 AND kind='VOID'`, i.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := f.s.RetryVoid(ctx, i.ID); err != nil {
		t.Fatal(err)
	}
	f.work(t)
	if f.status(t, i.ID) != domain.Voided {
		t.Fatal("void did not converge")
	}
	if err := f.s.DB.Pool.QueryRow(ctx, `SELECT provider_request_id FROM payment_operations WHERE intent_id=$1 AND kind='VOID'`, i.ID).Scan(&after); err != nil || before != after {
		t.Fatal("provider key changed", err)
	}
	if err := f.s.RetryVoid(ctx, i.ID); err == nil {
		t.Fatal("terminal replay accepted")
	}
}
