package app

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/ramst/pause/internal/domain"
	"github.com/ramst/pause/internal/investigation"
)

// Keep projection metadata and its durable job inside the canonical write tx.
// Private projection dispatch acknowledges only performed index writes.
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

// Standalone hydration uses the same locked canonical permissions and causal
// ordering as evidence packets. A caller's old window cannot widen access.
func (s *Service) HydrateHistory(ctx context.Context, a domain.Actor, query domain.SearchQuery, hit domain.SearchHit) (HistorySource, error) {
	var out HistorySource
	if _, e := uuid.Parse(hit.SourceID); e != nil {
		return out, domain.Fail("NOT_FOUND", 404)
	}
	if (a.Role != domain.Reviewer && a.Role != domain.Senior) || query.Validate() != nil || query.Scope != domain.History || query.HouseholdID != a.HouseholdID || hit.SourceID == query.ExcludeIntentID || hit.SourceVersion < 1 || hit.ConsentVersion < 1 || hit.ProjectionRevision < 1 {
		return out, domain.Fail("NOT_FOUND", 404)
	}
	e := s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		p, err := s.paymentLock(ctx, tx, query.ExcludeIntentID)
		if err != nil {
			return err
		}
		if err = s.paymentActor(ctx, tx, a, p, false); err != nil {
			return err
		}
		if !p.history || p.consent != query.ConsentVersion || p.cmd.Binding.SubjectContactID != query.SubjectContactID {
			return domain.Fail("NOT_FOUND", 404)
		}
		h, err := s.canonicalHistory(ctx, tx, p)
		if err != nil {
			return err
		}
		key := investigation.PermissionKey(hit.SourceID, hit.SourceVersion, hit.ConsentVersion, hit.ProjectionRevision)
		permitted := false
		for _, allowed := range historyPermissions(h) {
			if allowed == key {
				permitted = true
			}
		}
		if !permitted {
			return domain.Fail("NOT_FOUND", 404)
		}
		var ciphertext []byte
		err = tx.QueryRow(ctx, `SELECT v.binding_schema,v.amount_minor,v.currency,v.payee_id,v.subject_contact_id::text,v.purpose,m.ciphertext FROM intent_versions v JOIN request_messages m ON m.intent_id=v.intent_id AND m.version=v.version JOIN payment_intents i ON i.id=v.intent_id WHERE v.intent_id=$1 AND v.version=$2 AND m.deleted_at IS NULL AND m.expires_at>$3 AND i.created_at BETWEEN $4 AND $5`, hit.SourceID, hit.SourceVersion, s.Clock.Now(), query.Cutoff, query.Now).Scan(&out.Binding.Schema, &out.Binding.AmountMinor, &out.Binding.Currency, &out.Binding.PayeeID, &out.Binding.SubjectContactID, &out.Binding.Purpose, &ciphertext)
		if err == pgx.ErrNoRows {
			return domain.Fail("NOT_FOUND", 404)
		}
		if err != nil {
			return err
		}
		out.IntentID, out.Version, out.ConsentVersion, out.ProjectionRevision = hit.SourceID, hit.SourceVersion, hit.ConsentVersion, hit.ProjectionRevision
		out.Binding.IntentID, out.Binding.Version = out.IntentID, out.Version
		out.Message, err = s.Box.Decrypt(ciphertext, fmt.Sprintf("%s:%d", out.IntentID, out.Version))
		return err
	})
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
