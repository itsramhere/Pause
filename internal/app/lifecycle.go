package app

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/ramst/pause/internal/domain"
)

// Lifecycle owns all payment-state UPDATE statements. P01 exposes only local
// abandonment before any order exists. P02 must add provider/policy-gated
// methods here rather than introducing a client-settable status endpoint.
type Lifecycle struct{ Service *Service }

func (l Lifecycle) Abandon(ctx context.Context, a domain.Actor, id string, expected int, correlation string) error {
	parsed, parseErr := uuid.Parse(id)
	if parseErr != nil {
		return domain.Fail("NOT_FOUND", 404)
	}
	id = parsed.String()
	s := l.Service
	return s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		if e := s.active(ctx, tx, a, domain.Senior); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `SELECT id FROM households WHERE id=$1 FOR UPDATE`, a.HouseholdID); e != nil {
			return e
		}
		var from domain.PaymentStatus
		var version, consent int
		var order *string
		var history bool
		var expires time.Time
		e := tx.QueryRow(ctx, `SELECT i.payment_status,i.current_version,i.provider_order_id,i.consent_version,c.history_allowed,m.expires_at FROM payment_intents i JOIN intent_consents c ON c.intent_id=i.id AND c.version=i.consent_version JOIN request_messages m ON m.intent_id=i.id AND m.version=i.current_version WHERE i.id=$1 AND i.household_id=$2 AND i.creator_id=$3 FOR UPDATE OF i`, id, a.HouseholdID, a.UserID).Scan(&from, &version, &order, &consent, &history, &expires)
		if errors.Is(e, pgx.ErrNoRows) {
			return domain.Fail("NOT_FOUND", 404)
		}
		if e != nil {
			return e
		}
		if version != expected {
			return domain.Fail("VERSION_CONFLICT", 409)
		}
		if from != domain.Draft || order != nil || !domain.CanTransition(from, domain.Abandoned) {
			return domain.Fail("INVALID_TRANSITION", 409)
		}
		if _, e = tx.Exec(ctx, `UPDATE payment_intents SET payment_status='ABANDONED' WHERE id=$1`, id); e != nil {
			return e
		}
		if e = s.invalidate(ctx, tx, a.HouseholdID); e != nil {
			return e
		}
		if e = s.project(ctx, tx, id, version, consent, history && expires.After(s.Clock.Now()), expires); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO request_events(id,intent_id,intent_version,kind,source,occurred_at) VALUES($1,$2,$3,'PAYMENT_STATE','APPLICATION',$4)`, uuid.NewString(), id, version, s.Clock.Now()); e != nil {
			return e
		}
		return s.audit(ctx, tx, a, id, "PAYMENT_TRANSITION", "LOCALLY_ABANDONED", correlation)
	})
}
