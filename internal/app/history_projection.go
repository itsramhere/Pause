package app

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/ramst/pause/internal/domain"
	"github.com/ramst/pause/internal/investigation"
	"github.com/ramst/pause/internal/store"
)

var privateAddresses = regexp.MustCompile(`(?i)https?://\S+|[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}|\+?\d[\d ()-]{6,}\d`)

func redactHistory(text string) string {
	r := []rune(privateAddresses.ReplaceAllString(text, "[redacted]"))
	if len(r) > 600 {
		r = r[:600]
	}
	return string(r)
}

func (s *Service) projectionDocument(ctx context.Context, id string) (investigation.PrivateDocument, error) {
	var d investigation.PrivateDocument
	var ciphertext []byte
	var allowed bool
	var action string
	err := s.DB.Pool.QueryRow(ctx, `SELECT i.household_id::text,v.subject_contact_id::text,i.id::text,i.current_version,v.content_digest,p.revision,i.consent_version,i.created_at,m.expires_at,v.purpose,v.amount_minor,CASE WHEN seed.intent_id IS NULL THEN 'USER_SUBMITTED' ELSE 'SYNTHETIC_SEEDED_HISTORY' END,c.history_allowed,p.action,CASE WHEN c.history_allowed AND p.action='UPSERT' AND m.deleted_at IS NULL AND m.expires_at>$2 THEN m.ciphertext ELSE NULL END
 FROM search_projections p JOIN payment_intents i ON i.id=p.intent_id JOIN intent_versions v ON v.intent_id=i.id AND v.version=i.current_version JOIN intent_consents c ON c.intent_id=i.id AND c.version=i.consent_version JOIN request_messages m ON m.intent_id=i.id AND m.version=i.current_version LEFT JOIN synthetic_history seed ON seed.intent_id=i.id WHERE i.id=$1`, id, s.Clock.Now()).Scan(&d.Household, &d.Contact, &d.Intent, &d.Version, &d.Digest, &d.Revision, &d.Consent, &d.Time, &d.Expires, &d.Purpose, &d.Amount, &d.Source, &allowed, &action, &ciphertext)
	if err != nil {
		return d, err
	}
	d.Deleted = !allowed || action != "UPSERT" || ciphertext == nil || !s.Clock.Now().Before(d.Expires)
	if d.Deleted {
		return d, nil
	}
	text, err := s.Box.Decrypt(ciphertext, fmt.Sprintf("%s:%d", d.Intent, d.Version))
	if err != nil {
		return d, err
	}
	d.Text = redactHistory(text)
	d.Language = "en"
	d.Encoder = s.Investigation.Encoder.EncoderVersion
	d.Permission = investigation.PermissionKey(d.Intent, d.Version, d.Consent, d.Revision)
	in := domain.EmbedInput{Text: d.Purpose + " " + d.Text, InputDigest: domain.Hash([]byte(d.Purpose + " " + d.Text)), EncoderVersion: d.Encoder}
	emb, err := s.Investigation.Embed(ctx, in)
	d.Vector = emb.Vector
	return d, err
}

// Read current canonical permission rather than trusting an old outbox payload.
// Versioned replacement prevents a delayed prepared upsert beating a tombstone.
func (s *Service) syncHistory(ctx context.Context, id string, targets []string) error {
	if s.Investigation == nil {
		return errors.New("private search unavailable")
	}
	d, err := s.projectionDocument(ctx, id)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		rows, err := s.DB.Pool.Query(ctx, `SELECT name,encoder_version FROM history_indexes ORDER BY name`)
		if err != nil {
			return err
		}
		type target struct{ name, encoder string }
		all := []target{}
		for rows.Next() {
			var x target
			if err = rows.Scan(&x.name, &x.encoder); err != nil {
				rows.Close()
				return err
			}
			all = append(all, x)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(all) == 0 {
			return errors.New("private index not configured")
		}
		for _, x := range all {
			doc := d
			if x.encoder != s.Investigation.Encoder.EncoderVersion {
				doc.Deleted = true
			}
			if err = s.Investigation.WriteHistory(ctx, x.name, doc); err != nil {
				return err
			}
		}
	} else {
		for _, target := range targets {
			if err = s.Investigation.WriteHistory(ctx, target, d); err != nil {
				return err
			}
		}
	}
	// Re-read after dependency I/O. If withdrawal/expiry won the race, immediately
	// replace prepared data before acknowledging any projection progress.
	var rev int
	var allowed bool
	err = s.DB.Pool.QueryRow(ctx, `SELECT p.revision,c.history_allowed AND p.action='UPSERT' AND m.deleted_at IS NULL AND m.expires_at>$2 FROM search_projections p JOIN payment_intents i ON i.id=p.intent_id JOIN intent_consents c ON c.intent_id=i.id AND c.version=i.consent_version JOIN request_messages m ON m.intent_id=i.id AND m.version=i.current_version WHERE i.id=$1`, id, s.Clock.Now()).Scan(&rev, &allowed)
	if err != nil {
		return err
	}
	if rev != d.Revision || (!allowed && !d.Deleted) {
		return s.syncHistory(ctx, id, targets)
	}
	if len(targets) == 0 {
		_, err = s.DB.Pool.Exec(ctx, `UPDATE search_projections SET indexed_revision=$2,indexed_at=$3 WHERE intent_id=$1 AND revision=$2`, id, rev, s.Clock.Now())
	}
	return err
}
func (s *Service) WorkHistoryOnce(ctx context.Context) (bool, error) {
	jobs := &store.Jobs{DB: s.DB, Clock: s.Clock}
	job, err := jobs.Lease(ctx, []string{"SEARCH_PROJECTION"}, 2*time.Minute)
	if err != nil || job == nil {
		return false, err
	}
	err = s.syncHistory(ctx, job.ObjectID, nil)
	if err != nil {
		retry := jobs.Retry(ctx, *job, "PRIVATE_PROJECTION_UNAVAILABLE", time.Duration(job.Attempts)*time.Second)
		if retry != nil {
			return true, retry
		}
		return true, err
	}
	return true, jobs.Complete(ctx, *job)
}

// Explicit operator recovery after inspecting the dependency. This resets only
// failed private projection jobs; payment operations and active leases are never
// replayed by this command. Every retry still reloads canonical permission.
func (s *Service) RetryHistoryCleanup(ctx context.Context) error {
	_, err := s.DB.Pool.Exec(ctx, `UPDATE jobs SET failed_at=NULL,attempts=0,not_before=$1,lease_token=NULL,lease_until=NULL WHERE kind='SEARCH_PROJECTION' AND failed_at IS NOT NULL`, s.Clock.Now())
	return err
}

// Reindex is an explicit offline operation. Registered building and retired
// targets receive ongoing writes/deletions so a rollback cannot restore consent.
func (s *Service) ReindexHistory(ctx context.Context, name string) error {
	if s.Investigation == nil {
		return errors.New("private search unavailable")
	}
	conn, err := s.DB.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err = conn.Exec(ctx, `SELECT pg_advisory_lock(7040401)`); err != nil {
		return err
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock(7040401)`)
	if err = s.Investigation.CreateHistoryIndex(ctx, name); err != nil {
		return err
	}
	_, err = s.DB.Pool.Exec(ctx, `INSERT INTO history_indexes(name,encoder_version,state,created_at) VALUES($1,$2,'BUILDING',$3) ON CONFLICT(name) DO NOTHING`, name, s.Investigation.Encoder.EncoderVersion, s.Clock.Now())
	if err != nil {
		return err
	}
	var encoder string
	if err = s.DB.Pool.QueryRow(ctx, `SELECT encoder_version FROM history_indexes WHERE name=$1`, name).Scan(&encoder); err != nil || encoder != s.Investigation.Encoder.EncoderVersion {
		return errors.New("registered encoder mismatch")
	}
	// Page by stable UUID; never keep a PG transaction open over embeddings.
	last := "00000000-0000-0000-0000-000000000000"
	for {
		rows, err := s.DB.Pool.Query(ctx, `SELECT intent_id::text FROM search_projections WHERE intent_id>$1 ORDER BY intent_id LIMIT 100`, last)
		if err != nil {
			return err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			if err = s.syncHistory(ctx, id, []string{name}); err != nil {
				return err
			}
			last = id
		}
	}
	if err = s.Investigation.SwitchHistory(ctx, name); err != nil {
		return err
	}
	return s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE history_indexes SET state='RETIRED' WHERE state='ACTIVE'`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE history_indexes SET state='ACTIVE' WHERE name=$1`, name)
		return err
	})
}
