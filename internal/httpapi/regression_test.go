package httpapi_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/ramst/pause/internal/app"
	"github.com/ramst/pause/internal/domain"
)

func TestCanonicalIntentPathsPostgres(t *testing.T) {
	f := newFixture(t)
	c := f.newClient("127.0.0.40")
	c.login(t, "senior1@example.test")
	created := c.request("POST", "/api/intents", intake("40", true), map[string]string{"Idempotency-Key": "canonical-paths-01"})
	status(t, created, 201)
	i := readIntent(t, created)
	for _, id := range []string{strings.ToUpper(i.ID), "{" + i.ID + "}", "urn:uuid:" + i.ID} {
		t.Run(id, func(t *testing.T) {
			status(t, c.request("GET", "/api/intents/"+id, nil, nil), 200)
			out := c.request("GET", "/api/intents/"+id+"/message", nil, nil)
			status(t, out, 200)
			if !strings.Contains(out.Body.String(), "SYNTHETIC_PRIVATE_MARKER") {
				t.Fatal("canonical message missing")
			}
		})
	}
	status(t, c.request("POST", "/api/intents/"+strings.ToUpper(i.ID)+"/corrections", app.Correction{ExpectedVersion: 1, ExpectedConsentVersion: 1, Intake: intake("45.01", true)}, nil), 200)
	status(t, c.request("GET", "/api/intents/"+i.ID+"/message", nil, nil), 200)
	status(t, c.request("PUT", "/api/intents/urn:uuid:"+i.ID+"/consent", map[string]any{"expected_version": 1, "history": false}, nil), 200)
	status(t, c.request("POST", "/api/intents/urn:uuid:"+i.ID+"/abandon", map[string]int{"expected_version": 2}, nil), 200)
}

func TestInitialStateAndProviderBindingConstraintsPostgres(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	i, _, e := f.svc.Create(ctx, f.actor("senior", 1), intake("40", true), "binding-guards-01", "synthetic-test")
	if e != nil {
		t.Fatal(e)
	}
	for _, state := range []string{"AWAITING_PAYER", "HELD", "CAPTURE_PENDING", "VOID_PENDING", "RECONCILING", "CAPTURED", "VOIDED", "CLOSED_UNCAPTURED", "ABANDONED"} {
		t.Run("insert_"+state, func(t *testing.T) {
			tx, e := f.db.Pool.Begin(ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback(ctx)
			// Deferred version FKs intentionally have not fired yet. The initial
			// lifecycle state itself must be rejected at INSERT time.
			_, e = tx.Exec(ctx, `INSERT INTO payment_intents(id,household_id,creator_id,reviewer_id,current_version,consent_version,payment_status,created_at,decision_deadline) SELECT $1,household_id,creator_id,reviewer_id,1,1,$2,created_at,decision_deadline FROM payment_intents WHERE id=$3`, uuid.NewString(), state, i.ID)
			var constraint *pgconn.PgError
			if !errors.As(e, &constraint) || constraint.Code != "23514" {
				t.Fatal("expected initial-state check violation", e)
			}
		})
	}
	if _, e = f.db.Pool.Exec(ctx, `UPDATE payment_intents SET provider_order_id='synthetic-bound-order' WHERE id=$1`, i.ID); e != nil {
		t.Fatal(e)
	}
	for _, value := range []any{nil, "synthetic-replacement-order"} {
		_, e = f.db.Pool.Exec(ctx, `UPDATE payment_intents SET provider_order_id=$2 WHERE id=$1`, i.ID, value)
		var constraint *pgconn.PgError
		if !errors.As(e, &constraint) || constraint.Code != "23514" {
			t.Error("expected immutable-provider check violation", e)
		}
	}
	if _, e = f.db.Pool.Exec(ctx, `UPDATE payment_intents SET authorization_id='synthetic-bound-auth' WHERE id=$1`, i.ID); e != nil {
		t.Fatal(e)
	}
	for _, value := range []any{nil, "synthetic-replacement-auth"} {
		_, e = f.db.Pool.Exec(ctx, `UPDATE payment_intents SET authorization_id=$2 WHERE id=$1`, i.ID, value)
		var constraint *pgconn.PgError
		if !errors.As(e, &constraint) || constraint.Code != "23514" {
			t.Error("expected immutable-authorization check violation", e)
		}
	}
}

func TestPaymentTransitionParityPostgres(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	i, _, e := f.svc.Create(ctx, f.actor("senior", 1), intake("40", true), "transition-parity-01", "synthetic-test")
	if e != nil {
		t.Fatal(e)
	}
	paths := map[domain.PaymentStatus][]domain.PaymentStatus{
		domain.Draft:            {},
		domain.AwaitingPayer:    {domain.AwaitingPayer},
		domain.Held:             {domain.AwaitingPayer, domain.Held},
		domain.CapturePending:   {domain.AwaitingPayer, domain.Held, domain.CapturePending},
		domain.VoidPending:      {domain.AwaitingPayer, domain.Held, domain.VoidPending},
		domain.Reconciling:      {domain.AwaitingPayer, domain.Reconciling},
		domain.Captured:         {domain.AwaitingPayer, domain.Held, domain.CapturePending, domain.Captured},
		domain.Voided:           {domain.AwaitingPayer, domain.Held, domain.VoidPending, domain.Voided},
		domain.ClosedUncaptured: {domain.AwaitingPayer, domain.ClosedUncaptured},
		domain.Abandoned:        {domain.Abandoned},
	}
	for from, path := range paths {
		for to := range paths {
			if from == to {
				continue
			}
			t.Run(string(from)+"_to_"+string(to), func(t *testing.T) {
				tx, e := f.db.Pool.Begin(ctx)
				if e != nil {
					t.Fatal(e)
				}
				defer tx.Rollback(ctx)
				if _, e = tx.Exec(ctx, `UPDATE payment_intents SET provider_order_id='synthetic-order',authorization_id='synthetic-auth' WHERE id=$1`, i.ID); e != nil {
					t.Fatal(e)
				}
				for _, step := range path {
					if _, e = tx.Exec(ctx, `UPDATE payment_intents SET payment_status=$2 WHERE id=$1`, i.ID, step); e != nil {
						t.Fatal(e)
					}
				}
				_, e = tx.Exec(ctx, `UPDATE payment_intents SET payment_status=$2 WHERE id=$1`, i.ID, to)
				if domain.CanTransition(from, to) {
					if e != nil {
						t.Fatal("database rejected a service transition", e)
					}
				} else {
					var constraint *pgconn.PgError
					if !errors.As(e, &constraint) || constraint.Code != "23514" {
						t.Fatal("expected transition check violation", e)
					}
				}
			})
		}
	}
}

func TestCrossHouseholdMutationScopesPostgres(t *testing.T) {
	f := newFixture(t)
	owner := f.newClient("127.0.0.50")
	owner.login(t, "senior1@example.test")
	out := owner.request("POST", "/api/intents", intake("40", true), map[string]string{"Idempotency-Key": "scope-mutations-01"})
	status(t, out, 201)
	i := readIntent(t, out)
	for n, email := range []string{"senior2@example.test", "reviewer2@example.test", "subject2@example.test"} {
		t.Run(email, func(t *testing.T) {
			c := f.newClient(fmt.Sprintf("127.0.0.%d", 60+n))
			c.login(t, email)
			for _, suffix := range []string{"", "/message"} {
				out := c.request("GET", "/api/intents/"+i.ID+suffix, nil, nil)
				status(t, out, 404)
				code(t, out, "NOT_FOUND")
			}
			want := 403
			if strings.HasPrefix(email, "senior") {
				want = 404
			}
			status(t, c.request("POST", "/api/intents/"+i.ID+"/corrections", app.Correction{ExpectedVersion: 1, ExpectedConsentVersion: 1, Intake: intake("41", false)}, nil), want)
			status(t, c.request("PUT", "/api/intents/"+i.ID+"/consent", map[string]any{"expected_version": 1, "history": false}, nil), want)
			status(t, c.request("POST", "/api/intents/"+i.ID+"/abandon", map[string]int{"expected_version": 1}, nil), want)
		})
	}
	out = owner.request("GET", "/api/intents/"+i.ID, nil, nil)
	status(t, out, 200)
	if current := readIntent(t, out); current.Version != 1 || !current.Consent.History || current.Payment != domain.Draft {
		t.Fatal("unauthorized mutation changed the intent")
	}
}

func TestCorrectionCannotRestoreWithdrawnConsentPostgres(t *testing.T) {
	f := newFixture(t)
	c := f.newClient("127.0.0.70")
	c.login(t, "senior1@example.test")
	out := c.request("POST", "/api/intents", intake("40", true), map[string]string{"Idempotency-Key": "stale-correction-consent"})
	status(t, out, 201)
	i := readIntent(t, out)
	status(t, c.request("PUT", "/api/intents/"+i.ID+"/consent", map[string]any{"expected_version": 1, "history": false}, nil), 200)
	// A correction opened before withdrawal must not restore its stale opt-in.
	for _, revision := range []int{0, 1} {
		out = c.request("POST", "/api/intents/"+i.ID+"/corrections", app.Correction{ExpectedVersion: 1, ExpectedConsentVersion: revision, Intake: intake("45.01", true)}, nil)
		status(t, out, 409)
		code(t, out, "CONSENT_VERSION_CONFLICT")
	}
	out = c.request("GET", "/api/intents/"+i.ID, nil, nil)
	status(t, out, 200)
	if current := readIntent(t, out); current.Version != 1 || current.Consent.History || current.Consent.Version != 2 {
		t.Fatal("stale correction overwrote withdrawal")
	}
	out = c.request("POST", "/api/intents/"+i.ID+"/corrections", app.Correction{ExpectedVersion: 1, ExpectedConsentVersion: 2, Intake: intake("45.01", false)}, nil)
	status(t, out, 200)
	if current := readIntent(t, out); current.Version != 2 || current.Consent.History || current.Consent.Version != 2 {
		t.Fatal("current correction did not preserve withdrawal")
	}
}
