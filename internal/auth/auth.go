package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/ramst/pause/internal/domain"
	"github.com/ramst/pause/internal/secure"
	"github.com/ramst/pause/internal/store"
)

type Service struct {
	DB        *store.Store
	Clock     domain.Clock
	TTL       time.Duration
	dummyHash string
}
type Session struct {
	Actor    domain.Actor
	CSRFHash string
}
type LoginResult struct {
	Actor     domain.Actor `json:"actor"`
	CSRFToken string       `json:"csrf_token"`
	Token     string       `json:"-"`
}

func New(db *store.Store, clock domain.Clock, ttl time.Duration) (*Service, error) {
	h, e := secure.PasswordHash("synthetic-unknown-account-password")
	return &Service{db, clock, ttl, h}, e
}
func (s *Service) Login(ctx context.Context, email, password, ip, oldToken, correlation string) (LoginResult, error) {
	var out LoginResult
	email = strings.ToLower(strings.TrimSpace(email))
	if len(email) > 254 || len(password) > 256 {
		return out, domain.Fail("INVALID_CREDENTIALS", 401)
	}
	// Persistent account and peer buckets work across restarts/processes; ignored
	// proxy headers cannot create fresh peer identities. Admit before hashing.
	now := s.Clock.Now()
	e := s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		for _, key := range []string{"account:" + email, "peer:" + ip} {
			var n int
			e := tx.QueryRow(ctx, `INSERT INTO login_buckets(bucket_hash,attempts,window_start) VALUES($1,1,$2) ON CONFLICT(bucket_hash) DO UPDATE SET attempts=CASE WHEN login_buckets.window_start<=$3 THEN 1 ELSE login_buckets.attempts+1 END,window_start=CASE WHEN login_buckets.window_start<=$3 THEN $2 ELSE login_buckets.window_start END RETURNING attempts`, domain.Hash([]byte(key)), now, now.Add(-15*time.Minute)).Scan(&n)
			if e != nil {
				return e
			}
			limit := 10
			if strings.HasPrefix(key, "peer:") {
				limit = 50
			}
			if n > limit {
				return domain.Fail("LOGIN_THROTTLED", 429)
			}
		}
		return nil
	})
	if e != nil {
		return out, e
	}
	var hash, member string
	var a domain.Actor
	e = s.DB.Pool.QueryRow(ctx, `SELECT u.id::text,u.display_name,u.password_hash,m.id::text,m.household_id::text,m.role FROM users u JOIN memberships m ON m.user_id=u.id AND m.active WHERE u.email=$1 ORDER BY m.id LIMIT 1`, email).Scan(&a.UserID, &a.Name, &hash, &member, &a.HouseholdID, &a.Role)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return out, e
	}
	missing := errors.Is(e, pgx.ErrNoRows)
	if missing {
		hash = s.dummyHash
	}
	valid := secure.VerifyPassword(hash, password)
	if !valid || missing {
		return out, domain.Fail("INVALID_CREDENTIALS", 401)
	}
	token, e := secure.Token()
	if e != nil {
		return out, e
	}
	csrf, e := secure.Token()
	if e != nil {
		return out, e
	}
	e = s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		if oldToken != "" {
			if _, e := tx.Exec(ctx, `DELETE FROM sessions WHERE token_hash=$1`, domain.Hash([]byte(oldToken))); e != nil {
				return e
			}
		}
		if _, e := tx.Exec(ctx, `INSERT INTO sessions(token_hash,csrf_hash,membership_id,created_at,expires_at) VALUES($1,$2,$3,$4,$5)`, domain.Hash([]byte(token)), domain.Hash([]byte(csrf)), member, now, now.Add(s.TTL)); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `INSERT INTO audit_events(id,household_id,actor_id,action,object_id,reason_code,correlation_id,occurred_at) VALUES($1,$2,$3,'SESSION_LOGIN',$3,'AUTHENTICATED',$4,$5)`, uuid.NewString(), a.HouseholdID, a.UserID, correlation, now)
		return e
	})
	if e != nil {
		return out, e
	}
	return LoginResult{a, csrf, token}, nil
}
func (s *Service) Resolve(ctx context.Context, token string) (Session, error) {
	var out Session
	if len(token) != 43 {
		return out, domain.Fail("UNAUTHENTICATED", 401)
	}
	e := s.DB.Pool.QueryRow(ctx, `SELECT u.id::text,u.display_name,m.household_id::text,m.role,s.csrf_hash FROM sessions s JOIN memberships m ON m.id=s.membership_id AND m.active JOIN users u ON u.id=m.user_id WHERE s.token_hash=$1 AND s.expires_at>$2`, domain.Hash([]byte(token)), s.Clock.Now()).Scan(&out.Actor.UserID, &out.Actor.Name, &out.Actor.HouseholdID, &out.Actor.Role, &out.CSRFHash)
	if errors.Is(e, pgx.ErrNoRows) {
		return out, domain.Fail("UNAUTHENTICATED", 401)
	}
	return out, e
}
func (s *Service) RotateCSRF(ctx context.Context, token string) (string, error) {
	csrf, e := secure.Token()
	if e != nil {
		return "", e
	}
	r, e := s.DB.Pool.Exec(ctx, `UPDATE sessions SET csrf_hash=$2 WHERE token_hash=$1 AND expires_at>$3`, domain.Hash([]byte(token)), domain.Hash([]byte(csrf)), s.Clock.Now())
	if e == nil && r.RowsAffected() != 1 {
		return "", domain.Fail("UNAUTHENTICATED", 401)
	}
	return csrf, e
}
func (s *Service) Logout(ctx context.Context, token, correlation string) error {
	return s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		var h, u string
		e := tx.QueryRow(ctx, `DELETE FROM sessions s USING memberships m WHERE s.token_hash=$1 AND s.membership_id=m.id RETURNING m.household_id::text,m.user_id::text`, domain.Hash([]byte(token))).Scan(&h, &u)
		if errors.Is(e, pgx.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `INSERT INTO audit_events(id,household_id,actor_id,action,reason_code,correlation_id,occurred_at) VALUES($1,$2,$3,'SESSION_LOGOUT','SIGNED_OUT',$4,$5)`, uuid.NewString(), h, u, correlation, s.Clock.Now())
		return e
	})
}
