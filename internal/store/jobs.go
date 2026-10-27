package store

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/ramst/pause/internal/domain"
)

type Jobs struct {
	DB    *Store
	Clock domain.Clock
}

var _ domain.JobStore = (*Jobs)(nil)

func (j *Jobs) Lease(ctx context.Context, kinds []string, duration time.Duration) (*domain.Job, error) {
	if len(kinds) == 0 || duration < time.Second || duration > 5*time.Minute {
		return nil, errors.New("invalid lease")
	}
	now := j.Clock.Now()
	token := uuid.NewString()
	out := &domain.Job{}
	// SKIP LOCKED plus a fresh token fences a late worker after lease replacement.
	e := j.DB.Pool.QueryRow(ctx, `WITH candidate AS (SELECT id FROM jobs WHERE kind=ANY($1) AND not_before<=$2 AND completed_at IS NULL AND failed_at IS NULL AND attempts<max_attempts AND (lease_until IS NULL OR lease_until<=$2) ORDER BY not_before,id FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE jobs j SET attempts=j.attempts+1,lease_token=$3,lease_until=$4 FROM candidate c WHERE j.id=c.id RETURNING j.id::text,j.kind,j.object_id::text,j.attempts,j.lease_token::text,j.lease_until`, kinds, now, token, now.Add(duration)).Scan(&out.ID, &out.Kind, &out.ObjectID, &out.Attempts, &out.LeaseToken, &out.LeaseUntil)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, nil
	}
	return out, e
}
func (j *Jobs) Complete(ctx context.Context, job domain.Job) error {
	r, e := j.DB.Pool.Exec(ctx, `UPDATE jobs SET completed_at=$3,lease_until=NULL,lease_token=NULL WHERE id=$1 AND lease_token=$2 AND lease_until>$3 AND completed_at IS NULL AND failed_at IS NULL`, job.ID, job.LeaseToken, j.Clock.Now())
	if e == nil && r.RowsAffected() != 1 {
		return domain.Fail("LEASE_CONFLICT", 409)
	}
	return e
}

var reasonPattern = regexp.MustCompile(`^[A-Z_]{1,64}$`)

func (j *Jobs) Retry(ctx context.Context, job domain.Job, reason string, backoff time.Duration) error {
	if !reasonPattern.MatchString(reason) || backoff < 0 || backoff > time.Hour {
		return errors.New("invalid retry")
	}
	now := j.Clock.Now()
	r, e := j.DB.Pool.Exec(ctx, `UPDATE jobs SET not_before=$3,last_error_code=$4,lease_until=NULL,lease_token=NULL,failed_at=CASE WHEN attempts>=max_attempts THEN $5 ELSE NULL END WHERE id=$1 AND lease_token=$2 AND lease_until>$5 AND completed_at IS NULL AND failed_at IS NULL`, job.ID, job.LeaseToken, now.Add(backoff), reason, now)
	if e == nil && r.RowsAffected() != 1 {
		return domain.Fail("LEASE_CONFLICT", 409)
	}
	return e
}
func (j *Jobs) ReapExhausted(ctx context.Context) error {
	_, e := j.DB.Pool.Exec(ctx, `UPDATE jobs SET failed_at=$1,lease_until=NULL,lease_token=NULL,last_error_code='LEASE_EXHAUSTED' WHERE completed_at IS NULL AND failed_at IS NULL AND attempts>=max_attempts AND (lease_until IS NULL OR lease_until<=$1)`, j.Clock.Now())
	return e
}
