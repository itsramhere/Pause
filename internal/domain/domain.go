package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"
)

type Clock interface{ Now() time.Time }
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now().UTC() }

type Role string

const (
	Senior   Role = "senior"
	Subject  Role = "subject"
	Reviewer Role = "reviewer"
)

type Actor struct {
	UserID      string `json:"user_id"`
	HouseholdID string `json:"household_id"`
	Name        string `json:"name"`
	Role        Role   `json:"role"`
}
type Fault struct {
	Code   string
	Status int
}

func (e *Fault) Error() string           { return e.Code }
func Fail(code string, status int) error { return &Fault{code, status} }

var moneyPattern = regexp.MustCompile(`^[0-9]{1,16}(\.[0-9]{1,2})?$`)

func ParseUSD(s, currency string, max int64) (int64, error) {
	if currency != "USD" || len(s) > 20 || !moneyPattern.MatchString(s) {
		return 0, Fail("INVALID_AMOUNT", 422)
	}
	parts := strings.Split(s, ".")
	dollars, e := strconv.ParseInt(parts[0], 10, 64)
	if e != nil || dollars > 92233720368547758 {
		return 0, Fail("INVALID_AMOUNT", 422)
	}
	cents := int64(0)
	if len(parts) == 2 {
		f := parts[1]
		if len(f) == 1 {
			f += "0"
		}
		cents, _ = strconv.ParseInt(f, 10, 64)
	}
	if dollars > (9223372036854775807-cents)/100 {
		return 0, Fail("INVALID_AMOUNT", 422)
	}
	total := dollars*100 + cents
	if total <= 0 || total > max {
		return 0, Fail("INVALID_AMOUNT", 422)
	}
	return total, nil
}

// BindingV1 uses NFC, collapsed Unicode whitespace, lowercase canonical UUIDs,
// exact case-sensitive payee and purpose, and ordered JSON with integer cents.
// Message text is deliberately not a financial authority.
type Binding struct {
	Schema           string `json:"schema"`
	IntentID         string `json:"intent_id"`
	Version          int    `json:"version"`
	AmountMinor      int64  `json:"amount_minor"`
	Currency         string `json:"currency"`
	PayeeID          string `json:"payee_id"`
	SubjectContactID string `json:"subject_contact_id"`
	Purpose          string `json:"purpose"`
}

func NormalizePurpose(s string) (string, error) {
	if !utf8.ValidString(s) || strings.IndexFunc(s, func(r rune) bool { return unicode.IsControl(r) && !unicode.IsSpace(r) }) >= 0 {
		return "", Fail("INVALID_PURPOSE", 422)
	}
	s = strings.Join(strings.Fields(norm.NFC.String(s)), " ")
	if utf8.RuneCountInString(s) < 3 || utf8.RuneCountInString(s) > 240 {
		return "", Fail("INVALID_PURPOSE", 422)
	}
	return s, nil
}
func (b Binding) Canonical() (Binding, error) {
	i, e := uuid.Parse(b.IntentID)
	if e != nil {
		return b, errors.New("invalid binding intent")
	}
	s, e := uuid.Parse(b.SubjectContactID)
	if e != nil {
		return b, errors.New("invalid binding contact")
	}
	b.IntentID = i.String()
	b.SubjectContactID = s.String()
	b.Schema = "pause-binding-v1"
	b.Purpose, e = NormalizePurpose(b.Purpose)
	if e != nil || b.Version < 1 || b.AmountMinor <= 0 || b.AmountMinor > 500000 || b.Currency != "USD" || b.PayeeID == "" || len(b.PayeeID) > 128 {
		return b, errors.New("invalid binding")
	}
	return b, nil
}
func (b Binding) Digest() (string, error) {
	b, e := b.Canonical()
	if e != nil {
		return "", e
	}
	raw, e := json.Marshal(b)
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:]), nil
}
func Hash(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }

type PaymentStatus string

const (
	Draft            PaymentStatus = "DRAFT"
	AwaitingPayer    PaymentStatus = "AWAITING_PAYER"
	Held             PaymentStatus = "HELD"
	CapturePending   PaymentStatus = "CAPTURE_PENDING"
	VoidPending      PaymentStatus = "VOID_PENDING"
	Reconciling      PaymentStatus = "RECONCILING"
	Captured         PaymentStatus = "CAPTURED"
	Voided           PaymentStatus = "VOIDED"
	ClosedUncaptured PaymentStatus = "CLOSED_UNCAPTURED"
	Abandoned        PaymentStatus = "ABANDONED"
)

func CanTransition(from, to PaymentStatus) bool {
	next := map[PaymentStatus][]PaymentStatus{Draft: {AwaitingPayer, Abandoned}, AwaitingPayer: {Held, Abandoned, Reconciling, ClosedUncaptured}, Held: {CapturePending, VoidPending, Reconciling, ClosedUncaptured}, CapturePending: {Captured, Held, Reconciling}, VoidPending: {Voided, Reconciling}, Reconciling: {Captured, Voided, Held, ClosedUncaptured, Abandoned, AwaitingPayer}, Abandoned: {Reconciling}}
	for _, n := range next[from] {
		if n == to {
			return true
		}
	}
	return false
}

type VerificationStatus string
type ReviewStatus string

const (
	VerificationNotRequested VerificationStatus = "NOT_REQUESTED"
	ReviewNotRequested       ReviewStatus       = "NOT_REQUESTED"
)

type Consent struct {
	Version   int       `json:"version"`
	History   bool      `json:"history"`
	Training  bool      `json:"training"`
	UpdatedAt time.Time `json:"updated_at"`
}
type CheckoutDTO struct {
	Status          string  `json:"status"`
	ProviderOrderID *string `json:"provider_order_id"`
	AuthorizationID *string `json:"authorization_id"`
	CaptureID       *string `json:"capture_id"`
	ReasonCode      string  `json:"reason_code"`
}
type EvidenceDTO struct {
	SchemaVersion    string  `json:"schema_version"`
	Status           string  `json:"status"`
	ClassifierStatus string  `json:"classifier_status"`
	PlaybookStatus   string  `json:"playbook_status"`
	HistoryStatus    string  `json:"history_status"`
	SnapshotID       *string `json:"snapshot_id"`
	ReasonCode       string  `json:"reason_code"`
}
type Intent struct {
	ID               string             `json:"id"`
	Version          int                `json:"version"`
	Binding          Binding            `json:"binding"`
	Digest           string             `json:"content_digest"`
	Payment          PaymentStatus      `json:"payment_status"`
	Verification     VerificationStatus `json:"verification_status"`
	Review           ReviewStatus       `json:"review_status"`
	Consent          Consent            `json:"consent"`
	CreatedAt        time.Time          `json:"created_at"`
	DecisionDeadline time.Time          `json:"decision_deadline"`
	MessageExpiresAt time.Time          `json:"message_expires_at"`
	Checkout         CheckoutDTO        `json:"checkout"`
	Evidence         EvidenceDTO        `json:"evidence"`
}
type Task struct {
	ID        string    `json:"id"`
	IntentID  string    `json:"intent_id"`
	Version   int       `json:"version"`
	Digest    string    `json:"content_digest"`
	Binding   Binding   `json:"binding"`
	Status    string    `json:"status"`
	ExpiresAt time.Time `json:"expires_at"`
}
type Contact struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Relationship string `json:"relationship"`
}
