package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/ramst/pause/internal/domain"
	"github.com/ramst/pause/internal/secure"
)

// Seed is an explicit development/test operation, never a startup side effect.
// A rerun preserves existing passwords and identities.
func (s *Store) Seed(ctx context.Context, environment, password string, clock domain.Clock) error {
	if environment != "development" && environment != "demo" && environment != "test" {
		return errors.New("seed is disabled in this environment")
	}
	hash, e := secure.PasswordHash(password)
	if e != nil {
		return e
	}
	return s.WithTx(ctx, func(tx pgx.Tx) error {
		for h := 1; h <= 2; h++ {
			household := SeedID(fmt.Sprintf("household-%d", h))
			if _, e := tx.Exec(ctx, `INSERT INTO households(id,name) VALUES($1,$2) ON CONFLICT(id) DO NOTHING`, household, fmt.Sprintf("Synthetic household %d", h)); e != nil {
				return e
			}
			for _, role := range []string{"senior", "subject", "reviewer"} {
				key := fmt.Sprintf("%s-%d", role, h)
				user := SeedID(key)
				email := fmt.Sprintf("%s%d@example.test", role, h)
				if _, e := tx.Exec(ctx, `INSERT INTO users(id,email,display_name,password_hash,created_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(id) DO NOTHING`, user, email, fmt.Sprintf("Synthetic %s %d", role, h), hash, clock.Now()); e != nil {
					return e
				}
				if _, e := tx.Exec(ctx, `INSERT INTO memberships(id,household_id,user_id,role) VALUES($1,$2,$3,$4) ON CONFLICT(id) DO NOTHING`, SeedID("member-"+key), household, user, role); e != nil {
					return e
				}
				if role == "subject" {
					if _, e := tx.Exec(ctx, `INSERT INTO contacts(id,household_id,user_id,relationship,enrolled_at,provenance) VALUES($1,$2,$3,'Synthetic relative',$4,'EXPLICIT_DEVELOPMENT_SEED') ON CONFLICT(id) DO NOTHING`, SeedID("contact-"+key), household, user, clock.Now()); e != nil {
						return e
					}
				}
			}
		}
		return nil
	})
}
func SeedID(name string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("https://pause.example.test/seed/"+name)).String()
}
