package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/ramst/pause/internal/domain"
	"github.com/ramst/pause/internal/secure"
	"github.com/ramst/pause/internal/store"
)

type scenarioClock struct{ at time.Time }

func (c scenarioClock) Now() time.Time { return c.at }

// SeedPatterns uses dedicated accounts and honest synthetic outcomes. It never
// creates a PayPal receipt, provider identity, held status, or captured status.
func (s *Service) SeedPatterns(ctx context.Context, scenario, password string) (string, error) {
	if s.Config.Environment != "development" && s.Config.Environment != "demo" && s.Config.Environment != "test" {
		return "", errors.New("scenario seed disabled")
	}
	if scenario != "f04" && scenario != "benign" {
		return "", errors.New("unknown scenario")
	}
	hash, err := secure.PasswordHash(password)
	if err != nil {
		return "", err
	}
	household := store.SeedID("patterns-" + scenario)
	err = s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO households(id,name) VALUES($1,$2) ON CONFLICT DO NOTHING`, household, "Synthetic patterns "+scenario); err != nil {
			return err
		}
		for _, role := range []string{"senior", "reviewer", "subject"} {
			user := store.SeedID("patterns-" + scenario + "-" + role)
			if _, err := tx.Exec(ctx, `INSERT INTO users(id,email,display_name,password_hash,created_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(id) DO NOTHING`, user, scenario+"-"+role+"@example.test", "Synthetic "+scenario+" "+role, hash, s.Clock.Now()); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO memberships(id,household_id,user_id,role) VALUES($1,$2,$3,$4) ON CONFLICT(id) DO NOTHING`, store.SeedID("patterns-member-"+scenario+"-"+role), household, user, role); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `INSERT INTO contacts(id,household_id,user_id,relationship,enrolled_at,provenance) VALUES($1,$2,$3,'Synthetic relative',$4,'EXPLICIT_DEVELOPMENT_SEED') ON CONFLICT(id) DO NOTHING`, store.SeedID("patterns-contact-"+scenario), household, store.SeedID("patterns-"+scenario+"-subject"), s.Clock.Now())
		return err
	})
	if err != nil {
		return "", err
	}
	a := domain.Actor{UserID: store.SeedID("patterns-" + scenario + "-senior"), HouseholdID: household, Role: domain.Senior}
	// An existing scenario is preserved rather than quietly reseeding dates or
	// creating duplicate exposure. Use a fresh database for a new scenario clock.
	var count int
	if err = s.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM payment_intents WHERE household_id=$1`, household).Scan(&count); err != nil {
		return "", err
	}
	if count > 0 {
		return scenario + "-senior@example.test", nil
	}
	amounts := []string{"40.00", "180.00", "900.00"}
	purposes := []string{"Transport purchase", "Medication purchase", "Emergency supplies purchase"}
	texts := []string{"I need a ride to the clinic. Can you help pay for transport?", "The clinic visit needs medicine. Please help with the pharmacy purchase.", "The medical emergency needs more supplies today. Please pay for the emergency purchase."}
	if scenario == "benign" {
		amounts = []string{"40.00", "60.00", "90.00"}
		purposes = []string{"Planned transport purchase", "Planned pharmacy purchase", "Planned household supplies"}
		texts = []string{"Our scheduled clinic ride costs forty dollars. Please check with me before paying.", "Here is the planned pharmacy shopping list. Please confirm the purchase with me.", "Our planned household order is ready. Take your time and confirm the merchant purchase with me."}
	}
	now := s.Clock.Now().UTC()
	yes := true
	for n := 0; n < 3; n++ {
		copyService := *s
		at := now
		if n == 0 {
			at = now.Add(-4 * 24 * time.Hour)
		}
		if n == 1 {
			at = now.Add(-2 * 24 * time.Hour)
		}
		copyService.Clock = scenarioClock{at}
		i, _, err := copyService.Create(ctx, a, Intake{Amount: amounts[n], Currency: "USD", MerchantID: s.Config.MerchantID, SubjectContactID: store.SeedID("patterns-contact-" + scenario), Purpose: purposes[n], Message: texts[n], HistoryConsent: &yes}, fmt.Sprintf("patterns-%s-%d", scenario, n), "explicit-synthetic-pattern-seed")
		if err != nil {
			return "", err
		}
		if n < 2 {
			err = s.DB.WithTx(ctx, func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, `SELECT id FROM households WHERE id=$1 FOR UPDATE`, household); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `INSERT INTO synthetic_history(intent_id,scenario,status,scenario_clock) VALUES($1,$2,'SEEDED_CAPTURED',$3)`, i.ID, scenario, now); err != nil {
					return err
				}
				if err := s.project(ctx, tx, i.ID, i.Version, i.Consent.Version, true, i.MessageExpiresAt); err != nil {
					return err
				}
				return s.invalidate(ctx, tx, household)
			})
			if err != nil {
				return "", err
			}
		}
	}
	return scenario + "-senior@example.test", nil
}
