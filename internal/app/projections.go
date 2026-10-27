package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/ramst/pause/internal/domain"
)

// Keep projection metadata and its durable job inside the canonical write tx.
// P04 will dispatch these jobs; P01 never marks an unperformed lookup/index done.
func (s *Service) project(ctx context.Context, tx pgx.Tx, id string, version, consent int, allowed bool, expires time.Time) error {
	action := "DELETE"
	if allowed {
		action = "UPSERT"
	}
	var revision int
	e := tx.QueryRow(ctx, `INSERT INTO search_projections(intent_id,revision,consent_version,intent_version,action,expires_at) VALUES($1,1,$2,$3,$4,$5) ON CONFLICT(intent_id) DO UPDATE SET revision=search_projections.revision+1,consent_version=excluded.consent_version,intent_version=excluded.intent_version,action=excluded.action,expires_at=excluded.expires_at RETURNING revision`, id, consent, version, action, expires).Scan(&revision)
	if e != nil {
		return e
	}
	job := uuid.NewString()
	now := s.Clock.Now()
	if _, e = tx.Exec(ctx, `INSERT INTO jobs(id,kind,object_id,dedup_key,not_before) VALUES($1,'SEARCH_PROJECTION',$2,$3,$4)`, job, id, fmt.Sprintf("projection:%s:%d", id, revision), now); e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO search_outbox(id,intent_id,projection_revision,consent_version,intent_version,action,job_id,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, uuid.NewString(), id, revision, consent, version, action, job, now)
	return e
}

type HistorySource struct {
	IntentID                                    string
	Version, ConsentVersion, ProjectionRevision int
	Message                                     string
	Binding                                     domain.Binding
}

// HydrateHistory is the only future private-search source hydration path.
// No Elastic excerpt or cached string can bypass current permission checks.
func (s *Service) HydrateHistory(ctx context.Context, a domain.Actor, query domain.SearchQuery, hit domain.SearchHit) (HistorySource, error) {
	var out HistorySource
	var ciphertext []byte
	if _, e := uuid.Parse(hit.SourceID); e != nil {
		return out, domain.Fail("NOT_FOUND", 404)
	}
	if (a.Role != domain.Reviewer && a.Role != domain.Senior) || query.Validate() != nil || query.Scope != domain.History || query.HouseholdID != a.HouseholdID || hit.SourceID == query.ExcludeIntentID || hit.SourceVersion < 1 || hit.ConsentVersion < 1 || hit.ProjectionRevision < 1 {
		return out, domain.Fail("NOT_FOUND", 404)
	}
	e := s.DB.Pool.QueryRow(ctx, `SELECT i.id::text,i.current_version,i.consent_version,p.revision,v.binding_schema,v.amount_minor,v.currency,v.payee_id,v.subject_contact_id::text,v.purpose,m.ciphertext FROM payment_intents i JOIN memberships member ON member.household_id=i.household_id AND member.user_id=$2 AND member.role=$3 AND member.active JOIN intent_versions v ON v.intent_id=i.id AND v.version=i.current_version JOIN intent_consents c ON c.intent_id=i.id AND c.version=i.consent_version JOIN request_messages m ON m.intent_id=i.id AND m.version=i.current_version JOIN search_projections p ON p.intent_id=i.id
 JOIN payment_intents current_request ON current_request.id=$9 AND current_request.household_id=i.household_id
 JOIN intent_versions current_binding ON current_binding.intent_id=current_request.id AND current_binding.version=current_request.current_version AND current_binding.subject_contact_id=$11
 JOIN intent_consents current_consent ON current_consent.intent_id=current_request.id AND current_consent.version=current_request.consent_version
 WHERE i.id=$1 AND i.id<>current_request.id AND i.household_id=$4 AND (($3='senior' AND i.creator_id=$2 AND current_request.creator_id=$2) OR ($3='reviewer' AND i.reviewer_id=$2 AND current_request.reviewer_id=$2)) AND c.history_allowed AND current_consent.history_allowed AND current_consent.version=$10 AND m.deleted_at IS NULL AND m.expires_at>$5 AND i.current_version=$6 AND i.consent_version=$7 AND p.revision=$8 AND p.action='UPSERT' AND v.subject_contact_id=$11 AND i.created_at>=$12 AND i.created_at<=current_request.created_at`, hit.SourceID, a.UserID, a.Role, a.HouseholdID, s.Clock.Now(), hit.SourceVersion, hit.ConsentVersion, hit.ProjectionRevision, query.ExcludeIntentID, query.ConsentVersion, query.SubjectContactID, query.Cutoff).Scan(&out.IntentID, &out.Version, &out.ConsentVersion, &out.ProjectionRevision, &out.Binding.Schema, &out.Binding.AmountMinor, &out.Binding.Currency, &out.Binding.PayeeID, &out.Binding.SubjectContactID, &out.Binding.Purpose, &ciphertext)
	if errors.Is(e, pgx.ErrNoRows) {
		return out, domain.Fail("NOT_FOUND", 404)
	}
	if e != nil {
		return out, e
	}
	out.Binding.IntentID = out.IntentID
	out.Binding.Version = out.Version
	out.Message, e = s.Box.Decrypt(ciphertext, fmt.Sprintf("%s:%d", out.IntentID, out.Version))
	return out, e
}

// CleanupMessages removes raw ciphertext and invalidates derived content before
// queuing deletion. Access checks already enforce expires_at even if this stops.
func (s *Service) CleanupMessages(ctx context.Context) error {
	rows, e := s.DB.Pool.Query(ctx, `SELECT DISTINCT i.household_id::text FROM request_messages m JOIN payment_intents i ON i.id=m.intent_id WHERE m.deleted_at IS NULL AND m.expires_at<=$1 LIMIT 100`, s.Clock.Now())
	if e != nil {
		return e
	}
	households := []string{}
	for rows.Next() {
		var h string
		if e = rows.Scan(&h); e != nil {
			rows.Close()
			return e
		}
		households = append(households, h)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, h := range households {
		e = s.DB.WithTx(ctx, func(tx pgx.Tx) error {
			if _, e := tx.Exec(ctx, `SELECT id FROM households WHERE id=$1 FOR UPDATE`, h); e != nil {
				return e
			}
			r, e := tx.Query(ctx, `UPDATE request_messages m SET ciphertext=NULL,deleted_at=$2 FROM payment_intents i WHERE m.intent_id=i.id AND i.household_id=$1 AND m.deleted_at IS NULL AND m.expires_at<=$2 RETURNING m.intent_id::text,m.version,i.current_version,i.consent_version,m.expires_at`, h, s.Clock.Now())
			if e != nil {
				return e
			}
			type expired struct {
				id            string
				v, current, c int
				t             time.Time
			}
			items := []expired{}
			for r.Next() {
				var x expired
				if e = r.Scan(&x.id, &x.v, &x.current, &x.c, &x.t); e != nil {
					r.Close()
					return e
				}
				items = append(items, x)
			}
			e = r.Err()
			r.Close()
			if e != nil {
				return e
			}
			for _, x := range items {
				if x.v == x.current {
					if e = s.project(ctx, tx, x.id, x.current, x.c, false, x.t); e != nil {
						return e
					}
				}
			}
			if len(items) > 0 {
				return s.invalidate(ctx, tx, h)
			}
			return nil
		})
		if e != nil {
			return e
		}
	}
	return nil
}
