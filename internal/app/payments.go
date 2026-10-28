package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/ramst/pause/internal/domain"
	"github.com/ramst/pause/internal/investigation"
	"github.com/ramst/pause/internal/paypal"
	"github.com/ramst/pause/internal/store"
)

const PaymentPolicy = "pause-payment-policy-v1"

type PaymentAction struct {
	ExpectedVersion int    `json:"expected_version"`
	OrderID         string `json:"order_id,omitempty"`
}
type paymentRow struct {
	cmd                          domain.ProviderCommand
	household, creator, reviewer string
	state                        domain.PaymentStatus
	deadline                     time.Time
	cleanup, history             bool
	consent                      int
	revision                     int64
}

func (s *Service) paymentLock(ctx context.Context, tx pgx.Tx, id string) (paymentRow, error) {
	var p paymentRow
	p.cmd.Environment = paypal.Sandbox
	e := tx.QueryRow(ctx, `SELECT household_id::text FROM payment_intents WHERE id=$1`, id).Scan(&p.household)
	if errors.Is(e, pgx.ErrNoRows) {
		return p, domain.Fail("NOT_FOUND", 404)
	}
	if e != nil {
		return p, e
	}
	if _, e = tx.Exec(ctx, `SELECT id FROM households WHERE id=$1 FOR UPDATE`, p.household); e != nil {
		return p, e
	}
	e = tx.QueryRow(ctx, `SELECT i.id::text,i.current_version,i.creator_id::text,i.reviewer_id::text,i.payment_status,i.decision_deadline,i.cleanup_requested,i.consent_version,h.history_revision,c.history_allowed,COALESCE(i.provider_order_id,''),COALESCE(i.authorization_id,''),COALESCE(i.capture_id,''),v.binding_schema,v.amount_minor,v.currency,v.payee_id,v.subject_contact_id::text,v.purpose,v.content_digest FROM payment_intents i JOIN households h ON h.id=i.household_id JOIN intent_consents c ON c.intent_id=i.id AND c.version=i.consent_version JOIN intent_versions v ON v.intent_id=i.id AND v.version=i.current_version WHERE i.id=$1 FOR UPDATE OF i`, id).Scan(&p.cmd.IntentID, &p.cmd.Version, &p.creator, &p.reviewer, &p.state, &p.deadline, &p.cleanup, &p.consent, &p.revision, &p.history, &p.cmd.OrderID, &p.cmd.AuthorizationID, &p.cmd.CaptureID, &p.cmd.Binding.Schema, &p.cmd.Binding.AmountMinor, &p.cmd.Binding.Currency, &p.cmd.Binding.PayeeID, &p.cmd.Binding.SubjectContactID, &p.cmd.Binding.Purpose, &p.cmd.Digest)
	p.cmd.Binding.IntentID = p.cmd.IntentID
	p.cmd.Binding.Version = p.cmd.Version
	return p, e
}
func (s *Service) paymentActor(ctx context.Context, tx pgx.Tx, a domain.Actor, p paymentRow, seniorOnly bool) error {
	if a.HouseholdID != p.household || (a.Role == domain.Senior && a.UserID != p.creator) || (a.Role == domain.Reviewer && (seniorOnly || a.UserID != p.reviewer)) || (a.Role != domain.Senior && a.Role != domain.Reviewer) {
		return domain.Fail("NOT_FOUND", 404)
	}
	return s.active(ctx, tx, a, a.Role)
}
func canonicalID(id string) (string, error) {
	u, e := uuid.Parse(id)
	if e != nil {
		return "", domain.Fail("NOT_FOUND", 404)
	}
	return u.String(), nil
}
func (s *Service) queue(ctx context.Context, tx pgx.Tx, kind, object, key string) error {
	_, e := tx.Exec(ctx, `INSERT INTO jobs(id,kind,object_id,dedup_key,not_before) VALUES($1,$2,$3,$4,$5) ON CONFLICT(dedup_key) DO UPDATE SET completed_at=NULL,attempts=CASE WHEN jobs.completed_at IS NOT NULL THEN 0 ELSE jobs.attempts END,not_before=CASE WHEN jobs.completed_at IS NOT NULL THEN EXCLUDED.not_before ELSE jobs.not_before END WHERE jobs.failed_at IS NULL`, uuid.NewString(), kind, object, key, s.Clock.Now())
	return e
}
func (s *Service) paymentAudit(ctx context.Context, tx pgx.Tx, p paymentRow, actor *domain.Actor, action, reason, correlation string) error {
	var actorID any
	if actor != nil {
		actorID = actor.UserID
	}
	_, e := tx.Exec(ctx, `INSERT INTO audit_events(id,household_id,actor_id,action,object_id,reason_code,correlation_id,occurred_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, uuid.NewString(), p.household, actorID, action, p.cmd.IntentID, reason, correlation, s.Clock.Now())
	return e
}
func (s *Service) state(ctx context.Context, tx pgx.Tx, p *paymentRow, to domain.PaymentStatus, reason string) error {
	if p.state == to {
		return nil
	}
	if !domain.CanTransition(p.state, to) {
		return domain.Fail("INVALID_TRANSITION", 409)
	}
	if _, e := tx.Exec(ctx, `UPDATE payment_intents SET payment_status=$2,closure_reason=$3 WHERE id=$1`, p.cmd.IntentID, to, reason); e != nil {
		return e
	}
	_, e := tx.Exec(ctx, `INSERT INTO request_events(id,intent_id,intent_version,kind,source,occurred_at) VALUES($1,$2,$3,'PAYMENT_STATE','PAYPAL',$4)`, uuid.NewString(), p.cmd.IntentID, p.cmd.Version, s.Clock.Now())
	if e == nil {
		p.state = to
	}
	return e
}
func (s *Service) reserve(ctx context.Context, tx pgx.Tx, p *paymentRow, kind string, actor *domain.Actor, key, hash, correlation string) (string, error) {
	var op string
	var oldHash string
	if actor != nil {
		e := tx.QueryRow(ctx, `SELECT request_hash,operation_id::text FROM payment_receipts WHERE actor_id=$1 AND key=$2`, actor.UserID, key).Scan(&oldHash, &op)
		if e == nil {
			if oldHash != hash {
				return "", domain.Fail("IDEMPOTENCY_CONFLICT", 409)
			}
			return op, nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return "", e
		}
	}
	e := tx.QueryRow(ctx, `SELECT id::text FROM payment_operations WHERE intent_id=$1 AND intent_version=$2 AND kind=$3`, p.cmd.IntentID, p.cmd.Version, kind).Scan(&op)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return "", e
	}
	if errors.Is(e, pgx.ErrNoRows) {
		op = uuid.NewString()
		requestID := uuid.NewString()
		body := paypal.Payload(kind, p.cmd)
		if body == nil {
			body = []byte{}
		}
		_, e = tx.Exec(ctx, `INSERT INTO payment_operations(id,intent_id,intent_version,kind,provider_request_id,state,request_hash,request_body,provider_object_id,created_at,retain_until) VALUES($1,$2,$3,$4,$5,'RESERVED',$6,$7,$8,$9,$10)`, op, p.cmd.IntentID, p.cmd.Version, kind, requestID, domain.Hash(body), body, p.cmd.AuthorizationID, s.Clock.Now(), s.Clock.Now().Add(90*24*time.Hour))
		if e != nil {
			return "", e
		}
		if e = s.queue(ctx, tx, "PAYMENT_OPERATION", op, "payment:"+op); e != nil {
			return "", e
		}
		if e = s.paymentAudit(ctx, tx, *p, actor, "PAYMENT_RESERVED", kind, correlation); e != nil {
			return "", e
		}
	}
	if actor != nil {
		_, e = tx.Exec(ctx, `INSERT INTO payment_receipts(actor_id,key,request_hash,operation_id,created_at) VALUES($1,$2,$3,$4,$5)`, actor.UserID, key, hash, op, s.Clock.Now())
		if e != nil {
			return "", e
		}
	}
	return op, nil
}

// ReserveCheckout only persists work. A worker dispatches after this transaction commits.
func (s *Service) ReserveCheckout(ctx context.Context, a domain.Actor, id, kind, key string, in PaymentAction, correlation string) (domain.Intent, error) {
	var out domain.Intent
	if s.Provider == nil {
		return out, domain.Fail("PAYMENTS_UNAVAILABLE", 503)
	}
	id, e := canonicalID(id)
	if e != nil {
		return out, e
	}
	if !idempotencyPattern.MatchString(key) {
		return out, domain.Fail("IDEMPOTENCY_KEY_REQUIRED", 400)
	}
	if kind != "CREATE_ORDER" && kind != "AUTHORIZE" {
		return out, domain.Fail("INVALID_COMMAND", 400)
	}
	raw, _ := json.Marshal(struct {
		ID, Kind string
		Action   PaymentAction
	}{id, kind, in})
	hash := domain.Hash(raw)
	e = s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		p, e := s.paymentLock(ctx, tx, id)
		if e != nil {
			return e
		}
		if e = s.paymentActor(ctx, tx, a, p, true); e != nil {
			return e
		}
		if p.cmd.Version != in.ExpectedVersion {
			return domain.Fail("VERSION_CONFLICT", 409)
		}
		// Receipt lookup precedes state checks so a double click returns its original reservation.
		var prior, priorHash string
		e = tx.QueryRow(ctx, `SELECT operation_id::text,request_hash FROM payment_receipts WHERE actor_id=$1 AND key=$2`, a.UserID, key).Scan(&prior, &priorHash)
		if e == nil {
			if priorHash != hash {
				return domain.Fail("IDEMPOTENCY_CONFLICT", 409)
			}
			return nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if kind == "CREATE_ORDER" {
			if p.cmd.Binding.PayeeID != s.Config.MerchantID {
				return domain.Fail("MERCHANT_NOT_ALLOWED", 409)
			}
			if !s.Clock.Now().Before(p.deadline) || p.cleanup {
				return domain.Fail("DEADLINE_EXPIRED", 409)
			}
			if p.state != domain.Draft && p.state != domain.AwaitingPayer {
				return domain.Fail("INVALID_TRANSITION", 409)
			}
			if p.state == domain.Draft {
				if e = s.state(ctx, tx, &p, domain.AwaitingPayer, "ORDER_REQUESTED"); e != nil {
					return e
				}
			}
		} else {
			if in.OrderID == "" || in.OrderID != p.cmd.OrderID {
				return domain.Fail("ORDER_MAPPING_MISMATCH", 409)
			}
			if p.state != domain.AwaitingPayer && p.state != domain.Reconciling && p.state != domain.Abandoned && p.state != domain.Held {
				return domain.Fail("INVALID_TRANSITION", 409)
			}
			if p.state == domain.Abandoned {
				if e = s.state(ctx, tx, &p, domain.Reconciling, "LATE_APPROVAL"); e != nil {
					return e
				}
			}
		}
		_, e = s.reserve(ctx, tx, &p, kind, &a, key, hash, correlation)
		return e
	})
	if e != nil {
		return out, e
	}
	return s.Read(ctx, a, id)
}
func (s *Service) reserveVoid(ctx context.Context, tx pgx.Tx, p *paymentRow, reason string) error {
	if p.cmd.AuthorizationID == "" {
		return nil
	}
	if p.state == domain.Captured || p.state == domain.Voided || p.state == domain.ClosedUncaptured {
		return nil
	}
	var active bool
	if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_operations WHERE intent_id=$1 AND kind IN ('CAPTURE','VOID') AND state IN ('RESERVED','DISPATCHED','UNKNOWN'))`, p.cmd.IntentID).Scan(&active); e != nil {
		return e
	}
	if active || p.state == domain.Reconciling || p.state == domain.CapturePending || p.state == domain.VoidPending {
		return s.queue(ctx, tx, "PAYMENT_RECONCILE", p.cmd.IntentID, "reconcile:"+p.cmd.IntentID)
	}
	if p.state != domain.Held {
		return nil
	}
	var failed bool
	if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_operations WHERE intent_id=$1 AND kind='VOID' AND state='FAILED')`, p.cmd.IntentID).Scan(&failed); e != nil {
		return e
	}
	if failed {
		return s.state(ctx, tx, p, domain.Reconciling, "CLEANUP_FAILED_OPERATOR_ATTENTION")
	}
	if e := s.state(ctx, tx, p, domain.VoidPending, reason); e != nil {
		return e
	}
	_, e := s.reserve(ctx, tx, p, "VOID", nil, "", "", "cleanup")
	return e
}

// Cancel records a cleanup intent even when capture is already in flight. It
// never describes an in-flight capture as reversed or queues a competing void.
func (s *Service) Cancel(ctx context.Context, a domain.Actor, id string, expected int, correlation string) (domain.Intent, error) {
	var out domain.Intent
	id, e := canonicalID(id)
	if e != nil {
		return out, e
	}
	e = s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		p, e := s.paymentLock(ctx, tx, id)
		if e != nil {
			return e
		}
		if e = s.paymentActor(ctx, tx, a, p, false); e != nil {
			return e
		}
		if expected != p.cmd.Version {
			return domain.Fail("VERSION_CONFLICT", 409)
		}
		if _, e = tx.Exec(ctx, `UPDATE payment_intents SET cleanup_requested=true WHERE id=$1`, id); e != nil {
			return e
		}
		if a.Role == domain.Reviewer {
			if _, e = tx.Exec(ctx, `UPDATE payment_intents SET review_status='DECLINED' WHERE id=$1`, id); e != nil {
				return e
			}
		}
		p.cleanup = true
		if p.state == domain.Draft {
			e = s.state(ctx, tx, &p, domain.Abandoned, "LOCALLY_ABANDONED")
		} else if p.state == domain.AwaitingPayer {
			var authorizing bool
			e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_operations WHERE intent_id=$1 AND kind='AUTHORIZE' AND state IN ('RESERVED','DISPATCHED','UNKNOWN'))`, id).Scan(&authorizing)
			if e != nil {
				return e
			}
			if authorizing {
				e = s.state(ctx, tx, &p, domain.Reconciling, "CANCEL_DURING_AUTHORIZATION")
			} else {
				e = s.state(ctx, tx, &p, domain.Abandoned, "NO_HOLD_OBSERVED")
			}
			if e == nil {
				e = s.queue(ctx, tx, "PAYMENT_RECONCILE", id, "reconcile:"+id)
			}
		} else {
			e = s.reserveVoid(ctx, tx, &p, "CANCEL_REQUESTED")
		}
		if e != nil {
			return e
		}
		return s.paymentAudit(ctx, tx, p, &a, "CLEANUP_REQUESTED", "USER_CANCEL", correlation)
	})
	if e != nil {
		return out, e
	}
	return s.Read(ctx, a, id)
}

// ReleaseEligibility is a private, typed capability produced only by a current
// PG evidence/policy check. P02 has no public capture route or fixture switch.
type releaseEligibility struct {
	pattern                          bool
	rationale                        string
	intent, digest, evidence, review string
	version, consent                 int
	revision                         int64
	requested, held, captured        int64
}
type releaseEvidence struct {
	Classification            domain.Classification     `json:"classification"`
	Classifier                domain.ClassifierManifest `json:"classifier"`
	Playbooks                 domain.SearchResult       `json:"playbooks"`
	History                   domain.SearchResult       `json:"history"`
	HistoryHash               string                    `json:"history_hash"`
	Requested, Held, Captured int64
	PolicyVersion             string                 `json:"policy_version"`
	Encoder                   domain.EncoderManifest `json:"encoder"`
	Unresolved                bool                   `json:"unresolved"`
}

func (s *Service) totals(ctx context.Context, tx pgx.Tx, p paymentRow) (requested, held, captured int64, err error) {
	err = tx.QueryRow(ctx, `SELECT COALESCE(sum(v.amount_minor),0)::bigint,COALESCE(sum(v.amount_minor) FILTER(WHERE i.authorization_id IS NOT NULL AND i.payment_status IN ('HELD','CAPTURE_PENDING','VOID_PENDING','RECONCILING')),0)::bigint,COALESCE(sum(v.amount_minor) FILTER(WHERE i.payment_status='CAPTURED'),0)::bigint FROM payment_intents i JOIN intent_versions v ON v.intent_id=i.id AND v.version=i.current_version WHERE i.household_id=$1 AND v.subject_contact_id=$2 AND i.created_at>=$3 AND i.created_at<=$4`, p.household, p.cmd.Binding.SubjectContactID, s.Clock.Now().Add(-7*24*time.Hour), s.Clock.Now()).Scan(&requested, &held, &captured)
	return
}
func (s *Service) eligibility(ctx context.Context, tx pgx.Tx, p paymentRow, a domain.Actor, evidenceID string, proof sourceValidation) (releaseEligibility, error) {
	var cap releaseEligibility
	reject := func() (releaseEligibility, error) { return cap, domain.Fail("RELEASE_EVIDENCE_REQUIRED", 409) }
	if p.cleanup || p.state != domain.Held || p.cmd.AuthorizationID == "" || !s.Clock.Now().Before(p.deadline) || a.UserID != p.reviewer || a.UserID == p.creator {
		return reject()
	}
	if e := s.active(ctx, tx, a, domain.Reviewer); e != nil {
		return cap, e
	}
	var subject string
	var subjectActive bool
	e := tx.QueryRow(ctx, `SELECT c.user_id::text,m.active AND c.active FROM contacts c JOIN memberships m ON m.household_id=c.household_id AND m.user_id=c.user_id AND m.role='subject' WHERE c.id=$1 AND c.household_id=$2`, p.cmd.Binding.SubjectContactID, p.household).Scan(&subject, &subjectActive)
	if e != nil || !subjectActive {
		return reject()
	}
	var verified bool
	e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM verifications WHERE intent_id=$1 AND intent_version=$2 AND content_digest=$3 AND subject_user_id=$4 AND status='CONFIRMED' AND responded_at IS NOT NULL AND expires_at>$5)`, p.cmd.IntentID, p.cmd.Version, p.cmd.Digest, subject, s.Clock.Now()).Scan(&verified)
	if e != nil {
		return cap, e
	}
	if !verified {
		return reject()
	}
	if s.Investigation != nil {
		var snapshot, responseHash []byte
		var hash *string
		if e = tx.QueryRow(ctx, `SELECT request_snapshot,response_evidence_hash FROM verifications WHERE intent_id=$1 AND intent_version=$2 AND subject_user_id=$3`, p.cmd.IntentID, p.cmd.Version, subject).Scan(&snapshot, &hash); e != nil || hash == nil {
			return reject()
		}
		responseHash = []byte(*hash)
		var bound taskSnapshot
		if json.Unmarshal(snapshot, &bound) != nil || bound.Binding != p.cmd.Binding || bound.SubjectUserID != subject || bound.Digest != p.cmd.Digest || string(responseHash) != domain.Hash(snapshot) {
			return reject()
		}
	}
	var raw []byte
	var evVersion, consent int
	var rev int64
	var digest, policy, classifier, encoder string
	e = tx.QueryRow(ctx, `SELECT e.intent_version,e.consent_version,e.history_revision,e.content_digest,e.policy_version,e.classifier_version,e.encoder_version,e.evidence FROM evidence_snapshots e JOIN payment_evidence_config cfg ON cfg.id=1 AND cfg.classifier_version=e.classifier_version AND cfg.encoder_version=e.encoder_version AND cfg.template_version=e.template_version AND cfg.policy_version=e.policy_version WHERE e.id=$1 AND e.intent_id=$2 AND e.invalidated_at IS NULL AND e.created_at>$3 AND e.created_at<=$4 AND e.schema_version=$5 FOR SHARE OF cfg`, evidenceID, p.cmd.IntentID, s.Clock.Now().Add(-30*time.Minute), s.Clock.Now(), domain.ContractVersion).Scan(&evVersion, &consent, &rev, &digest, &policy, &classifier, &encoder, &raw)
	if e != nil {
		return reject()
	}
	if evVersion != p.cmd.Version || consent != p.consent || rev != p.revision || digest != p.cmd.Digest || policy != PaymentPolicy {
		return reject()
	}
	var ev releaseEvidence
	if proof.evidenceID != evidenceID || proof.hash != evidenceHash(raw) {
		return reject()
	}
	if s.Investigation == nil && s.sourceValidator == nil {
		return reject()
	}
	if json.Unmarshal(raw, &ev) != nil || ev.Unresolved || ev.PolicyVersion != PaymentPolicy || ev.Classifier.Validate() != nil || ev.Encoder.Validate() != nil || ev.Classifier.ModelVersion != classifier || ev.Encoder.EncoderVersion != encoder {
		return reject()
	}
	if s.Investigation != nil {
		var stored map[string]any
		var packet EvidencePacket
		var hash string
		if json.Unmarshal(raw, &stored) != nil || json.Unmarshal(raw, &packet) != nil {
			return reject()
		}
		if e = tx.QueryRow(ctx, `SELECT evidence_hash FROM evidence_snapshots WHERE id=$1`, evidenceID).Scan(&hash); e != nil {
			return reject()
		}
		if hash != domain.Hash(mustJSON(stored)) || packet.Receipt.CorpusHash != s.Investigation.CorpusHash || packet.Receipt.ConcreteIndex != investigation.IndexVersion || ev.Playbooks.IndexVersion != investigation.IndexVersion || ev.Playbooks.MappingVersion != "playbooks-v1" || ev.Playbooks.FusionVersion != investigation.FusionVersion || len(s.Investigation.Sources(ev.Playbooks)) != len(ev.Playbooks.Hits) || ev.Classifier.ArtifactHash != s.Investigation.Classifier.ArtifactHash || ev.Encoder.ArtifactHash != s.Investigation.Encoder.ArtifactHash {
			return reject()
		}
		if p.history && (packet.History.Template != domain.HistoryTemplate || packet.History.Policy != domain.PatternPolicy || ev.History.MappingVersion != investigation.HistoryMapping || ev.History.FusionVersion != investigation.FusionVersion || packet.History.Receipt.ConcreteIndex != ev.History.IndexVersion) {
			return reject()
		}
	}
	var released int
	e = tx.QueryRow(ctx, `SELECT count(*) FROM model_releases WHERE approved_at IS NOT NULL AND ((kind='classifier' AND version=$1 AND artifact_hash=$2 AND manifest=$5::jsonb) OR (kind='encoder' AND version=$3 AND artifact_hash=$4 AND manifest=$6::jsonb))`, classifier, ev.Classifier.ArtifactHash, encoder, ev.Encoder.ArtifactHash, mustJSON(ev.Classifier), mustJSON(ev.Encoder)).Scan(&released)
	if e != nil {
		return cap, e
	}
	if released != 2 {
		return reject()
	}
	input := domain.InferenceInput{IntentID: p.cmd.IntentID, IntentVersion: p.cmd.Version, ContentDigest: p.cmd.Digest, Text: "bound retained message", Language: "en", ModelVersion: classifier}
	if ev.Classification.Validate(input, ev.Classifier) != nil || (ev.Classification.Status != domain.InferenceOK && ev.Classification.Status != domain.Abstain) {
		return reject()
	}
	q := domain.SearchQuery{Scope: domain.Playbooks, EncoderVersion: encoder, Limit: 20}
	if ev.Playbooks.Validate(q) != nil || ev.Playbooks.Status != domain.LookupOK {
		return reject()
	}
	if p.history {
		q = domain.SearchQuery{Scope: domain.History, HouseholdID: p.household, SubjectContactID: p.cmd.Binding.SubjectContactID, ExcludeIntentID: p.cmd.IntentID, ConsentVersion: p.consent, Cutoff: s.Clock.Now().Add(-7 * 24 * time.Hour), Now: s.Clock.Now(), EncoderVersion: encoder, Limit: 20}
		if ev.History.Validate(q) != nil || ev.History.Status != domain.LookupOK {
			return reject()
		}
		for _, h := range ev.History.Hits {
			var valid bool
			e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_intents i JOIN intent_consents c ON c.intent_id=i.id AND c.version=i.consent_version JOIN request_messages m ON m.intent_id=i.id AND m.version=i.current_version JOIN intent_versions v ON v.intent_id=i.id AND v.version=i.current_version JOIN search_projections sp ON sp.intent_id=i.id WHERE i.id=$1 AND i.household_id=$2 AND v.subject_contact_id=$3 AND i.current_version=$4 AND i.consent_version=$5 AND sp.revision=$6 AND c.history_allowed AND m.deleted_at IS NULL AND m.expires_at>$7 AND i.created_at BETWEEN $8 AND $7)`, h.SourceID, p.household, p.cmd.Binding.SubjectContactID, h.SourceVersion, h.ConsentVersion, h.ProjectionRevision, s.Clock.Now(), q.Cutoff).Scan(&valid)
			if e != nil {
				return cap, e
			}
			if !valid {
				return reject()
			}
		}
	} else if ev.History.Status != domain.LookupNotApplicable || len(ev.History.Hits) != 0 {
		return reject()
	}
	requested, held, captured, e := s.totals(ctx, tx, p)
	if e != nil {
		return cap, e
	}
	if requested != ev.Requested || held != ev.Held || captured != ev.Captured {
		return reject()
	}
	pattern := false
	if p.history {
		history, err := s.canonicalHistory(ctx, tx, p)
		if err != nil {
			return cap, err
		}
		if history.SnapshotHash == "" || history.SnapshotHash != ev.HistoryHash {
			return cap, domain.Fail("HISTORY_REVIEW_REQUIRED", 409)
		}
		pattern = patternAttention(history)
	}
	return releaseEligibility{pattern: pattern, intent: p.cmd.IntentID, digest: p.cmd.Digest, evidence: evidenceID, version: p.cmd.Version, consent: p.consent, revision: p.revision, requested: requested, held: held, captured: captured}, nil
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

// ReviewApprove is a server-only seam for P03. No HTTP endpoint calls it in P02.
// Persisted P03 evidence and a current authenticated reviewer are mandatory.
func (s *Service) ReviewApprove(ctx context.Context, a domain.Actor, id, evidenceID, rationale, correlation string) error {
	if rationale == "" || len(rationale) > 240 {
		return domain.Fail("REVIEW_REASON_REQUIRED", 422)
	}
	id, e := canonicalID(id)
	if e != nil {
		return e
	}
	if _, e = s.Read(ctx, a, id); e != nil {
		return e
	}
	proof, e := s.validateReleaseSources(ctx, id, evidenceID)
	if e != nil {
		return e
	}
	return s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		p, e := s.paymentLock(ctx, tx, id)
		if e != nil {
			return e
		}
		if e = s.paymentActor(ctx, tx, a, p, false); e != nil {
			return e
		}
		cap, e := s.eligibility(ctx, tx, p, a, evidenceID, proof)
		if e != nil {
			return e
		}
		cap.rationale = rationale
		return s.reserveCapture(ctx, tx, &p, a, cap, correlation)
	})
}
func (s *Service) reserveCapture(ctx context.Context, tx pgx.Tx, p *paymentRow, a domain.Actor, cap releaseEligibility, correlation string) error {
	if cap.intent != p.cmd.IntentID || cap.version != p.cmd.Version || cap.digest != p.cmd.Digest || cap.consent != p.consent || cap.revision != p.revision || cap.evidence == "" || p.cleanup || p.state != domain.Held {
		return domain.Fail("RELEASE_EVIDENCE_REQUIRED", 409)
	}
	review := uuid.NewString()
	reason := "CURRENT_POLICY_ELIGIBLE"
	if cap.pattern {
		reason = "PATTERN_REVIEWED_WITH_RATIONALE"
	}
	if _, e := tx.Exec(ctx, `INSERT INTO reviews(id,intent_id,intent_version,content_digest,reviewer_id,evidence_id,decision,reason_code,created_at,rationale) VALUES($1,$2,$3,$4,$5,$6,'APPROVED',$9,$7,$8)`, review, p.cmd.IntentID, p.cmd.Version, p.cmd.Digest, a.UserID, cap.evidence, s.Clock.Now(), cap.rationale, reason); e != nil {
		return e
	}
	if _, e := tx.Exec(ctx, `UPDATE payment_intents SET review_status='APPROVED' WHERE id=$1`, p.cmd.IntentID); e != nil {
		return e
	}
	if e := s.state(ctx, tx, p, domain.CapturePending, "REVIEW_APPROVED"); e != nil {
		return e
	}
	_, e := s.reserve(ctx, tx, p, "CAPTURE", nil, "", "", correlation)
	return e
}

type paymentOperation struct {
	id, kind, state string
	cmd             domain.ProviderCommand
	created         time.Time
	body            []byte
	hash            string
}

func (s *Service) loadOperation(ctx context.Context, id string) (paymentOperation, error) {
	var o paymentOperation
	o.id = id
	o.cmd.Environment = paypal.Sandbox
	e := s.DB.Pool.QueryRow(ctx, `SELECT o.kind,o.state,o.provider_request_id,o.created_at,o.intent_id::text,o.intent_version,v.content_digest,v.binding_schema,v.amount_minor,v.currency,v.payee_id,v.subject_contact_id::text,v.purpose,COALESCE(i.provider_order_id,''),COALESCE(i.authorization_id,''),COALESCE(i.capture_id,''),o.request_body,o.request_hash FROM payment_operations o JOIN intent_versions v ON v.intent_id=o.intent_id AND v.version=o.intent_version JOIN payment_intents i ON i.id=o.intent_id WHERE o.id=$1`, id).Scan(&o.kind, &o.state, &o.cmd.RequestID, &o.created, &o.cmd.IntentID, &o.cmd.Version, &o.cmd.Digest, &o.cmd.Binding.Schema, &o.cmd.Binding.AmountMinor, &o.cmd.Binding.Currency, &o.cmd.Binding.PayeeID, &o.cmd.Binding.SubjectContactID, &o.cmd.Binding.Purpose, &o.cmd.OrderID, &o.cmd.AuthorizationID, &o.cmd.CaptureID, &o.body, &o.hash)
	o.cmd.Binding.IntentID = o.cmd.IntentID
	o.cmd.Binding.Version = o.cmd.Version
	if e == nil && (domain.Hash(o.body) != o.hash || !bytes.Equal(o.body, paypal.Payload(o.kind, o.cmd))) {
		return o, domain.Fail("PERSISTED_COMMAND_MISMATCH", 409)
	}
	return o, e
}

// A recovery replay keeps the original full/final capture, immutable authorization,
// payload and request ID. PayPal's final_capture contract disallows additional
// captures on that authorization; this does not assume a retention duration for
// the idempotency cache. Provider reads always precede this current PG check.
func (s *Service) captureRecoveryEligible(ctx context.Context, job domain.Job, o paymentOperation) error {
	proof, err := s.validateApprovedSources(ctx, o.cmd.IntentID, o.cmd.Version)
	if err != nil {
		return err
	}
	return s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		p, e := s.paymentLock(ctx, tx, o.cmd.IntentID)
		if e != nil {
			return e
		}
		if e := s.fence(ctx, tx, job); e != nil {
			return e
		}
		if p.cmd.Version != o.cmd.Version || p.cmd.Digest != o.cmd.Digest || p.cmd.AuthorizationID != o.cmd.AuthorizationID || p.cmd.CaptureID != "" || (p.state != domain.CapturePending && p.state != domain.Reconciling && p.state != domain.Held) {
			return domain.Fail("RELEASE_EVIDENCE_REQUIRED", 409)
		}
		var reviewer, evidence string
		if e = tx.QueryRow(ctx, `SELECT reviewer_id::text,evidence_id::text FROM reviews WHERE intent_id=$1 AND intent_version=$2 AND decision='APPROVED' ORDER BY created_at DESC LIMIT 1`, o.cmd.IntentID, o.cmd.Version).Scan(&reviewer, &evidence); e != nil {
			return e
		}
		p.state = domain.Held
		_, e = s.eligibility(ctx, tx, p, domain.Actor{UserID: reviewer, HouseholdID: p.household, Role: domain.Reviewer}, evidence, proof)
		return e
	})
}
func (s *Service) fence(ctx context.Context, tx pgx.Tx, job domain.Job) error {
	var ok bool
	e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM jobs WHERE id=$1 AND lease_token=$2 AND lease_until>$3 AND completed_at IS NULL AND failed_at IS NULL FOR UPDATE)`, job.ID, job.LeaseToken, s.Clock.Now()).Scan(&ok)
	if e != nil {
		return e
	}
	if !ok {
		return domain.Fail("LEASE_CONFLICT", 409)
	}
	return nil
}
func (s *Service) RunPaymentJob(ctx context.Context, job domain.Job) error {
	if s.Provider == nil {
		return domain.Fail("PAYMENTS_UNAVAILABLE", 503)
	}
	if job.Kind == "PAYMENT_RECONCILE" {
		return s.reconcileJob(ctx, job)
	}
	o, e := s.loadOperation(ctx, job.ObjectID)
	if e != nil {
		return e
	}
	if o.state == "CONFIRMED" || o.state == "FAILED" {
		return nil
	}
	dispatch := o.state == "RESERVED"
	var proof sourceValidation
	var sourceErr error
	if dispatch && o.kind == "CAPTURE" {
		proof, sourceErr = s.validateApprovedSources(ctx, o.cmd.IntentID, o.cmd.Version)
	}
	blocked := false
	e = s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		var p paymentRow
		if dispatch && o.kind == "CAPTURE" {
			var e error
			p, e = s.paymentLock(ctx, tx, o.cmd.IntentID)
			if e != nil {
				return e
			}
		}
		if e := s.fence(ctx, tx, job); e != nil {
			return e
		}
		if dispatch && o.kind == "CAPTURE" {
			var reviewer, evidence string
			e := tx.QueryRow(ctx, `SELECT reviewer_id::text,evidence_id::text FROM reviews WHERE intent_id=$1 AND intent_version=$2 AND decision='APPROVED' ORDER BY created_at DESC LIMIT 1`, o.cmd.IntentID, o.cmd.Version).Scan(&reviewer, &evidence)
			check := p
			check.state = domain.Held
			if e == nil && p.state == domain.CapturePending {
				if sourceErr != nil {
					e = sourceErr
				} else {
					_, e = s.eligibility(ctx, tx, check, domain.Actor{UserID: reviewer, HouseholdID: p.household, Role: domain.Reviewer}, evidence, proof)
				}
			} else {
				e = domain.Fail("RELEASE_EVIDENCE_REQUIRED", 409)
			}
			if e != nil {
				blocked = true
				if _, e = tx.Exec(ctx, `UPDATE payment_operations SET state='FAILED',last_error_code='RELEASE_INVALIDATED_BEFORE_DISPATCH' WHERE id=$1`, o.id); e != nil {
					return e
				}
				if p.state == domain.CapturePending {
					if e = s.state(ctx, tx, &p, domain.Held, "RELEASE_INVALIDATED_BEFORE_DISPATCH"); e != nil {
						return e
					}
				}
				if _, e = tx.Exec(ctx, `UPDATE payment_intents SET cleanup_requested=true,review_status='STALE' WHERE id=$1`, p.cmd.IntentID); e != nil {
					return e
				}
				return s.reserveVoid(ctx, tx, &p, "RELEASE_INVALIDATED_BEFORE_DISPATCH")
			}
		}
		_, e := tx.Exec(ctx, `UPDATE payment_operations SET state='DISPATCHED',dispatched_at=COALESCE(dispatched_at,$2),lease_token=$3,lease_until=$4 WHERE id=$1 AND state IN ('RESERVED','DISPATCHED','UNKNOWN')`, o.id, s.Clock.Now(), job.LeaseToken, job.LeaseUntil)
		return e
	})
	if e != nil {
		return e
	}
	if blocked {
		return nil
	}
	var result domain.ProviderResult
	if !dispatch {
		result, e = s.Provider.Reconcile(ctx, o.cmd)
		if e == nil && operationResolved(o.kind, result) {
			return s.finish(ctx, job, o, result, nil)
		}
		if o.kind == "CREATE_ORDER" && o.cmd.OrderID == "" && s.Clock.Now().Sub(o.created) < 10*time.Minute {
			result, e = s.Provider.CreateOrder(ctx, o.cmd)
		} else if o.kind == "AUTHORIZE" && e == nil && result.AuthorizationID == "" && s.Clock.Now().Sub(o.created) < 10*time.Minute {
			result, e = s.Provider.Authorize(ctx, o.cmd)
		} else if o.kind == "VOID" && e == nil && result.Status == "HELD" {
			result, e = s.Provider.Void(ctx, o.cmd)
		} else if o.kind == "CAPTURE" && e == nil && result.Status == "HELD" && result.CaptureID == "" && result.AuthorizationStatus == "CREATED" && result.AuthorizationID == o.cmd.AuthorizationID {
			if checkErr := s.captureRecoveryEligible(ctx, job, o); checkErr != nil {
				return s.finish(ctx, job, o, result, &paypal.Error{Code: "CAPTURE_RECOVERY_EVIDENCE_REQUIRED", Temporary: true, Unknown: true})
			}
			result, e = s.Provider.Capture(ctx, o.cmd)
		} else {
			if e == nil {
				e = &paypal.Error{Code: "PROVIDER_OUTCOME_UNRESOLVED", Temporary: true, Unknown: true}
			}
			return s.finish(ctx, job, o, result, e)
		}
	} else {
		switch o.kind {
		case "CREATE_ORDER":
			result, e = s.Provider.CreateOrder(ctx, o.cmd)
		case "AUTHORIZE":
			result, e = s.Provider.Authorize(ctx, o.cmd)
		case "CAPTURE":
			result, e = s.Provider.Capture(ctx, o.cmd)
		case "VOID":
			result, e = s.Provider.Void(ctx, o.cmd)
		default:
			e = domain.Fail("INVALID_COMMAND", 400)
		}
	}
	return s.finish(ctx, job, o, result, e)
}
func operationResolved(kind string, r domain.ProviderResult) bool {
	switch kind {
	case "CREATE_ORDER":
		return r.OrderID != ""
	case "AUTHORIZE":
		return r.AuthorizationID != "" || r.Status == "CLOSED_UNCAPTURED"
	case "CAPTURE":
		return r.Status == "CAPTURED" || r.Status == "VOIDED" || r.Status == "CLOSED_UNCAPTURED" || (r.Status == "HELD" && (r.CaptureStatus == "DECLINED" || r.CaptureStatus == "FAILED" || r.CaptureStatus == "DENIED"))
	case "VOID":
		return r.Status == "VOIDED" || r.Status == "CAPTURED" || r.Status == "CLOSED_UNCAPTURED"
	}
	return false
}
func (s *Service) applyTruth(ctx context.Context, tx pgx.Tx, p *paymentRow, r domain.ProviderResult) error {
	if !r.Confirmed || r.Environment != paypal.Sandbox || r.PayeeID != p.cmd.Binding.PayeeID || r.AmountMinor != p.cmd.Binding.AmountMinor || r.Currency != p.cmd.Binding.Currency || r.OrderID == "" || (p.cmd.OrderID != "" && p.cmd.OrderID != r.OrderID) || (p.cmd.AuthorizationID != "" && p.cmd.AuthorizationID != r.AuthorizationID) || (p.cmd.CaptureID != "" && r.CaptureID != "" && p.cmd.CaptureID != r.CaptureID) {
		return domain.Fail("PROVIDER_BINDING_MISMATCH", 409)
	}
	if _, e := tx.Exec(ctx, `UPDATE payment_intents SET provider_order_id=$2,authorization_id=COALESCE(NULLIF($3,''),authorization_id),capture_id=COALESCE(NULLIF($4,''),capture_id) WHERE id=$1`, p.cmd.IntentID, r.OrderID, r.AuthorizationID, r.CaptureID); e != nil {
		return e
	}
	if _, e := tx.Exec(ctx, `INSERT INTO provider_observations(id,intent_id,intent_version,environment,order_id,authorization_id,capture_id,amount_minor,currency,payee_id,order_status,authorization_status,capture_status,normalized_status,debug_id,observed_at) VALUES($1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,''),$8,$9,$10,$11,$12,$13,$14,NULLIF($15,''),$16)`, uuid.NewString(), p.cmd.IntentID, p.cmd.Version, r.Environment, r.OrderID, r.AuthorizationID, r.CaptureID, r.AmountMinor, r.Currency, r.PayeeID, r.OrderStatus, r.AuthorizationStatus, r.CaptureStatus, r.Status, r.DebugID, s.Clock.Now()); e != nil {
		return e
	}
	p.cmd.OrderID = r.OrderID
	if r.AuthorizationID != "" {
		p.cmd.AuthorizationID = r.AuthorizationID
	}
	if r.CaptureID != "" {
		p.cmd.CaptureID = r.CaptureID
	}
	target := domain.PaymentStatus(r.Status)
	// A CREATED authorization read cannot disprove a capture with a lost response.
	if target == domain.Held && r.CaptureStatus == "" {
		var unresolved bool
		if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payment_operations WHERE intent_id=$1 AND kind='CAPTURE' AND state IN ('DISPATCHED','UNKNOWN'))`, p.cmd.IntentID).Scan(&unresolved); e != nil {
			return e
		}
		if unresolved {
			target = domain.Reconciling
		}
	}
	if p.state == domain.Captured {
		return nil
	}
	if p.state == domain.Voided || p.state == domain.ClosedUncaptured {
		if target != p.state {
			return domain.Fail("PROVIDER_TERMINAL_CONFLICT", 409)
		}
		return nil
	}
	if target == domain.AwaitingPayer && p.cleanup {
		target = domain.Abandoned
	}
	if p.state == domain.Abandoned && target == domain.Abandoned {
		return nil
	}
	if p.state != target && !domain.CanTransition(p.state, target) {
		if !domain.CanTransition(p.state, domain.Reconciling) {
			return domain.Fail("PROVIDER_STATE_CONFLICT", 409)
		}
		if e := s.state(ctx, tx, p, domain.Reconciling, "PROVIDER_READ"); e != nil {
			return e
		}
	}
	if e := s.state(ctx, tx, p, target, "PROVIDER_"+r.Status); e != nil {
		return e
	}
	if e := s.paymentAudit(ctx, tx, *p, nil, "PROVIDER_TRUTH", r.Status, "worker"); e != nil {
		return e
	}
	if target == domain.Held && (p.cleanup || !s.Clock.Now().Before(p.deadline)) {
		return s.reserveVoid(ctx, tx, p, "CLEANUP_REQUIRED")
	}
	return nil
}
func (s *Service) finish(ctx context.Context, job domain.Job, o paymentOperation, r domain.ProviderResult, providerErr error) error {
	reason, temporary, unknown := "", false, false
	if providerErr != nil {
		reason, temporary, unknown = paypal.Failure(providerErr)
	}
	err := s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		p, e := s.paymentLock(ctx, tx, o.cmd.IntentID)
		if e != nil {
			return e
		}
		if e := s.fence(ctx, tx, job); e != nil {
			return e
		}
		if providerErr == nil {
			if p.cmd.Version != o.cmd.Version {
				return domain.Fail("SUPERSEDED_OPERATION", 409)
			}
			if e = s.applyTruth(ctx, tx, &p, r); e != nil {
				return e
			}
			state := "CONFIRMED"
			if !operationResolved(o.kind, r) {
				state = "UNKNOWN"
			}
			_, e = tx.Exec(ctx, `UPDATE payment_operations SET state=$2,confirmed_at=CASE WHEN $2='CONFIRMED' THEN $3::timestamptz ELSE NULL END,provider_object_id=$4,provider_status=$5,debug_id=$6,last_error_code=NULL WHERE id=$1`, o.id, state, s.Clock.Now(), r.OrderID, r.Status, r.DebugID)
			if e != nil {
				return e
			}
			if state == "UNKNOWN" {
				return s.queue(ctx, tx, "PAYMENT_RECONCILE", p.cmd.IntentID, "reconcile:"+p.cmd.IntentID)
			}
			return nil
		}
		state := "FAILED"
		if temporary || unknown {
			state = "UNKNOWN"
		}
		if _, e = tx.Exec(ctx, `UPDATE payment_operations SET state=$2,last_error_code=$3 WHERE id=$1`, o.id, state, reason); e != nil {
			return e
		}
		if state == "UNKNOWN" && p.state != domain.Captured && p.state != domain.Voided && p.state != domain.ClosedUncaptured {
			if p.state == domain.AwaitingPayer || p.state == domain.Abandoned || p.state == domain.Held || p.state == domain.CapturePending || p.state == domain.VoidPending {
				if e = s.state(ctx, tx, &p, domain.Reconciling, reason); e != nil {
					return e
				}
			}
		}
		if state == "FAILED" && (o.kind == "CAPTURE" || o.kind == "VOID") {
			if e = s.state(ctx, tx, &p, domain.Reconciling, reason); e != nil {
				return e
			}
			if e = s.queue(ctx, tx, "PAYMENT_RECONCILE", p.cmd.IntentID, "reconcile:"+p.cmd.IntentID); e != nil {
				return e
			}
		}
		return s.paymentAudit(ctx, tx, p, nil, "PROVIDER_COMMAND_ERROR", reason, "worker")
	})
	if err != nil {
		return err
	}
	if providerErr != nil {
		return providerErr
	}
	if !operationResolved(o.kind, r) {
		return &paypal.Error{Code: "PROVIDER_PENDING", Temporary: true, Unknown: true}
	}
	return nil
}
func (s *Service) reconcileJob(ctx context.Context, job domain.Job) error {
	var cmd domain.ProviderCommand
	var state domain.PaymentStatus
	e := s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		p, e := s.paymentLock(ctx, tx, job.ObjectID)
		if e != nil {
			return e
		}
		if e := s.fence(ctx, tx, job); e != nil {
			return e
		}
		cmd = p.cmd
		state = p.state
		return nil
	})
	if e != nil {
		return e
	}
	if cmd.OrderID == "" {
		return &paypal.Error{Code: "PROVIDER_ORDER_UNKNOWN", Temporary: true, Unknown: true}
	}
	if cmd.CaptureID == "" {
		var candidate *string
		if e = s.DB.Pool.QueryRow(ctx, `SELECT min(capture_id) FROM paypal_events WHERE mapped_intent=$1 AND capture_id IS NOT NULL HAVING count(DISTINCT capture_id)<=1`, cmd.IntentID).Scan(&candidate); e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if candidate != nil {
			cmd.CaptureID = *candidate
		}
	}
	r, e := s.Provider.Reconcile(ctx, cmd)
	if e != nil {
		return e
	}
	pending := false
	e = s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		p, e := s.paymentLock(ctx, tx, job.ObjectID)
		if e != nil {
			return e
		}
		if e := s.fence(ctx, tx, job); e != nil {
			return e
		}
		if e = s.applyTruth(ctx, tx, &p, r); e != nil {
			return e
		}
		rows, e := tx.Query(ctx, `SELECT id::text,kind FROM payment_operations WHERE intent_id=$1 AND state IN ('UNKNOWN','DISPATCHED')`, job.ObjectID)
		if e != nil {
			return e
		}
		type active struct{ id, kind string }
		ops := []active{}
		for rows.Next() {
			var a active
			if e = rows.Scan(&a.id, &a.kind); e != nil {
				rows.Close()
				return e
			}
			ops = append(ops, a)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, o := range ops {
			if operationResolved(o.kind, r) {
				if _, e = tx.Exec(ctx, `UPDATE payment_operations SET state='CONFIRMED',confirmed_at=$2,provider_status=$3,last_error_code=NULL WHERE id=$1`, o.id, s.Clock.Now(), r.Status); e != nil {
					return e
				}
			}
		}
		if p.state == domain.Held && (p.cleanup || !s.Clock.Now().Before(p.deadline)) {
			if e = s.reserveVoid(ctx, tx, &p, "CLEANUP_REQUIRED"); e != nil {
				return e
			}
		}
		if r.Status == "RECONCILING" || r.Status == "CAPTURE_PENDING" || (state == domain.Reconciling && r.Status == "HELD" && len(ops) > 0) {
			pending = true
		}
		return nil
	})
	if e != nil {
		return e
	}
	// Event ingestion locks events before enqueueing a job. Mark observations
	// after committing payment/job locks to preserve that lock order.
	if _, e = s.DB.Pool.Exec(ctx, `UPDATE paypal_events SET processed_at=$2,result_code='RECONCILED' WHERE mapped_intent=$1 AND processed_at IS NULL`, job.ObjectID, s.Clock.Now()); e != nil {
		return e
	}
	if pending {
		return &paypal.Error{Code: "PROVIDER_PENDING", Temporary: true, Unknown: true}
	}
	return nil
}
func PaymentBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 9 {
		attempt = 9
	}
	d := time.Second * time.Duration(1<<uint(attempt))
	if d > 5*time.Minute {
		return 5 * time.Minute
	}
	return d
}

// TickPayments is independent of ML/search. It also maps early webhook events
// after local order/authorization persistence has caught up.
func (s *Service) TickPayments(ctx context.Context) error {
	if _, e := s.DB.Pool.Exec(ctx, `UPDATE paypal_events SET raw_body=NULL WHERE raw_body IS NOT NULL AND retain_until<=$1`, s.Clock.Now()); e != nil {
		return e
	}
	ids := []string{}
	rows, e := s.DB.Pool.Query(ctx, `SELECT id::text FROM payment_intents WHERE (decision_deadline<=$1 OR cleanup_requested) AND payment_status IN ('AWAITING_PAYER','HELD','CAPTURE_PENDING','VOID_PENDING','RECONCILING') ORDER BY decision_deadline LIMIT 100`, s.Clock.Now())
	if e != nil {
		return e
	}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, id := range ids {
		e = s.DB.WithTx(ctx, func(tx pgx.Tx) error {
			p, e := s.paymentLock(ctx, tx, id)
			if e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, `UPDATE payment_intents SET cleanup_requested=true WHERE id=$1`, id); e != nil {
				return e
			}
			p.cleanup = true
			if p.state == domain.AwaitingPayer {
				return s.queue(ctx, tx, "PAYMENT_RECONCILE", id, "reconcile:"+id)
			}
			return s.reserveVoid(ctx, tx, &p, "APPLICATION_DEADLINE")
		})
		if e != nil {
			return e
		}
	}
	return s.mapWebhookEvents(ctx)
}
func (s *Service) WorkPaymentOnce(ctx context.Context) (bool, error) {
	jobs := &store.Jobs{DB: s.DB, Clock: s.Clock}
	job, e := jobs.Lease(ctx, []string{"PAYMENT_OPERATION", "PAYMENT_RECONCILE"}, time.Minute)
	if e != nil || job == nil {
		return false, e
	}
	e = s.RunPaymentJob(ctx, *job)
	if e == nil {
		return true, jobs.Complete(ctx, *job)
	}
	reason, _, _ := paypal.Failure(e)
	if f := new(domain.Fault); errors.As(e, &f) {
		reason = f.Code
	}
	if retryErr := jobs.Retry(ctx, *job, reason, PaymentBackoff(job.Attempts)); retryErr != nil {
		return true, retryErr
	}
	return true, nil
}
func (s *Service) Diagnostics(ctx context.Context, a domain.Actor, id string) (any, error) {
	i, e := s.Read(ctx, a, id)
	if e != nil {
		return nil, e
	}
	rows, e := s.DB.Pool.Query(ctx, `SELECT o.id::text,o.kind,o.state,o.provider_request_id,o.request_hash,o.environment,o.provider_status,o.last_error_code,o.debug_id,j.attempts,j.lease_until,j.failed_at,j.last_error_code FROM payment_operations o LEFT JOIN jobs j ON j.dedup_key='payment:'||o.id::text WHERE o.intent_id=$1 ORDER BY o.created_at`, i.ID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, kind, state, key, hash, env string
		var status, reason, debug, jobReason *string
		var attempts *int
		var lease, failed *time.Time
		if e = rows.Scan(&id, &kind, &state, &key, &hash, &env, &status, &reason, &debug, &attempts, &lease, &failed, &jobReason); e != nil {
			return nil, e
		}
		out = append(out, map[string]any{"operation_id": id, "kind": kind, "state": state, "provider_request_id": key, "request_hash": hash, "environment": env, "provider_status": status, "error_code": reason, "debug_id": debug, "attempts": attempts, "lease_until": lease, "dead_letter_at": failed, "job_error_code": jobReason})
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	rows.Close()
	var cleanup bool
	var closure *string
	var attempts *int
	var lease, failed *time.Time
	var reason *string
	if e = s.DB.Pool.QueryRow(ctx, `SELECT i.cleanup_requested,i.closure_reason,j.attempts,j.lease_until,j.failed_at,j.last_error_code FROM payment_intents i LEFT JOIN jobs j ON j.dedup_key='reconcile:'||i.id::text WHERE i.id=$1`, i.ID).Scan(&cleanup, &closure, &attempts, &lease, &failed, &reason); e != nil {
		return nil, e
	}
	return map[string]any{"intent_id": i.ID, "version": i.Version, "payment_status": i.Payment, "cleanup_requested": cleanup, "cleanup_overdue": cleanup && !s.Clock.Now().Before(i.DecisionDeadline) && i.Payment != domain.Voided && i.Payment != domain.ClosedUncaptured && i.Payment != domain.Captured, "closure_reason": closure, "operations": out, "reconciliation_job": map[string]any{"attempts": attempts, "lease_until": lease, "dead_letter_at": failed, "error_code": reason}}, nil
}

// ProviderDiagnostic performs authoritative provider GETs only. It has no
// mutation retry, state override, key rotation or money-moving capability.
func (s *Service) ProviderDiagnostic(ctx context.Context, a domain.Actor, id string) (domain.ProviderResult, error) {
	var result domain.ProviderResult
	i, e := s.Read(ctx, a, id)
	if e != nil {
		return result, e
	}
	if s.Provider == nil {
		return result, domain.Fail("PAYMENTS_UNAVAILABLE", 503)
	}
	var cmd domain.ProviderCommand
	e = s.DB.WithTx(ctx, func(tx pgx.Tx) error { p, e := s.paymentLock(ctx, tx, i.ID); cmd = p.cmd; return e })
	if e != nil {
		return result, e
	}
	return s.Provider.Reconcile(ctx, cmd)
}
