package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/ramst/pause/internal/domain"
)

const intentSelect = `SELECT i.id::text,i.current_version,i.payment_status,i.verification_status,i.review_status,i.created_at,i.decision_deadline,v.binding_schema,v.amount_minor,v.currency,v.payee_id,v.subject_contact_id::text,v.purpose,v.content_digest,c.version,c.history_allowed,c.training_allowed,c.recorded_at,m.expires_at
 FROM payment_intents i JOIN intent_versions v ON v.intent_id=i.id AND v.version=i.current_version JOIN intent_consents c ON c.intent_id=i.id AND c.version=i.consent_version JOIN request_messages m ON m.intent_id=i.id AND m.version=i.current_version JOIN memberships member ON member.household_id=i.household_id AND member.user_id=$2 AND member.role=$3 AND member.active
 WHERE i.id=$1 AND i.household_id=$4 AND (($3='senior' AND i.creator_id=$2) OR ($3='reviewer' AND i.reviewer_id=$2))`

func (s *Service) Read(ctx context.Context, a domain.Actor, id string) (domain.Intent, error) {
	var i domain.Intent
	parsed, parseErr := uuid.Parse(id)
	if parseErr != nil {
		return i, domain.Fail("NOT_FOUND", 404)
	}
	id = parsed.String()
	e := s.DB.Pool.QueryRow(ctx, intentSelect, id, a.UserID, a.Role, a.HouseholdID).Scan(&i.ID, &i.Version, &i.Payment, &i.Verification, &i.Review, &i.CreatedAt, &i.DecisionDeadline, &i.Binding.Schema, &i.Binding.AmountMinor, &i.Binding.Currency, &i.Binding.PayeeID, &i.Binding.SubjectContactID, &i.Binding.Purpose, &i.Digest, &i.Consent.Version, &i.Consent.History, &i.Consent.Training, &i.Consent.UpdatedAt, &i.MessageExpiresAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return i, domain.Fail("NOT_FOUND", 404)
	}
	if e != nil {
		return i, e
	}
	i.Binding.IntentID = i.ID
	i.Binding.Version = i.Version
	if e = s.DB.Pool.QueryRow(ctx, `SELECT COALESCE((SELECT CASE kind WHEN 'CAPTURE' THEN 'A previous capture attempt did not complete. Check the current provider outcome and review the request again before retrying.' WHEN 'VOID' THEN 'Cancellation could not be confirmed. The authorization is still being checked; do not assume funds have been released.' ELSE 'A checkout attempt did not complete. Retry this saved request or request cancellation; do not create a replacement payment.' END FROM payment_operations WHERE intent_id=$1 AND state='FAILED' ORDER BY created_at DESC LIMIT 1),'')`, i.ID).Scan(&i.PaymentNotice); e != nil {
		return i, e
	}
	if e = s.DB.Pool.QueryRow(ctx, `SELECT u.display_name FROM contacts c JOIN users u ON u.id=c.user_id WHERE c.id=$1 AND c.household_id=$2`, i.Binding.SubjectContactID, a.HouseholdID).Scan(&i.SubjectName); e != nil {
		return i, e
	}
	i.Checkout = domain.CheckoutDTO{Status: "UNAVAILABLE", ReasonCode: "PAYMENTS_NOT_IMPLEMENTED"}
	if s.Provider != nil {
		i.Checkout.Status = "AVAILABLE"
		i.Checkout.ReasonCode = "SANDBOX_AUTHORIZATION_ONLY"
	}
	if e = s.DB.Pool.QueryRow(ctx, `SELECT cleanup_requested FROM payment_intents WHERE id=$1`, id).Scan(&i.CleanupRequested); e != nil {
		return i, e
	}
	var seedScenario, seedStatus *string
	if e = s.DB.Pool.QueryRow(ctx, `SELECT i.provider_order_id,i.authorization_id,i.capture_id,h.history_revision,CASE WHEN (t.status='PENDING' OR (t.status='CONFIRMED' AND i.payment_status NOT IN ('CAPTURED','VOIDED','CLOSED_UNCAPTURED','ABANDONED'))) AND t.expires_at<=$2 THEN 'EXPIRED' ELSE COALESCE(t.status,i.verification_status) END,seed.scenario,seed.status FROM payment_intents i JOIN households h ON h.id=i.household_id LEFT JOIN verifications t ON t.intent_id=i.id AND t.intent_version=i.current_version LEFT JOIN synthetic_history seed ON seed.intent_id=i.id WHERE i.id=$1`, id, s.Clock.Now()).Scan(&i.Checkout.ProviderOrderID, &i.Checkout.AuthorizationID, &i.Checkout.CaptureID, &i.HistoryRevision, &i.Verification, &seedScenario, &seedStatus); e != nil {
		return i, e
	}
	if seedScenario != nil && seedStatus != nil {
		i.SyntheticHistory = &domain.SyntheticHistory{Scenario: *seedScenario, Status: *seedStatus}
	}
	history := "NOT_APPLICABLE"
	if i.Consent.History {
		history = "UNAVAILABLE"
	}
	i.Evidence = domain.EvidenceDTO{SchemaVersion: domain.ContractVersion, Status: "UNAVAILABLE", ClassifierStatus: "UNAVAILABLE", PlaybookStatus: "UNAVAILABLE", HistoryStatus: history, ReasonCode: "INVESTIGATION_NOT_IMPLEMENTED"}
	if s.Investigation != nil {
		i.Evidence.ReasonCode = "ANALYZE_REQUIRED"
		i.Evidence.Status = "PENDING"
	}
	return i, nil
}
func (s *Service) List(ctx context.Context, a domain.Actor) ([]domain.Intent, error) {
	if a.Role != domain.Senior && a.Role != domain.Reviewer {
		return nil, domain.Fail("FORBIDDEN", 403)
	}
	rows, e := s.DB.Pool.Query(ctx, `SELECT i.id::text FROM payment_intents i JOIN memberships m ON m.household_id=i.household_id AND m.user_id=$1 AND m.role=$2 AND m.active WHERE i.household_id=$3 AND (($2='senior' AND i.creator_id=$1) OR ($2='reviewer' AND i.reviewer_id=$1)) ORDER BY i.created_at DESC,i.id LIMIT 100`, a.UserID, a.Role, a.HouseholdID)
	if e != nil {
		return nil, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return nil, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	out := []domain.Intent{}
	for _, id := range ids {
		i, e := s.Read(ctx, a, id)
		if e != nil {
			return nil, e
		}
		out = append(out, i)
	}
	return out, nil
}
func (s *Service) Message(ctx context.Context, a domain.Actor, id string) (string, error) {
	i, e := s.Read(ctx, a, id)
	if e != nil {
		return "", e
	}
	id = i.ID
	var ciphertext []byte
	e = s.DB.Pool.QueryRow(ctx, `SELECT ciphertext FROM request_messages WHERE intent_id=$1 AND version=$2 AND deleted_at IS NULL AND expires_at>$3`, id, i.Version, s.Clock.Now()).Scan(&ciphertext)
	if errors.Is(e, pgx.ErrNoRows) {
		return "", domain.Fail("MESSAGE_EXPIRED", 410)
	}
	if e != nil {
		return "", e
	}
	return s.Box.Decrypt(ciphertext, fmt.Sprintf("%s:%d", i.ID, i.Version))
}
func (s *Service) Contacts(ctx context.Context, a domain.Actor) ([]domain.Contact, error) {
	if a.Role != domain.Senior {
		return nil, domain.Fail("FORBIDDEN", 403)
	}
	rows, e := s.DB.Pool.Query(ctx, `SELECT c.id::text,u.display_name,c.relationship FROM contacts c JOIN users u ON u.id=c.user_id JOIN memberships subject ON subject.household_id=c.household_id AND subject.user_id=c.user_id AND subject.active AND subject.role='subject' JOIN memberships senior ON senior.household_id=c.household_id AND senior.user_id=$2 AND senior.active AND senior.role='senior' WHERE c.household_id=$1 AND c.active ORDER BY u.display_name,c.id`, a.HouseholdID, a.UserID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Contact{}
	for rows.Next() {
		var c domain.Contact
		if e = rows.Scan(&c.ID, &c.Name, &c.Relationship); e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Service) Tasks(ctx context.Context, a domain.Actor) ([]domain.Task, error) {
	if a.Role != domain.Subject {
		return nil, domain.Fail("FORBIDDEN", 403)
	}
	rows, e := s.DB.Pool.Query(ctx, `SELECT t.id::text,i.id::text,t.intent_version,t.content_digest,t.status,t.expires_at,i.payment_status,i.cleanup_requested,i.decision_deadline,v.binding_schema,v.amount_minor,v.currency,v.payee_id,v.subject_contact_id::text,v.purpose FROM verifications t JOIN payment_intents i ON i.id=t.intent_id AND i.current_version=t.intent_version JOIN intent_versions v ON v.intent_id=t.intent_id AND v.version=t.intent_version JOIN contacts c ON c.id=v.subject_contact_id AND c.household_id=i.household_id AND c.user_id=t.subject_user_id AND c.active JOIN memberships m ON m.household_id=i.household_id AND m.user_id=$1 AND m.role='subject' AND m.active WHERE t.subject_user_id=$1 AND i.household_id=$2 ORDER BY t.expires_at DESC LIMIT 100`, a.UserID, a.HouseholdID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Task{}
	for rows.Next() {
		var t domain.Task
		var payment domain.PaymentStatus
		var cleanup bool
		var deadline time.Time
		if e = rows.Scan(&t.ID, &t.IntentID, &t.Version, &t.Digest, &t.Status, &t.ExpiresAt, &payment, &cleanup, &deadline, &t.Binding.Schema, &t.Binding.AmountMinor, &t.Binding.Currency, &t.Binding.PayeeID, &t.Binding.SubjectContactID, &t.Binding.Purpose); e != nil {
			return nil, e
		}
		t.Binding.IntentID = t.IntentID
		t.Binding.Version = t.Version
		var raw []byte
		if e = s.DB.Pool.QueryRow(ctx, `SELECT request_snapshot FROM verifications WHERE id=$1`, t.ID).Scan(&raw); e != nil {
			return nil, e
		}
		var snapshot taskSnapshot
		if json.Unmarshal(raw, &snapshot) != nil {
			return nil, domain.Fail("INVALID_TASK", 409)
		}
		t.SnapshotHash = domain.Hash(raw)
		t.CreatedAt = snapshot.CreatedAt
		t.MerchantLabel = snapshot.MerchantLabel
		closed := payment == domain.Captured || payment == domain.Voided || payment == domain.ClosedUncaptured || payment == domain.Abandoned
		t.CanDeny = t.Status == "PENDING" && !closed && !cleanup && s.Clock.Now().Before(deadline)
		if (t.Status == "PENDING" || (t.Status == "CONFIRMED" && !closed)) && !s.Clock.Now().Before(t.ExpiresAt) {
			t.Status = "EXPIRED"
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
