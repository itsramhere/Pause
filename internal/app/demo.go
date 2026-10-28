package app

import (
	"context"
	"crypto/subtle"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/ramst/pause/internal/domain"
	"github.com/ramst/pause/internal/secure"
)

type DemoAccount struct {
	Role     string `json:"role"`
	Email    string `json:"email"`
	Password string `json:"password"`
}
type DemoRun struct {
	ID         string        `json:"id"`
	Scenario   string        `json:"scenario"`
	Mode       string        `json:"mode"`
	ExpiresAt  time.Time     `json:"expires_at"`
	ResetToken string        `json:"reset_token"`
	Accounts   []DemoAccount `json:"accounts"`
}

// LaunchDemo is opt-in, sandbox-only, and uses the normal membership/intake paths.
// Secrets are returned once; only hashes are retained. No sessions are issued.
func (s *Service) LaunchDemo(ctx context.Context, scenario, peer string) (DemoRun, error) {
	out := DemoRun{ID: uuid.NewString(), Scenario: scenario, Mode: "PAYPAL_SANDBOX", ExpiresAt: s.Clock.Now().Add(2 * time.Hour)}
	if !s.Config.DemoEnabled || s.Config.Environment == "production" {
		return out, domain.Fail("DEMO_DISABLED", 404)
	}
	if scenario != "stopped" && scenario != "positive" {
		return out, domain.Fail("DEMO_SCENARIO_INVALID", 422)
	}
	// Commit admissions separately, including failed launches, before password work.
	limited := false
	err := s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		for _, b := range []struct {
			key   string
			limit int
		}{{"global", 6}, {"peer:" + peer, 2}} {
			var n int
			if e := tx.QueryRow(ctx, `INSERT INTO demo_launch_buckets(bucket_hash,attempts,window_start) VALUES($1,1,$2) ON CONFLICT(bucket_hash) DO UPDATE SET attempts=CASE WHEN demo_launch_buckets.window_start<=$3 THEN 1 ELSE LEAST(demo_launch_buckets.attempts+1,100) END,window_start=CASE WHEN demo_launch_buckets.window_start<=$3 THEN $2 ELSE demo_launch_buckets.window_start END RETURNING attempts`, domain.Hash([]byte(b.key)), s.Clock.Now(), s.Clock.Now().Add(-time.Hour)).Scan(&n); e != nil {
				return e
			}
			limited = limited || n > b.limit
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	if limited {
		return out, domain.Fail("DEMO_THROTTLED", 429)
	}
	out.ResetToken, err = secure.Token()
	if err != nil {
		return out, err
	}
	household, senior, subject, reviewer, contact := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	users := []string{senior, subject, reviewer}
	hashes := []string{}
	for _, role := range []string{"senior", "subject", "reviewer"} {
		password, e := secure.Token()
		if e != nil {
			return out, e
		}
		hash, e := secure.PasswordHash(password)
		if e != nil {
			return out, e
		}
		hashes = append(hashes, hash)
		out.Accounts = append(out.Accounts, DemoAccount{role, role + "-" + out.ID + "@example.test", password})
	}
	err = s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, `INSERT INTO households(id,name) VALUES($1,$2)`, household, "Synthetic judge run "+out.ID); e != nil {
			return e
		}
		for n, a := range out.Accounts {
			if _, e := tx.Exec(ctx, `INSERT INTO users(id,email,display_name,password_hash,created_at) VALUES($1,$2,$3,$4,$5)`, users[n], a.Email, "Synthetic "+a.Role, hashes[n], s.Clock.Now()); e != nil {
				return e
			}
			if _, e := tx.Exec(ctx, `INSERT INTO memberships(id,household_id,user_id,role) VALUES($1,$2,$3,$4)`, uuid.NewString(), household, users[n], a.Role); e != nil {
				return e
			}
		}
		if _, e := tx.Exec(ctx, `INSERT INTO contacts(id,household_id,user_id,relationship,enrolled_at,provenance) VALUES($1,$2,$3,'Synthetic relative',$4,'EXPLICIT_DEMO_RUN')`, contact, household, subject, s.Clock.Now()); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `INSERT INTO demo_runs(id,household_id,scenario,reset_hash,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6)`, out.ID, household, scenario, domain.Hash([]byte(out.ResetToken)), s.Clock.Now(), out.ExpiresAt)
		return e
	})
	if err != nil {
		return out, err
	}
	// Honest historical fixtures, no provider operations or fabricated receipts.
	for n, amount := range []string{"40.00", "180.00"} {
		copyService := *s
		copyService.Clock = scenarioClock{s.Clock.Now().Add(-time.Duration(4-n*2) * 24 * time.Hour)}
		yes := true
		i, _, e := copyService.Create(ctx, domain.Actor{UserID: senior, HouseholdID: household, Role: domain.Senior}, Intake{Amount: amount, Currency: "USD", MerchantID: s.Config.MerchantID, SubjectContactID: contact, Purpose: []string{"Transport purchase", "Medication purchase"}[n], Message: []string{"Can you help with the ride to the clinic?", "Please help with the pharmacy purchase after the clinic visit."}[n], HistoryConsent: &yes}, fmt.Sprintf("demo-history-%d", n), "explicit-demo-seed")
		if e != nil {
			return out, e
		}
		e = s.DB.WithTx(ctx, func(tx pgx.Tx) error {
			if _, e := tx.Exec(ctx, `SELECT id FROM households WHERE id=$1 FOR UPDATE`, household); e != nil {
				return e
			}
			if _, e := tx.Exec(ctx, `INSERT INTO synthetic_history(intent_id,scenario,status,scenario_clock) VALUES($1,$2,'SEEDED_CAPTURED',$3)`, i.ID, scenario, s.Clock.Now()); e != nil {
				return e
			}
			if e := s.project(ctx, tx, i.ID, i.Version, i.Consent.Version, true, i.MessageExpiresAt); e != nil {
				return e
			}
			return s.invalidate(ctx, tx, household)
		})
		if e != nil {
			return out, e
		}
	}
	return out, nil
}

// Reset is a cleanup capability for this run only. It preserves all commands,
// requests cancellation, and waits for terminal states AND resolved operations.
func (s *Service) ResetDemo(ctx context.Context, id, token string) (string, error) {
	if !s.Config.DemoEnabled || s.Config.Environment == "production" {
		return "", domain.Fail("DEMO_DISABLED", 404)
	}
	if _, e := uuid.Parse(id); e != nil {
		return "", domain.Fail("NOT_FOUND", 404)
	}
	var household, hash string
	if e := s.DB.Pool.QueryRow(ctx, `SELECT household_id::text,reset_hash FROM demo_runs WHERE id=$1`, id).Scan(&household, &hash); e != nil {
		return "", domain.Fail("NOT_FOUND", 404)
	}
	got := domain.Hash([]byte(token))
	if len(token) != 43 || subtle.ConstantTimeCompare([]byte(got), []byte(hash)) != 1 {
		return "", domain.Fail("NOT_FOUND", 404)
	}
	e := s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, `SELECT id FROM households WHERE id=$1 FOR UPDATE`, household); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `UPDATE demo_runs SET resetting=true WHERE id=$1`, id)
		return e
	})
	if e != nil {
		return "", e
	}
	rows, e := s.DB.Pool.Query(ctx, `SELECT id::text FROM payment_intents WHERE household_id=$1 AND payment_status NOT IN ('CAPTURED','VOIDED','CLOSED_UNCAPTURED','ABANDONED')`, household)
	if e != nil {
		return "", e
	}
	ids := []string{}
	for rows.Next() {
		var i string
		if e = rows.Scan(&i); e != nil {
			rows.Close()
			return "", e
		}
		ids = append(ids, i)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return "", e
	}
	for _, i := range ids {
		e = s.DB.WithTx(ctx, func(tx pgx.Tx) error {
			p, e := s.paymentLock(ctx, tx, i)
			if e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, `UPDATE payment_intents SET cleanup_requested=true WHERE id=$1`, i); e != nil {
				return e
			}
			p.cleanup = true
			if p.state == domain.Draft {
				return s.state(ctx, tx, &p, domain.Abandoned, "DEMO_RESET")
			}
			if p.state == domain.AwaitingPayer {
				if e = s.state(ctx, tx, &p, domain.Reconciling, "DEMO_RESET"); e != nil {
					return e
				}
				return s.queue(ctx, tx, "PAYMENT_RECONCILE", i, "reconcile:"+i)
			}
			return s.reserveVoid(ctx, tx, &p, "DEMO_RESET")
		})
		if e != nil {
			return "", e
		}
	}
	var pending bool
	e = s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, `SELECT id FROM households WHERE id=$1 FOR UPDATE`, household); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_intents WHERE household_id=$1 AND payment_status NOT IN ('CAPTURED','VOIDED','CLOSED_UNCAPTURED','ABANDONED')) OR EXISTS(SELECT 1 FROM payment_operations o JOIN payment_intents i ON i.id=o.intent_id WHERE i.household_id=$1 AND o.state IN ('RESERVED','DISPATCHED','UNKNOWN'))`, household).Scan(&pending); e != nil {
			return e
		}
		if !pending {
			_, e := tx.Exec(ctx, `UPDATE demo_runs SET closed_at=COALESCE(closed_at,$2) WHERE id=$1`, id, s.Clock.Now())
			return e
		}
		return nil
	})
	if e != nil {
		return "", e
	}
	if pending {
		return "CLEANUP_PENDING", nil
	}
	return "CLOSED", nil
}

func (s *Service) demoCheckoutLimit(ctx context.Context, tx pgx.Tx, p paymentRow) error {
	var exists, blocked bool
	if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM demo_runs WHERE household_id=$1),EXISTS(SELECT 1 FROM demo_runs WHERE household_id=$1 AND (resetting OR expires_at<=$2))`, p.household, s.Clock.Now()).Scan(&exists, &blocked); e != nil {
		return e
	}
	if !exists {
		return nil
	}
	if blocked {
		return domain.Fail("DEMO_CLOSED", 409)
	}
	var count int
	var total int64
	if e := tx.QueryRow(ctx, `SELECT count(*),COALESCE(sum(v.amount_minor),0) FROM payment_operations o JOIN payment_intents i ON i.id=o.intent_id JOIN intent_versions v ON v.intent_id=o.intent_id AND v.version=o.intent_version WHERE i.household_id=$1 AND o.kind='CREATE_ORDER' AND o.intent_id<>$2`, p.household, p.cmd.IntentID).Scan(&count, &total); e != nil {
		return e
	}
	if count >= 2 || total+p.cmd.Binding.AmountMinor > 100000 {
		return domain.Fail("DEMO_PAYMENT_LIMIT", 429)
	}
	return nil
}
