package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/ramst/pause/internal/domain"
	"github.com/ramst/pause/internal/investigation"
)

type taskSnapshot struct {
	Binding       domain.Binding `json:"binding"`
	SubjectUserID string         `json:"subject_user_id"`
	CreatedAt     time.Time      `json:"request_created_at"`
	MerchantLabel string         `json:"merchant_label"`
	Digest        string         `json:"content_digest"`
}
type Answer struct {
	ExpectedVersion int    `json:"expected_version"`
	Digest          string `json:"content_digest"`
	SnapshotHash    string `json:"snapshot_hash"`
	Response        string `json:"response"`
}
type ReviewDecision struct {
	ExpectedVersion int    `json:"expected_version"`
	EvidenceID      string `json:"evidence_id"`
	EvidenceHash    string `json:"evidence_hash"`
	Decision        string `json:"decision"`
	Rationale       string `json:"rationale"`
}
type EvidencePacket struct {
	ID               string                   `json:"id"`
	Hash             string                   `json:"evidence_hash"`
	Binding          domain.Binding           `json:"binding"`
	OriginalMessage  string                   `json:"original_message"`
	CreatedAt        time.Time                `json:"request_created_at"`
	MerchantLabel    string                   `json:"merchant_label"`
	Verification     any                      `json:"verification"`
	Provider         any                      `json:"provider"`
	Signals          []string                 `json:"signals"`
	Sources          []investigation.Playbook `json:"sources"`
	Notes            []string                 `json:"notes"`
	Missing          []string                 `json:"missing_checks"`
	ClassifierStatus domain.InferenceStatus   `json:"classifier_status"`
	LookupStatus     domain.LookupStatus      `json:"playbook_status"`
	Attention        bool                     `json:"attention_required"`
	TemplateVersion  string                   `json:"template_version"`
	ReleaseEvidence  releaseEvidence          `json:"-"`
	Receipt          investigation.Receipt    `json:"retrieval_receipt"`
	History          HistoryPacket            `json:"private_history"`
	HistorySummary   string                   `json:"history_summary"`
}

func (s *Service) RequestVerification(ctx context.Context, a domain.Actor, id string, version int, correlation string) error {
	return s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		p, e := s.paymentLock(ctx, tx, id)
		if e != nil {
			return e
		}
		if e = s.paymentActor(ctx, tx, a, p, false); e != nil {
			return e
		}
		if p.cmd.Version != version {
			return domain.Fail("VERSION_CONFLICT", 409)
		}
		var exists bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM verifications WHERE intent_id=$1 AND intent_version=$2)`, id, version).Scan(&exists); e != nil {
			return e
		}
		if exists {
			return nil
		} // Idempotent delivery never renews a task or TTL.
		if p.cmd.Version != version || !s.Clock.Now().Before(p.deadline) || p.cleanup || p.state == domain.Captured || p.state == domain.Voided || p.state == domain.Abandoned || p.state == domain.ClosedUncaptured {
			return domain.Fail("VERIFICATION_CLOSED", 409)
		}
		var subject string
		var created time.Time
		e = tx.QueryRow(ctx, `SELECT c.user_id::text,i.created_at FROM contacts c JOIN memberships m ON m.household_id=c.household_id AND m.user_id=c.user_id AND m.active AND m.role='subject' JOIN payment_intents i ON i.id=$1 WHERE c.id=$2 AND c.household_id=$3 AND c.active`, id, p.cmd.Binding.SubjectContactID, p.household).Scan(&subject, &created)
		if e != nil {
			return domain.Fail("CONTACT_NOT_ALLOWED", 422)
		}
		snapshot := taskSnapshot{Binding: p.cmd.Binding, SubjectUserID: subject, CreatedAt: created, MerchantLabel: s.Config.MerchantLabel, Digest: p.cmd.Digest}
		expires := s.Clock.Now().Add(15 * time.Minute)
		if p.deadline.Before(expires) {
			expires = p.deadline
		}
		tag, e := tx.Exec(ctx, `INSERT INTO verifications(id,intent_id,intent_version,content_digest,subject_user_id,status,expires_at,request_snapshot) VALUES($1,$2,$3,$4,$5,'PENDING',$6,$7) ON CONFLICT(intent_id,intent_version) DO NOTHING`, uuid.NewString(), id, version, p.cmd.Digest, subject, expires, mustJSON(snapshot))
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 1 {
			if _, e = tx.Exec(ctx, `UPDATE payment_intents SET verification_status='PENDING',review_status='PENDING' WHERE id=$1`, id); e != nil {
				return e
			}
			return s.audit(ctx, tx, a, id, "VERIFICATION_REQUESTED", "EXPLICIT_ENROLLED_CONTACT", correlation)
		}
		return nil
	})
}
func (s *Service) RespondVerification(ctx context.Context, a domain.Actor, taskID string, in Answer, correlation string) error {
	// Commit the account-wide attempt before validating task contents. Rejected
	// guesses and transaction rollbacks must not erase the abuse-control counter.
	var attempts int
	e := s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.active(ctx, tx, a, domain.Subject); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `INSERT INTO verification_answer_buckets(user_id,attempts,window_start) VALUES($1,1,$2) ON CONFLICT(user_id) DO UPDATE SET attempts=CASE WHEN verification_answer_buckets.window_start<=$3 THEN 1 ELSE LEAST(verification_answer_buckets.attempts+1,21) END,window_start=CASE WHEN verification_answer_buckets.window_start<=$3 THEN $2 ELSE verification_answer_buckets.window_start END RETURNING attempts`, a.UserID, s.Clock.Now(), s.Clock.Now().Add(-time.Minute)).Scan(&attempts)
	})
	if e != nil {
		return e
	}
	if attempts > 20 {
		return domain.Fail("ANSWER_THROTTLED", 429)
	}
	if in.Response != "CONFIRMED" && in.Response != "DENIED" {
		return domain.Fail("INVALID_RESPONSE", 422)
	}
	taskID, e = canonicalID(taskID)
	if e != nil {
		return e
	}
	return s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		if e := s.active(ctx, tx, a, domain.Subject); e != nil {
			return e
		}
		var id string
		if e := tx.QueryRow(ctx, `SELECT t.intent_id::text FROM verifications t JOIN payment_intents i ON i.id=t.intent_id WHERE t.id=$1 AND t.subject_user_id=$2 AND i.household_id=$3`, taskID, a.UserID, a.HouseholdID).Scan(&id); e != nil {
			return domain.Fail("NOT_FOUND", 404)
		}
		p, e := s.paymentLock(ctx, tx, id)
		if e != nil {
			return e
		}
		// The household/payment lock serializes answers, review and dispatch.
		var version int
		var digest, status string
		var expires time.Time
		var raw []byte
		e = tx.QueryRow(ctx, `SELECT intent_version,content_digest,status,expires_at,request_snapshot FROM verifications WHERE id=$1 AND subject_user_id=$2 FOR UPDATE`, taskID, a.UserID).Scan(&version, &digest, &status, &expires, &raw)
		if e != nil {
			return e
		}
		if status != "PENDING" || version != p.cmd.Version || version != in.ExpectedVersion || digest != in.Digest || digest != p.cmd.Digest || domain.Hash(raw) != in.SnapshotHash || !s.Clock.Now().Before(expires) || !s.Clock.Now().Before(p.deadline) || p.cleanup || p.state == domain.Voided || p.state == domain.Captured || p.state == domain.Abandoned || p.state == domain.ClosedUncaptured {
			return domain.Fail("VERIFICATION_STALE", 409)
		}
		var active bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM contacts c WHERE c.id=$1 AND c.household_id=$2 AND c.user_id=$3 AND c.active)`, p.cmd.Binding.SubjectContactID, p.household, a.UserID).Scan(&active); e != nil {
			return e
		}
		if !active {
			return domain.Fail("FORBIDDEN", 403)
		}
		if _, e = tx.Exec(ctx, `UPDATE verifications SET status=$2,responded_at=$3,response_evidence_hash=$4 WHERE id=$1`, taskID, in.Response, s.Clock.Now(), in.SnapshotHash); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `UPDATE payment_intents SET verification_status=$2 WHERE id=$1`, id, in.Response); e != nil {
			return e
		}
		if e = s.invalidate(ctx, tx, p.household); e != nil {
			return e
		}
		if in.Response == "DENIED" {
			if _, e = tx.Exec(ctx, `UPDATE payment_intents SET cleanup_requested=true WHERE id=$1`, id); e != nil {
				return e
			}
			p.cleanup = true
			if e = s.reserveVoid(ctx, tx, &p, "SUBJECT_DENIED"); e != nil {
				return e
			}
		}
		return s.audit(ctx, tx, a, id, "SUBJECT_RESPONSE", in.Response, correlation)
	})
}
func (s *Service) Analyze(ctx context.Context, a domain.Actor, id string, version int) (any, error) {
	i, e := s.Read(ctx, a, id)
	if e != nil {
		return nil, e
	}
	if version != i.Version {
		return nil, domain.Fail("VERSION_CONFLICT", 409)
	}
	if s.Investigation == nil {
		return nil, domain.Fail("INVESTIGATION_UNAVAILABLE", 503)
	}
	message, e := s.Message(ctx, a, id)
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	engine := s.Investigation
	in := domain.InferenceInput{IntentID: i.ID, IntentVersion: i.Version, ContentDigest: i.Digest, Text: message, Language: "en", ModelVersion: engine.Classifier.ModelVersion}
	c, ce := engine.Classify(ctx, in)
	if ce != nil {
		c = domain.Classification{Status: domain.Unavailable}
	}
	r, receipt, re := engine.Retrieve(ctx, message)
	if re != nil {
		r = domain.SearchResult{SchemaVersion: domain.ContractVersion, Scope: domain.Playbooks, Status: domain.LookupUnavailable, EncoderVersion: engine.Encoder.EncoderVersion, Hits: []domain.SearchHit{}}
	}
	status := "OK"
	prior, history, he := s.historyForAnalysis(ctx, a, id)
	if he != nil {
		return nil, he
	}
	private := domain.SearchResult{SchemaVersion: domain.ContractVersion, Scope: domain.History, Status: domain.LookupNotApplicable, EncoderVersion: engine.Encoder.EncoderVersion, Hits: []domain.SearchHit{}}
	var privateReceipt investigation.Receipt
	if prior.history {
		private, privateReceipt, he = engine.RetrieveHistory(ctx, historyQuery(prior, history, engine.Encoder.EncoderVersion, message), historyPermissions(history))
		if he != nil {
			private.Status = domain.LookupUnavailable
			private.Hits = []domain.SearchHit{}
		}
	}
	if ce != nil || re != nil || he != nil {
		status = "UNAVAILABLE"
	}
	e = s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		p, e := s.paymentLock(ctx, tx, id)
		if e != nil {
			return e
		}
		if e = s.paymentActor(ctx, tx, a, p, false); e != nil {
			return e
		}
		if p.cmd.Version != version || p.cmd.Digest != i.Digest {
			return domain.Fail("VERSION_CONFLICT", 409)
		}
		if p.revision != prior.revision || p.consent != prior.consent {
			return domain.Fail("HISTORY_REVIEW_REQUIRED", 409)
		}
		// Only bounded model/search metadata is retained; never private text/vectors.
		_, e = tx.Exec(ctx, `INSERT INTO investigation_runs(id,intent_id,intent_version,content_digest,message_digest,status,classifier,lookup,receipt,created_at,private_lookup,private_receipt,private_revision) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT(intent_id,intent_version,message_digest) DO UPDATE SET status=EXCLUDED.status,classifier=EXCLUDED.classifier,lookup=EXCLUDED.lookup,receipt=EXCLUDED.receipt,created_at=EXCLUDED.created_at,private_lookup=EXCLUDED.private_lookup,private_receipt=EXCLUDED.private_receipt,private_revision=EXCLUDED.private_revision`, uuid.NewString(), id, version, i.Digest, domain.Hash([]byte(message)), status, mustJSON(c), mustJSON(r), mustJSON(receipt), s.Clock.Now(), mustJSON(private), mustJSON(privateReceipt), prior.revision)
		return e
	})
	if e != nil {
		return nil, e
	}
	return s.Evidence(ctx, a, id)
}
func (s *Service) Evidence(ctx context.Context, a domain.Actor, id string) (EvidencePacket, error) {
	var packet EvidencePacket
	i, e := s.Read(ctx, a, id)
	if e != nil {
		return packet, e
	}
	message, e := s.Message(ctx, a, id)
	if e != nil {
		return packet, e
	}
	if s.Investigation == nil {
		return packet, domain.Fail("INVESTIGATION_UNAVAILABLE", 503)
	}
	engine := s.Investigation
	// Hydrate current public references before taking decision locks. Compare
	// the run bytes again below so concurrent reanalysis cannot reuse this check.
	var liveLookupRaw []byte
	var liveLookup domain.SearchResult
	var liveSources []investigation.Playbook
	liveErr := s.DB.Pool.QueryRow(ctx, `SELECT lookup FROM investigation_runs WHERE intent_id=$1 AND intent_version=$2 AND content_digest=$3 AND message_digest=$4`, id, i.Version, i.Digest, domain.Hash([]byte(message))).Scan(&liveLookupRaw)
	if liveErr == nil {
		liveErr = json.Unmarshal(liveLookupRaw, &liveLookup)
		if liveErr == nil {
			liveSources, liveErr = engine.Revalidate(ctx, liveLookup)
		}
	}
	privateIndex, privateIndexErr := "", error(nil)
	if i.Consent.History {
		privateIndex, privateIndexErr = engine.HistoryIndex(ctx, engine.Config.HistoryAlias)
	}
	e = s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		p, e := s.paymentLock(ctx, tx, id)
		if e != nil {
			return e
		}
		if e = s.paymentActor(ctx, tx, a, p, false); e != nil {
			return e
		}
		if p.cmd.Version != i.Version || p.consent != i.Consent.Version {
			return domain.Fail("VERSION_CONFLICT", 409)
		}
		var cRaw, rRaw, receiptRaw, privateRaw, privateReceiptRaw []byte
		var privateRevision *int64
		e = tx.QueryRow(ctx, `SELECT classifier,lookup,receipt,private_lookup,private_receipt,private_revision FROM investigation_runs WHERE intent_id=$1 AND intent_version=$2 AND content_digest=$3 AND message_digest=$4`, id, i.Version, i.Digest, domain.Hash([]byte(message))).Scan(&cRaw, &rRaw, &receiptRaw, &privateRaw, &privateReceiptRaw, &privateRevision)
		checks := releaseEvidence{Classifier: engine.Classifier, Encoder: engine.Encoder, PolicyVersion: PaymentPolicy, History: domain.SearchResult{SchemaVersion: domain.ContractVersion, Scope: domain.History, Status: domain.LookupNotApplicable, EncoderVersion: engine.Encoder.EncoderVersion, Hits: []domain.SearchHit{}}}
		if p.history {
			checks.History.Status = domain.LookupUnavailable
		}
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if e == nil {
			if json.Unmarshal(cRaw, &checks.Classification) != nil || json.Unmarshal(rRaw, &checks.Playbooks) != nil || json.Unmarshal(receiptRaw, &packet.Receipt) != nil {
				return domain.Fail("INVALID_EVIDENCE", 409)
			}
			if p.history && privateRevision != nil && *privateRevision == p.revision && privateIndexErr == nil {
				var result domain.SearchResult
				if json.Unmarshal(privateRaw, &result) == nil && result.IndexVersion == privateIndex && result.Validate(historyQuery(p, HistoryPacket{WindowStart: s.Clock.Now().Add(-7 * 24 * time.Hour), WindowEnd: s.Clock.Now()}, engine.Encoder.EncoderVersion, message)) == nil {
					checks.History = result
				}
			}
		} else {
			checks.Classification.Status = domain.Unavailable
			checks.Playbooks.Status = domain.LookupUnavailable
		}
		checks.Requested, checks.Held, checks.Captured, e = s.totals(ctx, tx, p)
		if e != nil {
			return e
		}
		packet.History, e = s.canonicalHistory(ctx, tx, p)
		if e != nil {
			return e
		}
		if p.history {
			_ = json.Unmarshal(privateReceiptRaw, &packet.History.Receipt)
			if e = s.hydrateHistoryPacket(ctx, tx, &packet.History, checks.History); e != nil {
				return e
			}
			checks.History = packet.History.Lookup
		}
		checks.HistoryHash = packet.History.SnapshotHash
		packet.HistorySummary = historySummary(packet.History)
		var status string
		var answered *time.Time
		var expiry time.Time
		var subject string
		var verificationSnapshot []byte
		e = tx.QueryRow(ctx, `SELECT status,responded_at,expires_at,subject_user_id::text,request_snapshot FROM verifications WHERE intent_id=$1 AND intent_version=$2 AND content_digest=$3`, id, i.Version, i.Digest).Scan(&status, &answered, &expiry, &subject, &verificationSnapshot)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if errors.Is(e, pgx.ErrNoRows) {
			status = "NOT_REQUESTED"
		}
		if (status == "PENDING" || status == "CONFIRMED") && !s.Clock.Now().Before(expiry) {
			status = "EXPIRED"
		}
		packet.Verification = map[string]any{"status": status, "subject_user_id": subject, "responded_at": answered, "expires_at": expiry, "snapshot_hash": domain.Hash(verificationSnapshot), "scope": "Records the enrolled account's response, not proof that the event happened."}
		packet.Binding = p.cmd.Binding
		packet.CreatedAt = i.CreatedAt
		packet.MerchantLabel = s.Config.MerchantLabel
		packet.TemplateVersion = investigation.TemplateVersion
		packet.Provider = map[string]any{"status": p.state, "order_id": p.cmd.OrderID, "authorization_id": p.cmd.AuthorizationID, "capture_id": p.cmd.CaptureID, "receiving_merchant": p.cmd.Binding.PayeeID}
		packet.Signals = []string{}
		packet.Missing = []string{}
		packet.Notes = []string{"Classifier scores describe message signals, not fraud probabilities.", "Public matches are similar documented patterns, not findings about this author.", "A capture pays the actual sandbox merchant, not the relative named in the story."}
		in := domain.InferenceInput{IntentID: id, IntentVersion: i.Version, ContentDigest: i.Digest, Text: message, Language: "en", ModelVersion: engine.Classifier.ModelVersion}
		usableClassification := checks.Classification.Validate(in, engine.Classifier) == nil && (checks.Classification.Status == domain.InferenceOK || checks.Classification.Status == domain.Abstain)
		if usableClassification {
			for _, l := range checks.Classification.TriggeredLabels {
				packet.Signals = append(packet.Signals, "Signal detected: "+strings.ReplaceAll(l, "_", " ")+".")
			}
		}
		if liveErr == nil && evidenceHash(liveLookupRaw) == evidenceHash(rRaw) {
			packet.Sources = liveSources
		} else {
			packet.Sources = []investigation.Playbook{}
			checks.Playbooks.Status = domain.LookupUnavailable
			checks.Playbooks.Hits = []domain.SearchHit{}
		}
		packet.ClassifierStatus = checks.Classification.Status
		packet.LookupStatus = checks.Playbooks.Status
		packet.Attention = checks.Classification.Status == domain.Abstain || patternAttention(packet.History)
		if !usableClassification {
			packet.Missing = append(packet.Missing, "Usable approved classification")
		}
		if checks.Playbooks.Validate(domain.SearchQuery{Scope: domain.Playbooks, EncoderVersion: engine.Encoder.EncoderVersion, Limit: 20}) != nil || checks.Playbooks.Status != domain.LookupOK || packet.Receipt.CorpusHash != engine.CorpusHash {
			packet.Missing = append(packet.Missing, "Successful approved public lookup")
		}
		if p.history && packet.History.Status != domain.LookupOK {
			packet.Missing = append(packet.Missing, "Successful current private history lookup (run message checks to refresh)")
		}
		if status != "CONFIRMED" {
			packet.Missing = append(packet.Missing, "Current independent subject confirmation")
		}
		checks.Unresolved = len(packet.Missing) > 0
		packet.ReleaseEvidence = checks
		// Persist facts without a second raw-message copy. Authorized reads hydrate
		// the original message under its own retention boundary after this hash.
		packet.ID = uuid.NewString()
		packet.Hash = ""
		matches := packet.History.Matches
		packet.History.Matches = privateEvidenceMatches(packet.History)
		raw := mustJSON(packet)
		stored := map[string]any{}
		_ = json.Unmarshal(raw, &stored)
		for key, value := range map[string]any{"classification": checks.Classification, "classifier": checks.Classifier, "playbooks": checks.Playbooks, "history": checks.History, "history_hash": checks.HistoryHash, "Requested": checks.Requested, "Held": checks.Held, "Captured": checks.Captured, "policy_version": checks.PolicyVersion, "encoder": checks.Encoder, "unresolved": checks.Unresolved} {
			stored[key] = value
		}
		packet.Hash = evidenceHash(mustJSON(stored))
		_, e = tx.Exec(ctx, `INSERT INTO evidence_snapshots(id,intent_id,intent_version,consent_version,history_revision,content_digest,schema_version,policy_version,classifier_version,encoder_version,template_version,evidence,created_at,evidence_hash) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, packet.ID, id, i.Version, p.consent, p.revision, i.Digest, domain.ContractVersion, PaymentPolicy, engine.Classifier.ModelVersion, engine.Encoder.EncoderVersion, investigation.TemplateVersion, mustJSON(stored), s.Clock.Now(), packet.Hash)
		packet.History.Matches = matches
		return e
	})
	packet.OriginalMessage = message
	return packet, e
}

// JSONB reorders keys at every depth. Decode before hashing so typed nested Go
// structs and a PostgreSQL round trip produce the same sorted-key encoding.
func evidenceHash(raw []byte) string {
	var canonical any
	if json.Unmarshal(raw, &canonical) != nil {
		return ""
	}
	return domain.Hash(mustJSON(canonical))
}

type sourceValidation struct{ evidenceID, hash string }

// This proof binds a bounded live dependency check to the exact immutable
// evidence snapshot later re-read under PG locks. It is never supplied by HTTP.
func (s *Service) validateReleaseSources(ctx context.Context, id, evidenceID string) (sourceValidation, error) {
	var proof sourceValidation
	if s.Investigation == nil {
		if s.sourceValidator != nil {
			return s.sourceValidator(ctx, id, evidenceID)
		}
		return proof, domain.Fail("INVESTIGATION_UNAVAILABLE", 503)
	}
	var raw []byte
	if err := s.DB.Pool.QueryRow(ctx, `SELECT evidence FROM evidence_snapshots WHERE id=$1 AND intent_id=$2 AND invalidated_at IS NULL`, evidenceID, id).Scan(&raw); err != nil {
		return proof, domain.Fail("RELEASE_EVIDENCE_REQUIRED", 409)
	}
	var ev releaseEvidence
	if json.Unmarshal(raw, &ev) != nil {
		return proof, domain.Fail("RELEASE_EVIDENCE_REQUIRED", 409)
	}
	if err := s.Investigation.Ready(ctx); err != nil {
		return proof, domain.Fail("INVESTIGATION_UNAVAILABLE", 503)
	}
	if _, err := s.Investigation.Revalidate(ctx, ev.Playbooks); err != nil {
		return proof, domain.Fail("PUBLIC_SOURCES_UNAVAILABLE", 503)
	}
	if ev.History.Status == domain.LookupOK {
		index, err := s.Investigation.HistoryIndex(ctx, s.Investigation.Config.HistoryAlias)
		if err != nil || index != ev.History.IndexVersion {
			return proof, domain.Fail("PRIVATE_SOURCES_UNAVAILABLE", 503)
		}
	}
	return sourceValidation{evidenceID: evidenceID, hash: evidenceHash(raw)}, nil
}

func (s *Service) validateApprovedSources(ctx context.Context, id string, version int) (sourceValidation, error) {
	var evidence string
	if err := s.DB.Pool.QueryRow(ctx, `SELECT evidence_id::text FROM reviews WHERE intent_id=$1 AND intent_version=$2 AND decision='APPROVED'`, id, version).Scan(&evidence); err != nil {
		return sourceValidation{}, err
	}
	return s.validateReleaseSources(ctx, id, evidence)
}
func (s *Service) Review(ctx context.Context, a domain.Actor, id string, in ReviewDecision, correlation string) error {
	if len(strings.TrimSpace(in.Rationale)) < 3 || len(in.Rationale) > 240 {
		return domain.Fail("REVIEW_REASON_REQUIRED", 422)
	}
	if a.Role != domain.Reviewer {
		return domain.Fail("FORBIDDEN", 403)
	}
	if _, err := s.Read(ctx, a, id); err != nil {
		return err
	}
	var proof sourceValidation
	if in.Decision == "APPROVED" {
		var err error
		proof, err = s.validateReleaseSources(ctx, id, in.EvidenceID)
		if err != nil {
			return err
		}
	}
	return s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		p, e := s.paymentLock(ctx, tx, id)
		if e != nil {
			return e
		}
		if e = s.paymentActor(ctx, tx, a, p, false); e != nil {
			return e
		}
		if a.Role != domain.Reviewer || a.UserID != p.reviewer {
			return domain.Fail("FORBIDDEN", 403)
		}
		if in.ExpectedVersion != p.cmd.Version {
			return domain.Fail("VERSION_CONFLICT", 409)
		}
		var decided bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM reviews WHERE intent_id=$1 AND intent_version=$2)`, id, p.cmd.Version).Scan(&decided); e != nil {
			return e
		}
		if decided {
			return domain.Fail("REVIEW_ALREADY_RECORDED", 409)
		}
		var hash string
		var raw []byte
		e = tx.QueryRow(ctx, `SELECT evidence_hash,evidence FROM evidence_snapshots WHERE id=$1 AND intent_id=$2 AND intent_version=$3 AND invalidated_at IS NULL`, in.EvidenceID, id, p.cmd.Version).Scan(&hash, &raw)
		var canonical map[string]any
		_ = json.Unmarshal(raw, &canonical)
		if e != nil || hash != in.EvidenceHash || hash != domain.Hash(mustJSON(canonical)) {
			return domain.Fail("EVIDENCE_CONFLICT", 409)
		}
		if in.Decision == "APPROVED" {
			cap, e := s.eligibility(ctx, tx, p, a, in.EvidenceID, proof)
			if e != nil {
				return e
			}
			cap.rationale = strings.TrimSpace(in.Rationale)
			return s.reserveCapture(ctx, tx, &p, a, cap, correlation)
		}
		if in.Decision != "DECLINED" {
			return domain.Fail("INVALID_REVIEW", 422)
		}
		if _, e = tx.Exec(ctx, `INSERT INTO reviews(id,intent_id,intent_version,content_digest,reviewer_id,evidence_id,decision,reason_code,created_at,rationale) VALUES($1,$2,$3,$4,$5,$6,'DECLINED','REVIEWER_DECLINED',$7,$8)`, uuid.NewString(), id, p.cmd.Version, p.cmd.Digest, a.UserID, in.EvidenceID, s.Clock.Now(), in.Rationale); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `UPDATE payment_intents SET review_status='DECLINED',cleanup_requested=true WHERE id=$1`, id); e != nil {
			return e
		}
		p.cleanup = true
		return s.reserveVoid(ctx, tx, &p, "REVIEWER_DECLINED")
	})
}

// ApproveReleases is an explicit offline operator step. Never called on startup.
func (s *Service) ApproveReleases(ctx context.Context) error {
	if s.Investigation == nil {
		return errors.New("investigation not configured")
	}
	en := s.Investigation
	return s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		for _, m := range []struct {
			kind, version, hash string
			manifest            any
		}{{"classifier", en.Classifier.ModelVersion, en.Classifier.ArtifactHash, en.Classifier}, {"encoder", en.Encoder.EncoderVersion, en.Encoder.ArtifactHash, en.Encoder}} {
			_, e := tx.Exec(ctx, `INSERT INTO model_releases(id,kind,version,manifest,artifact_hash,approved_at) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(kind,version) DO UPDATE SET manifest=EXCLUDED.manifest,artifact_hash=EXCLUDED.artifact_hash,approved_at=EXCLUDED.approved_at`, uuid.NewString(), m.kind, m.version, mustJSON(m.manifest), m.hash, s.Clock.Now())
			if e != nil {
				return e
			}
		}
		_, e := tx.Exec(ctx, `INSERT INTO payment_evidence_config(id,classifier_version,encoder_version,template_version,policy_version,revision) VALUES(1,$1,$2,$3,$4,1) ON CONFLICT(id) DO UPDATE SET classifier_version=EXCLUDED.classifier_version,encoder_version=EXCLUDED.encoder_version,template_version=EXCLUDED.template_version,policy_version=EXCLUDED.policy_version,revision=payment_evidence_config.revision+1`, en.Classifier.ModelVersion, en.Encoder.EncoderVersion, investigation.TemplateVersion, PaymentPolicy)
		return e
	})
}
