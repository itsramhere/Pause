package app

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/ramst/pause/internal/domain"
	"github.com/ramst/pause/migrations"
)

type Operations struct {
	OverdueVoids       int      `json:"overdue_voids"`
	OldReconciliations int      `json:"old_reconciliations"`
	FailedJobs         int      `json:"failed_jobs"`
	QueueLagSeconds    int      `json:"queue_lag_seconds"`
	FailedVerification int      `json:"failed_verification"`
	PendingDeletes     int      `json:"pending_private_deletes"`
	WorkerHealthy      bool     `json:"worker_healthy"`
	Alerts             []string `json:"alerts"`
}

func (s *Service) Heartbeat(ctx context.Context, id string) error {
	_, err := s.DB.Pool.Exec(ctx, `WITH old AS (DELETE FROM worker_heartbeats WHERE seen_at<$3) INSERT INTO worker_heartbeats(id,seen_at) VALUES($1,$2) ON CONFLICT(id) DO UPDATE SET seen_at=EXCLUDED.seen_at`, id, s.Clock.Now(), s.Clock.Now().Add(-24*time.Hour))
	return err
}

// A process-local CLI may inspect all households. HTTP callers must use the
// active reviewer boundary below; counts include only that reviewer's objects.
func (s *Service) Operations(ctx context.Context, household, reviewer string) (Operations, error) {
	out := Operations{Alerts: []string{}}
	now := s.Clock.Now()
	err := s.DB.Pool.QueryRow(ctx, `SELECT
 count(*) FILTER(WHERE (i.cleanup_requested OR i.decision_deadline<=$1) AND i.authorization_id IS NOT NULL AND i.payment_status NOT IN ('CAPTURED','VOIDED','CLOSED_UNCAPTURED')),
 count(*) FILTER(WHERE i.payment_status='RECONCILING' AND i.created_at<$1-interval '5 minutes'),
 count(*) FILTER(WHERE i.verification_status IN ('UNAVAILABLE','EXPIRED'))
 FROM payment_intents i WHERE ($2='' OR i.household_id::text=$2) AND ($3='' OR i.reviewer_id::text=$3)`, now, household, reviewer).Scan(&out.OverdueVoids, &out.OldReconciliations, &out.FailedVerification)
	if err != nil {
		return out, err
	}
	err = s.DB.Pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE j.failed_at IS NOT NULL), COALESCE(GREATEST(0,EXTRACT(epoch FROM $1::timestamptz-min(j.not_before) FILTER(WHERE j.failed_at IS NULL AND j.not_before<=$1)))::integer,0)
 FROM jobs j JOIN payment_intents i ON i.id=CASE WHEN j.kind='PAYMENT_OPERATION' THEN (SELECT o.intent_id FROM payment_operations o WHERE o.id=j.object_id) ELSE j.object_id END
 WHERE j.completed_at IS NULL AND ($2='' OR i.household_id::text=$2) AND ($3='' OR i.reviewer_id::text=$3)`, now, household, reviewer).Scan(&out.FailedJobs, &out.QueueLagSeconds)
	if err != nil {
		return out, err
	}
	err = s.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM search_projections p JOIN payment_intents i ON i.id=p.intent_id WHERE p.action='DELETE' AND p.indexed_revision<p.revision AND ($1='' OR i.household_id::text=$1) AND ($2='' OR i.reviewer_id::text=$2)`, household, reviewer).Scan(&out.PendingDeletes)
	if err != nil {
		return out, err
	}
	err = s.DB.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM worker_heartbeats WHERE seen_at>$1)`, now.Add(-90*time.Second)).Scan(&out.WorkerHealthy)
	if err != nil {
		return out, err
	}
	if !out.WorkerHealthy {
		out.Alerts = append(out.Alerts, "WORKER_HEARTBEAT_STALE")
	}
	if out.OverdueVoids > 0 {
		out.Alerts = append(out.Alerts, "VOID_OVERDUE")
	}
	if out.OldReconciliations > 0 {
		out.Alerts = append(out.Alerts, "RECONCILIATION_OLD")
	}
	if out.FailedJobs > 0 {
		out.Alerts = append(out.Alerts, "JOBS_FAILED")
	}
	if out.QueueLagSeconds > 60 {
		out.Alerts = append(out.Alerts, "QUEUE_LAG")
	}
	if out.PendingDeletes > 0 {
		out.Alerts = append(out.Alerts, "PRIVATE_DELETE_PENDING")
	}
	if out.FailedVerification > 0 {
		out.Alerts = append(out.Alerts, "VERIFICATION_ATTENTION")
	}
	return out, nil
}

func (s *Service) OperationalStatus(ctx context.Context, a domain.Actor) (Operations, error) {
	err := s.DB.WithTx(ctx, func(tx pgx.Tx) error { return s.active(ctx, tx, a, domain.Reviewer) })
	if err != nil {
		return Operations{}, err
	}
	return s.Operations(ctx, a.HouseholdID, a.UserID)
}

// This queues provider GET reconciliation only. It cannot reset a financial
// command, alter a payment status, grant approval, or replay a capture.
func (s *Service) QueueReconciliation(ctx context.Context, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return domain.Fail("NOT_FOUND", 404)
	}
	return s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		p, err := s.paymentLock(ctx, tx, id)
		if err != nil {
			return err
		}
		if p.cmd.OrderID == "" {
			return domain.Fail("PROVIDER_ORDER_UNKNOWN", 409)
		}
		if _, err = tx.Exec(ctx, `UPDATE jobs SET failed_at=NULL,attempts=0,not_before=$2,lease_token=NULL,lease_until=NULL WHERE dedup_key=$1 AND failed_at IS NOT NULL`, "reconcile:"+id, s.Clock.Now()); err != nil {
			return err
		}
		if err = s.queue(ctx, tx, "PAYMENT_RECONCILE", id, "reconcile:"+id); err != nil {
			return err
		}
		return s.paymentAudit(ctx, tx, p, nil, "OPERATOR_RECONCILE", "PROVIDER_READ_REQUESTED", uuid.NewString())
	})
}

// Retry only exhausted VOID jobs. Keep the existing immutable request ID/body;
// dispatched/unknown commands reconcile provider truth before another void.
// Captures, provider-refused FAILED operations, and active leases are excluded.
func (s *Service) RetryVoid(ctx context.Context, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return domain.Fail("NOT_FOUND", 404)
	}
	return s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		p, err := s.paymentLock(ctx, tx, id)
		if err != nil {
			return err
		}
		if p.state == domain.Captured || p.state == domain.Voided || p.state == domain.ClosedUncaptured {
			return domain.Fail("INVALID_TRANSITION", 409)
		}
		r, err := tx.Exec(ctx, `UPDATE jobs j SET failed_at=NULL,attempts=0,not_before=$2,lease_token=NULL,lease_until=NULL FROM payment_operations o WHERE j.object_id=o.id AND j.kind='PAYMENT_OPERATION' AND o.intent_id=$1 AND o.kind='VOID' AND o.state IN ('RESERVED','DISPATCHED','UNKNOWN') AND j.failed_at IS NOT NULL AND (j.lease_until IS NULL OR j.lease_until<=$2) AND NOT EXISTS(SELECT 1 FROM payment_operations c WHERE c.intent_id=$1 AND c.kind='CAPTURE' AND c.state IN ('RESERVED','DISPATCHED','UNKNOWN'))`, id, s.Clock.Now())
		if err != nil {
			return err
		}
		if r.RowsAffected() != 1 {
			return domain.Fail("RECOVERY_REJECTED", 409)
		}
		return s.paymentAudit(ctx, tx, p, nil, "OPERATOR_VOID_RETRY", "EXHAUSTED_VOID_REQUEUED", uuid.NewString())
	})
}

// Separate from API availability: ML/search failure must not remove the routes
// for denial/cancel, or prevent workers from voiding and reconciling.
func (s *Service) ReleaseReady(ctx context.Context) error {
	n, err := migrations.Current(ctx, s.DB.Pool)
	if err != nil || n != migrations.Version {
		return errors.New("database or schema unavailable")
	}
	if s.Provider == nil || s.Investigation == nil || s.Config.PayPalWebhookID == "" {
		return errors.New("dependencies unconfigured")
	}
	var healthy bool
	if err = s.DB.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM worker_heartbeats WHERE seen_at>$1)`, s.Clock.Now().Add(-90*time.Second)).Scan(&healthy); err != nil || !healthy {
		return errors.New("worker unavailable")
	}
	if err = s.Investigation.Ready(ctx); err != nil {
		return err
	}
	if err = s.Investigation.CheckIndexes(ctx); err != nil {
		return err
	}
	var matched bool
	err = s.DB.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM model_releases c,model_releases e WHERE c.kind='classifier' AND e.kind='encoder' AND c.artifact_hash=$1 AND e.artifact_hash=$2 AND c.approved_at IS NOT NULL AND e.approved_at IS NOT NULL)`, s.Investigation.Classifier.ArtifactHash, s.Investigation.Encoder.ArtifactHash).Scan(&matched)
	if err != nil || !matched {
		return errors.New("release approval unavailable")
	}
	return nil
}
