package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

var envName = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
var providerID = regexp.MustCompile(`^[A-Za-z0-9]{6,64}$`)

type scenario struct {
	ID          string `json:"id"`
	SubjectID   string `json:"subject_id"`
	Subject     string `json:"claimed_subject"`
	Purpose     string `json:"purpose"`
	Message     string `json:"synthetic_message"`
	AmountMinor int64  `json:"amount_minor"`
	Terminal    string `json:"terminal_action"`
}

var scenarios = []scenario{
	{ID: "confirmed-purchase", SubjectID: "synthetic-alex", Subject: "Alex (synthetic relative)", Purpose: "Travel kit purchase", Message: "Hi Grandma, it's Alex. Please buy a travel kit for $40 USD from the sandbox store. You can check with me through my enrolled account before paying.", AmountMinor: 4000, Terminal: "capture"},
	{ID: "suspicious-request", SubjectID: "synthetic-alex", Subject: "Alex (synthetic relative)", Purpose: "Unverified emergency kit purchase", Message: "Grandma, it's Alex. Please pay $900 USD for an emergency kit right now. Keep this secret and don't call me.", AmountMinor: 90000, Terminal: "void"},
}

type operation struct {
	Kind        string    `json:"kind"`
	RequestID   string    `json:"request_id"`
	Body        string    `json:"request_body,omitempty"`
	RequestHash string    `json:"request_sha256"`
	StartedAt   time.Time `json:"started_at"`
	Attempts    int       `json:"attempts"`
	HTTPStatus  int       `json:"http_status,omitempty"`
	DebugID     string    `json:"provider_debug_id,omitempty"`
	Outcome     string    `json:"outcome"`
}

type run struct {
	ID                       string                `json:"id"`
	Scenario                 scenario              `json:"scenario"`
	Version                  int                   `json:"version"`
	Digest                   string                `json:"content_digest"`
	MerchantID               string                `json:"actual_merchant_id"`
	MerchantLabel            string                `json:"actual_merchant_label"`
	Currency                 string                `json:"currency"`
	CreatedAt                time.Time             `json:"created_at"`
	Deadline                 time.Time             `json:"application_deadline"`
	State                    string                `json:"state"`
	OrderID                  string                `json:"order_id,omitempty"`
	OrderStatus              string                `json:"order_status,omitempty"`
	AuthorizationID          string                `json:"authorization_id,omitempty"`
	CaptureID                string                `json:"capture_id,omitempty"`
	AuthorizationStatus      string                `json:"authorization_status,omitempty"`
	CaptureStatus            string                `json:"capture_status,omitempty"`
	AuthorizedWithoutCapture bool                  `json:"authorized_without_capture"`
	DuplicateCreateSameID    bool                  `json:"duplicate_create_same_id"`
	Pending                  string                `json:"pending_operation,omitempty"`
	LastError                string                `json:"last_error,omitempty"`
	Operations               map[string]*operation `json:"operations"`
}

type state struct {
	SchemaVersion int             `json:"schema_version"`
	Environment   string          `json:"environment"`
	Runs          map[string]*run `json:"runs"`
}

type diskStore struct {
	dir  string
	data state
}

func openStore(dir string) (*diskStore, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, errors.New("cannot create local evidence directory")
	}
	s := &diskStore{dir: dir, data: state{SchemaVersion: 1, Environment: "paypal-sandbox", Runs: map[string]*run{}}}
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err == nil {
		if json.Unmarshal(b, &s.data) != nil || s.data.SchemaVersion != 1 || s.data.Environment != "paypal-sandbox" || s.data.Runs == nil {
			return nil, errors.New("local state is invalid; preserve it for investigation instead of overwriting it")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("cannot read local state")
	}
	return s, nil
}

func atomicJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".p00-*.tmp")
	if err != nil {
		return errors.New("cannot create evidence temporary file")
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(append(b, '\n'))
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		return errors.New("cannot persist evidence; stop mutations and inspect local storage")
	}
	return nil
}

func (s *diskStore) save() error { return atomicJSON(filepath.Join(s.dir, "state.json"), s.data) }
func (s *diskStore) receipt(value any) error {
	return atomicJSON(filepath.Join(s.dir, "receipt-"+newID()+".json"), value)
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("secure randomness unavailable")
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func hash(b []byte) string   { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }
func usd(cents int64) string { return fmt.Sprintf("%d.%02d", cents/100, cents%100) }

func newRun(c config, selected scenario, now time.Time) *run {
	r := &run{ID: newID(), Scenario: selected, Version: 1, MerchantID: c.MerchantID, MerchantLabel: c.MerchantLabel, Currency: "USD", CreatedAt: now, Deadline: now.Add(time.Duration(c.DeadlineMinutes) * time.Minute), State: "DRAFT", Operations: map[string]*operation{}}
	// Canonical P00 binding: fixed-order JSON struct, UTF-8, integer cents.
	// Freeze the final product contract with cross-language fixtures in P01.
	b, _ := json.Marshal(struct {
		Version                                  int
		AmountMinor                              int64
		Currency, MerchantID, SubjectID, Purpose string
	}{r.Version, selected.AmountMinor, r.Currency, r.MerchantID, selected.SubjectID, selected.Purpose})
	r.Digest = hash(b)
	return r
}
