package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/ramst/pause/internal/config"
	"github.com/ramst/pause/internal/domain"
	"github.com/ramst/pause/internal/secure"
	"github.com/ramst/pause/internal/store"
)

type Service struct {
	DB     *store.Store
	Config config.Config
	Clock  domain.Clock
	Box    *secure.Box
}
type Intake struct {
	Amount           string `json:"amount"`
	Currency         string `json:"currency"`
	MerchantID       string `json:"merchant_id"`
	SubjectContactID string `json:"subject_contact_id"`
	Purpose          string `json:"purpose"`
	Message          string `json:"message"`
	HistoryConsent   *bool  `json:"history_consent"`
}
type Correction struct {
	ExpectedVersion        int `json:"expected_version"`
	ExpectedConsentVersion int `json:"expected_consent_version"`
	Intake
}
type ConsentChange struct {
	ExpectedVersion int   `json:"expected_version"`
	History         *bool `json:"history"`
}

var idempotencyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)

func (s *Service) normalize(in Intake) (Intake, int64, error) {
	amount, e := domain.ParseUSD(in.Amount, in.Currency, s.Config.MaxAmount)
	if e != nil {
		return in, 0, e
	}
	if in.MerchantID != s.Config.MerchantID {
		return in, 0, domain.Fail("MERCHANT_NOT_ALLOWED", 422)
	}
	id, e := uuid.Parse(in.SubjectContactID)
	if e != nil {
		return in, 0, domain.Fail("CONTACT_NOT_ALLOWED", 422)
	}
	in.SubjectContactID = id.String()
	in.Purpose, e = domain.NormalizePurpose(in.Purpose)
	if e != nil {
		return in, 0, e
	}
	if !utf8.ValidString(in.Message) || len(in.Message) > 8000 || strings.TrimSpace(in.Message) == "" || strings.ContainsRune(in.Message, 0) {
		return in, 0, domain.Fail("INVALID_MESSAGE", 422)
	}
	if in.HistoryConsent == nil {
		return in, 0, domain.Fail("CONSENT_REQUIRED", 422)
	}
	in.Amount = fmt.Sprintf("%d.%02d", amount/100, amount%100)
	return in, amount, nil
}
func (s *Service) active(ctx context.Context, tx pgx.Tx, a domain.Actor, role domain.Role) error {
	if a.Role != role {
		return domain.Fail("FORBIDDEN", 403)
	}
	var ok bool
	e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memberships WHERE household_id=$1 AND user_id=$2 AND role=$3 AND active)`, a.HouseholdID, a.UserID, role).Scan(&ok)
	if e != nil {
		return e
	}
	if !ok {
		return domain.Fail("FORBIDDEN", 403)
	}
	return nil
}
func (s *Service) contact(ctx context.Context, tx pgx.Tx, a domain.Actor, id string) error {
	var ok bool
	e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM contacts c JOIN memberships m ON m.household_id=c.household_id AND m.user_id=c.user_id WHERE c.id=$1 AND c.household_id=$2 AND c.active AND m.active AND m.role='subject')`, id, a.HouseholdID).Scan(&ok)
	if e != nil {
		return e
	}
	if !ok {
		return domain.Fail("CONTACT_NOT_ALLOWED", 422)
	}
	return nil
}
func (s *Service) Create(ctx context.Context, a domain.Actor, in Intake, key, correlation string) (domain.Intent, bool, error) {
	var result domain.Intent
	var replay bool
	in, amount, e := s.normalize(in)
	if e != nil {
		return result, false, e
	}
	if !idempotencyPattern.MatchString(key) {
		return result, false, domain.Fail("IDEMPOTENCY_KEY_REQUIRED", 422)
	}
	raw, _ := json.Marshal(in)
	requestHash := domain.Hash(raw)
	id := uuid.NewString()
	now := s.Clock.Now()
	e = s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		if e := s.active(ctx, tx, a, domain.Senior); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `SELECT id FROM households WHERE id=$1 FOR UPDATE`, a.HouseholdID); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, a.UserID+":"+key); e != nil {
			return e
		}
		var oldHash, existing string
		e := tx.QueryRow(ctx, `SELECT request_hash,intent_id::text FROM idempotency_records WHERE actor_id=$1 AND key=$2 AND expires_at>$3`, a.UserID, key, now).Scan(&oldHash, &existing)
		if e == nil {
			if requestHash != oldHash {
				return domain.Fail("IDEMPOTENCY_CONFLICT", 409)
			}
			id = existing
			replay = true
			return nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if e = s.contact(ctx, tx, a, in.SubjectContactID); e != nil {
			return e
		}
		var reviewer string
		e = tx.QueryRow(ctx, `SELECT user_id::text FROM memberships WHERE household_id=$1 AND role='reviewer' AND active AND user_id<>$2 ORDER BY id LIMIT 1`, a.HouseholdID, a.UserID).Scan(&reviewer)
		if errors.Is(e, pgx.ErrNoRows) {
			return domain.Fail("REVIEWER_UNAVAILABLE", 503)
		}
		if e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO payment_intents(id,household_id,creator_id,reviewer_id,current_version,consent_version,created_at,decision_deadline) VALUES($1,$2,$3,$4,1,1,$5,$6)`, id, a.HouseholdID, a.UserID, reviewer, now, now.Add(s.Config.DecisionTTL)); e != nil {
			return e
		}
		if e = s.writeVersion(ctx, tx, a, id, 1, in, amount); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO intent_consents(intent_id,version,history_allowed,actor_id,recorded_at) VALUES($1,1,$2,$3,$4)`, id, *in.HistoryConsent, a.UserID, now); e != nil {
			return e
		}
		if e = s.project(ctx, tx, id, 1, 1, *in.HistoryConsent, now.Add(s.Config.MessageRetention)); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `DELETE FROM idempotency_records WHERE actor_id=$1 AND key=$2 AND expires_at<=$3`, a.UserID, key, now); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO idempotency_records(actor_id,key,request_hash,intent_id,intent_version,expires_at) VALUES($1,$2,$3,$4,1,$5)`, a.UserID, key, requestHash, id, now.Add(s.Config.OperationRetention)); e != nil {
			return e
		}
		if e = s.invalidate(ctx, tx, a.HouseholdID); e != nil {
			return e
		}
		return s.audit(ctx, tx, a, id, "INTENT_CREATED", "EXPLICIT_INTAKE", correlation)
	})
	if e != nil {
		return result, false, e
	}
	result, e = s.Read(ctx, a, id)
	return result, replay, e
}
func (s *Service) writeVersion(ctx context.Context, tx pgx.Tx, a domain.Actor, id string, version int, in Intake, amount int64) error {
	b, e := (domain.Binding{IntentID: id, Version: version, AmountMinor: amount, Currency: in.Currency, PayeeID: in.MerchantID, SubjectContactID: in.SubjectContactID, Purpose: in.Purpose}).Canonical()
	if e != nil {
		return e
	}
	digest, e := b.Digest()
	if e != nil {
		return e
	}
	now := s.Clock.Now()
	if _, e = tx.Exec(ctx, `INSERT INTO intent_versions(intent_id,household_id,version,amount_minor,currency,payee_id,subject_contact_id,purpose,binding_schema,content_digest,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, id, a.HouseholdID, version, amount, in.Currency, in.MerchantID, in.SubjectContactID, in.Purpose, b.Schema, digest, now); e != nil {
		return e
	}
	ciphertext, e := s.Box.Encrypt(in.Message, fmt.Sprintf("%s:%d", b.IntentID, version))
	if e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO request_messages(intent_id,version,ciphertext,expires_at) VALUES($1,$2,$3,$4)`, id, version, ciphertext, now.Add(s.Config.MessageRetention)); e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO request_events(id,intent_id,intent_version,kind,source,occurred_at) VALUES($1,$2,$3,'INTAKE','USER_ENTERED',$4)`, uuid.NewString(), id, version, now)
	return e
}
func (s *Service) Correct(ctx context.Context, a domain.Actor, id string, change Correction, correlation string) (domain.Intent, error) {
	var out domain.Intent
	in, amount, e := s.normalize(change.Intake)
	if e != nil {
		return out, e
	}
	parsed, e := uuid.Parse(id)
	if e != nil {
		return out, domain.Fail("NOT_FOUND", 404)
	}
	id = parsed.String()
	e = s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		if e := s.active(ctx, tx, a, domain.Senior); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `SELECT id FROM households WHERE id=$1 FOR UPDATE`, a.HouseholdID); e != nil {
			return e
		}
		var version, consent int
		var status string
		var order *string
		var history bool
		e := tx.QueryRow(ctx, `SELECT i.current_version,i.consent_version,i.payment_status,i.provider_order_id,c.history_allowed FROM payment_intents i JOIN intent_consents c ON c.intent_id=i.id AND c.version=i.consent_version WHERE i.id=$1 AND i.household_id=$2 AND i.creator_id=$3 FOR UPDATE OF i`, id, a.HouseholdID, a.UserID).Scan(&version, &consent, &status, &order, &history)
		if errors.Is(e, pgx.ErrNoRows) {
			return domain.Fail("NOT_FOUND", 404)
		}
		if e != nil {
			return e
		}
		if version != change.ExpectedVersion {
			return domain.Fail("VERSION_CONFLICT", 409)
		}
		if status != "DRAFT" || order != nil {
			return domain.Fail("VERSION_BOUND", 409)
		}
		if consent != change.ExpectedConsentVersion {
			return domain.Fail("CONSENT_VERSION_CONFLICT", 409)
		}
		if e = s.contact(ctx, tx, a, in.SubjectContactID); e != nil {
			return e
		}
		version++
		if e = s.writeVersion(ctx, tx, a, id, version, in, amount); e != nil {
			return e
		}
		if history != *in.HistoryConsent {
			consent++
			if _, e = tx.Exec(ctx, `INSERT INTO intent_consents(intent_id,version,history_allowed,actor_id,recorded_at) VALUES($1,$2,$3,$4,$5)`, id, consent, *in.HistoryConsent, a.UserID, s.Clock.Now()); e != nil {
				return e
			}
		}
		if _, e = tx.Exec(ctx, `UPDATE payment_intents SET current_version=$2,consent_version=$3,verification_status='NOT_REQUESTED',review_status='NOT_REQUESTED' WHERE id=$1`, id, version, consent); e != nil {
			return e
		}
		if e = s.invalidate(ctx, tx, a.HouseholdID); e != nil {
			return e
		}
		if e = s.project(ctx, tx, id, version, consent, *in.HistoryConsent, s.Clock.Now().Add(s.Config.MessageRetention)); e != nil {
			return e
		}
		return s.audit(ctx, tx, a, id, "INTENT_CORRECTED", "NEW_IMMUTABLE_VERSION", correlation)
	})
	if e != nil {
		return out, e
	}
	return s.Read(ctx, a, id)
}
func (s *Service) ChangeConsent(ctx context.Context, a domain.Actor, id string, change ConsentChange, correlation string) (domain.Intent, error) {
	var out domain.Intent
	if change.History == nil {
		return out, domain.Fail("CONSENT_REQUIRED", 422)
	}
	parsed, parseErr := uuid.Parse(id)
	if parseErr != nil {
		return out, domain.Fail("NOT_FOUND", 404)
	}
	id = parsed.String()
	e := s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		if e := s.active(ctx, tx, a, domain.Senior); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `SELECT id FROM households WHERE id=$1 FOR UPDATE`, a.HouseholdID); e != nil {
			return e
		}
		var version, consent int
		var allowed bool
		var expires time.Time
		e := tx.QueryRow(ctx, `SELECT i.current_version,i.consent_version,c.history_allowed,m.expires_at FROM payment_intents i JOIN intent_consents c ON c.intent_id=i.id AND c.version=i.consent_version JOIN request_messages m ON m.intent_id=i.id AND m.version=i.current_version WHERE i.id=$1 AND i.household_id=$2 AND i.creator_id=$3 FOR UPDATE OF i`, id, a.HouseholdID, a.UserID).Scan(&version, &consent, &allowed, &expires)
		if errors.Is(e, pgx.ErrNoRows) {
			return domain.Fail("NOT_FOUND", 404)
		}
		if e != nil {
			return e
		}
		if consent != change.ExpectedVersion {
			return domain.Fail("CONSENT_VERSION_CONFLICT", 409)
		}
		if allowed == *change.History {
			return nil
		}
		consent++
		if _, e = tx.Exec(ctx, `INSERT INTO intent_consents(intent_id,version,history_allowed,actor_id,recorded_at) VALUES($1,$2,$3,$4,$5)`, id, consent, *change.History, a.UserID, s.Clock.Now()); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `UPDATE payment_intents SET consent_version=$2 WHERE id=$1`, id, consent); e != nil {
			return e
		}
		if e = s.invalidate(ctx, tx, a.HouseholdID); e != nil {
			return e
		}
		if e = s.project(ctx, tx, id, version, consent, *change.History && expires.After(s.Clock.Now()), expires); e != nil {
			return e
		}
		return s.audit(ctx, tx, a, id, "CONSENT_CHANGED", "HISTORY_PERMISSION_UPDATED", correlation)
	})
	if e != nil {
		return out, e
	}
	return s.Read(ctx, a, id)
}
func (s *Service) invalidate(ctx context.Context, tx pgx.Tx, household string) error {
	now := s.Clock.Now()
	if _, e := tx.Exec(ctx, `UPDATE households SET history_revision=history_revision+1 WHERE id=$1`, household); e != nil {
		return e
	}
	// Physically clear derived evidence as well as denying stale reads immediately.
	if _, e := tx.Exec(ctx, `UPDATE evidence_snapshots e SET invalidated_at=$2,evidence='{}'::jsonb FROM payment_intents i WHERE e.intent_id=i.id AND i.household_id=$1 AND e.invalidated_at IS NULL`, household, now); e != nil {
		return e
	}
	_, e := tx.Exec(ctx, `UPDATE payment_intents SET review_status='STALE' WHERE household_id=$1 AND review_status='APPROVED'`, household)
	return e
}
func (s *Service) audit(ctx context.Context, tx pgx.Tx, a domain.Actor, id, action, reason, correlation string) error {
	_, e := tx.Exec(ctx, `INSERT INTO audit_events(id,household_id,actor_id,action,object_id,reason_code,correlation_id,occurred_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, uuid.NewString(), a.HouseholdID, a.UserID, action, id, reason, correlation, s.Clock.Now())
	return e
}
