package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ramst/pause/internal/config"
	"github.com/ramst/pause/internal/domain"
	"github.com/ramst/pause/internal/paypal"
	"github.com/ramst/pause/internal/secure"
	"github.com/ramst/pause/internal/store"
	"github.com/ramst/pause/migrations"
)

type paymentClock struct{ now time.Time }

func (c *paymentClock) Now() time.Time { return c.now }

type paymentFixture struct {
	s      *Service
	p      *fakeProvider
	clock  *paymentClock
	schema string
}

func setupPayments(t *testing.T) *paymentFixture {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required for real PostgreSQL payment tests")
	}
	ctx := context.Background()
	admin, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := "p02_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, e = admin.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	cfg, _ := pgxpool.ParseConfig(dsn)
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		pool.Close()
		_, e := admin.Exec(ctx, `DROP SCHEMA `+pgx.Identifier{schema}.Sanitize()+` CASCADE`)
		if e != nil {
			t.Error(e)
		}
		admin.Close()
	})
	if e = migrations.Apply(ctx, pool); e != nil {
		t.Fatal(e)
	}
	clock := &paymentClock{time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)}
	db := &store.Store{Pool: pool}
	if e = db.Seed(ctx, "test", "Synthetic-fixture-password!", clock); e != nil {
		t.Fatal(e)
	}
	box, _ := secure.NewBox(bytes.Repeat([]byte{42}, 32))
	p := &fakeProvider{states: map[string]domain.ProviderResult{}, calls: map[string]int{}, keys: map[string]string{}, captures: map[string]int{}}
	s := &Service{DB: db, Clock: clock, Box: box, Provider: p, Config: config.Config{MerchantID: "FIXTURE_MERCHANT", MaxAmount: 500000, DecisionTTL: 30 * time.Minute, MessageRetention: 7 * 24 * time.Hour, OperationRetention: 24 * time.Hour}}
	// Explicit dependency fixture, available only from package test code. Product
	// startup has no equivalent switch and rejects a missing investigation engine.
	installFixtureSourceValidator(s)
	return &paymentFixture{s, p, clock, schema}
}
func installFixtureSourceValidator(s *Service) {
	s.sourceValidator = func(ctx context.Context, id, evidenceID string) (sourceValidation, error) {
		var raw []byte
		err := s.DB.Pool.QueryRow(ctx, `SELECT evidence FROM evidence_snapshots WHERE id=$1 AND intent_id=$2`, evidenceID, id).Scan(&raw)
		return sourceValidation{evidenceID: evidenceID, hash: evidenceHash(raw)}, err
	}
}
func actor(role domain.Role) domain.Actor {
	return domain.Actor{UserID: store.SeedID(string(role) + "-1"), HouseholdID: store.SeedID("household-1"), Role: role}
}
func (f *paymentFixture) draft(t *testing.T) domain.Intent {
	t.Helper()
	no := false
	i, _, e := f.s.Create(context.Background(), actor(domain.Senior), Intake{Amount: "40.01", Currency: "USD", MerchantID: "FIXTURE_MERCHANT", SubjectContactID: store.SeedID("contact-subject-1"), Purpose: "Synthetic payment test", Message: "Synthetic message; fixture only", HistoryConsent: &no}, uuid.NewString(), "fixture")
	if e != nil {
		t.Fatal(e)
	}
	return i
}
func (f *paymentFixture) work(t *testing.T) {
	t.Helper()
	for n := 0; n < 15; n++ {
		worked, e := f.s.WorkPaymentOnce(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		if !worked {
			return
		}
	}
}
func (f *paymentFixture) held(t *testing.T) domain.Intent {
	t.Helper()
	i := f.draft(t)
	_, e := f.s.ReserveCheckout(context.Background(), actor(domain.Senior), i.ID, "CREATE_ORDER", uuid.NewString(), PaymentAction{ExpectedVersion: 1}, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	f.work(t)
	i, e = f.s.Read(context.Background(), actor(domain.Senior), i.ID)
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.s.ReserveCheckout(context.Background(), actor(domain.Senior), i.ID, "AUTHORIZE", uuid.NewString(), PaymentAction{ExpectedVersion: 1, OrderID: *i.Checkout.ProviderOrderID}, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	f.work(t)
	i, e = f.s.Read(context.Background(), actor(domain.Senior), i.ID)
	if e != nil || i.Payment != domain.Held {
		t.Fatalf("held: %s %v", i.Payment, e)
	}
	return i
}
func (f *paymentFixture) status(t *testing.T, id string) domain.PaymentStatus {
	t.Helper()
	i, e := f.s.Read(context.Background(), actor(domain.Senior), id)
	if e != nil {
		t.Fatal(e)
	}
	return i.Payment
}

type fakeProvider struct {
	mu                                                             sync.Mutex
	states                                                         map[string]domain.ProviderResult
	calls                                                          map[string]int
	keys                                                           map[string]string
	captures                                                       map[string]int
	dropCapture, pendingCapture, failVoid, staleRead, badSignature bool
	before                                                         func(string, domain.ProviderCommand)
}

func (p *fakeProvider) invoke(kind string, c domain.ProviderCommand) (domain.ProviderResult, error) {
	if p.before != nil {
		p.before(kind, c)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls[kind]++
	if kind != "READ" {
		if k := p.keys[kind+c.IntentID]; k != "" && k != c.RequestID {
			return domain.ProviderResult{}, errors.New("fixture request key rotated")
		}
		p.keys[kind+c.IntentID] = c.RequestID
	}
	r, ok := p.states[c.IntentID]
	if !ok {
		r = domain.ProviderResult{OrderID: "ORDER" + strings.ReplaceAll(c.IntentID, "-", ""), Status: "AWAITING_PAYER", Confirmed: true, AmountMinor: c.Binding.AmountMinor, Currency: c.Binding.Currency, PayeeID: c.Binding.PayeeID, Environment: "sandbox", OrderStatus: "CREATED"}
	}
	switch kind {
	case "AUTHORIZE":
		r.AuthorizationID = "AUTH" + strings.ReplaceAll(c.IntentID, "-", "")
		r.AuthorizationStatus = "CREATED"
		r.Status = "HELD"
	case "CAPTURE":
		// Model provider authorization finality independently of request-ID caching.
		// Even a missing/expired idempotency entry cannot capture this final hold twice.
		if r.CaptureID != "" {
			return r, &paypal.Error{Code: "PROVIDER_AUTHORIZATION_ALREADY_CAPTURED", HTTP: 422, Unknown: true}
		}
		frozen := paypal.Payload("CAPTURE", c)
		var body struct {
			Amount struct {
				Currency string `json:"currency_code"`
				Value    string `json:"value"`
			} `json:"amount"`
			Final bool `json:"final_capture"`
		}
		if json.Unmarshal(frozen, &body) != nil || !body.Final || body.Amount.Value != paypal.USD(r.AmountMinor) || body.Amount.Currency != r.Currency {
			return r, errors.New("fixture requires exact full/final capture")
		}
		p.captures[c.IntentID]++
		r.CaptureID = "CAP" + strings.ReplaceAll(c.IntentID, "-", "")
		r.Status = "CAPTURED"
		r.CaptureStatus = "COMPLETED"
		r.AuthorizationStatus = "CAPTURED"
		if p.pendingCapture {
			r.Status = "CAPTURE_PENDING"
			r.CaptureStatus = "PENDING"
		}
	case "VOID":
		if p.failVoid {
			return r, &paypal.Error{Code: "PROVIDER_TRANSPORT", Temporary: true, Unknown: true}
		}
		r.Status = "VOIDED"
		r.AuthorizationStatus = "VOIDED"
	case "READ":
		if p.staleRead {
			p.staleRead = false
			stale := r
			stale.Status = "HELD"
			stale.CaptureID = ""
			stale.CaptureStatus = ""
			stale.AuthorizationStatus = "CREATED"
			return stale, nil
		}
	}
	p.states[c.IntentID] = r
	if kind == "CAPTURE" && p.dropCapture {
		p.dropCapture = false
		return domain.ProviderResult{}, &paypal.Error{Code: "PROVIDER_TRANSPORT", Temporary: true, Unknown: true}
	}
	return r, nil
}
func (p *fakeProvider) CreateOrder(_ context.Context, c domain.ProviderCommand) (domain.ProviderResult, error) {
	return p.invoke("CREATE_ORDER", c)
}
func (p *fakeProvider) Authorize(_ context.Context, c domain.ProviderCommand) (domain.ProviderResult, error) {
	return p.invoke("AUTHORIZE", c)
}
func (p *fakeProvider) Capture(_ context.Context, c domain.ProviderCommand) (domain.ProviderResult, error) {
	return p.invoke("CAPTURE", c)
}
func (p *fakeProvider) Void(_ context.Context, c domain.ProviderCommand) (domain.ProviderResult, error) {
	return p.invoke("VOID", c)
}
func (p *fakeProvider) Reconcile(_ context.Context, c domain.ProviderCommand) (domain.ProviderResult, error) {
	return p.invoke("READ", c)
}
func (p *fakeProvider) VerifyWebhook(context.Context, http.Header, json.RawMessage) error {
	if p.badSignature {
		return &paypal.Error{Code: "WEBHOOK_SIGNATURE_INVALID"}
	}
	return nil
}
func TestP02ConcurrentCheckoutAndHoldOnly(t *testing.T) {
	f := setupPayments(t)
	i := f.draft(t)
	key := uuid.NewString()
	var wg sync.WaitGroup
	for n := 0; n < 2; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := f.s.ReserveCheckout(context.Background(), actor(domain.Senior), i.ID, "CREATE_ORDER", key, PaymentAction{ExpectedVersion: 1}, "race")
			if e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	f.p.before = func(kind string, c domain.ProviderCommand) {
		if kind == "CREATE_ORDER" {
			var n int
			if e := f.s.DB.Pool.QueryRow(context.Background(), `SELECT count(*) FROM payment_operations WHERE provider_request_id=$1 AND state='DISPATCHED'`, c.RequestID).Scan(&n); e != nil || n != 1 {
				t.Error("provider called before committed reservation", e, n)
			}
		}
	}
	for n := 0; n < 2; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			js := &store.Jobs{DB: f.s.DB, Clock: f.clock}
			job, e := js.Lease(context.Background(), []string{"PAYMENT_OPERATION"}, time.Minute)
			if e == nil && job != nil {
				e = f.s.RunPaymentJob(context.Background(), *job)
				if e == nil {
					e = js.Complete(context.Background(), *job)
				}
			}
			if e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	f.p.before = nil
	current, e := f.s.Read(context.Background(), actor(domain.Senior), i.ID)
	if e != nil {
		t.Fatal(e)
	}
	if current.Checkout.ProviderOrderID == nil {
		var code *string
		_ = f.s.DB.Pool.QueryRow(context.Background(), `SELECT last_error_code FROM jobs WHERE kind='PAYMENT_OPERATION'`).Scan(&code)
		t.Fatalf("order missing: state=%s job=%v calls=%v", current.Payment, code, f.p.calls)
	}
	for n := 0; n < 2; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := f.s.ReserveCheckout(context.Background(), actor(domain.Senior), i.ID, "AUTHORIZE", uuid.NewString(), PaymentAction{ExpectedVersion: 1, OrderID: *current.Checkout.ProviderOrderID}, "race")
			if e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	f.work(t)
	if f.status(t, i.ID) != domain.Held || f.p.calls["CAPTURE"] != 0 || f.p.calls["CREATE_ORDER"] != 1 || f.p.calls["AUTHORIZE"] != 1 {
		t.Fatal("duplicate or capture on approval", f.p.calls)
	}
	_, e = f.s.ReserveCheckout(context.Background(), actor(domain.Senior), i.ID, "AUTHORIZE", key, PaymentAction{ExpectedVersion: 1, OrderID: *current.Checkout.ProviderOrderID}, "conflict")
	if e == nil {
		t.Fatal("same key different payload accepted")
	}
}
func TestP02EvidenceGateAndCleanupOutage(t *testing.T) {
	f := setupPayments(t)
	i := f.held(t)
	if e := f.s.ReviewApprove(context.Background(), actor(domain.Reviewer), i.ID, uuid.NewString(), "Fixture approve", "fixture"); e == nil {
		t.Fatal("missing evidence released")
	}
	f.s.Config.MLURL = "http://unreachable.invalid"
	f.s.Config.ElasticURL = "http://unreachable.invalid"
	if _, e := f.s.Cancel(context.Background(), actor(domain.Reviewer), i.ID, 1, "deny"); e != nil {
		t.Fatal(e)
	}
	f.work(t)
	if f.status(t, i.ID) != domain.Voided || f.p.calls["CAPTURE"] != 0 {
		t.Fatal("cleanup depended on analysis")
	}
}
func (f *paymentFixture) evidence(t *testing.T, i domain.Intent) string {
	t.Helper()
	ctx := context.Background()
	hash := strings.Repeat("a", 64)
	thresholds := map[string]float64{}
	scores := map[string]float64{}
	for _, l := range domain.Labels {
		thresholds[l] = .5
		scores[l] = .1
	}
	classifier := domain.ClassifierManifest{SchemaVersion: domain.ContractVersion, Status: "RELEASED", ModelVersion: "fixture-classifier-v1", ArtifactHash: hash, LabelSchemaVersion: "pause-signals-v1", PreprocessingVersion: "fixture-pre-v1", Labels: domain.Labels, Thresholds: thresholds, DatasetHash: hash, SplitHash: hash, DependencyLockHash: hash, CodeRevision: "fixture-only", Language: "en"}
	encoder := domain.EncoderManifest{SchemaVersion: domain.ContractVersion, Status: "RELEASED", EncoderVersion: "fixture-encoder-v1", ArtifactHash: hash, UpstreamRevision: "fixture-only", License: "synthetic", PreprocessingVersion: "fixture-pre-v1", Dimension: 3, Normalized: true, Similarity: "cosine"}
	classification := domain.Classification{SchemaVersion: domain.ContractVersion, IntentID: i.ID, IntentVersion: i.Version, ContentDigest: i.Digest, ModelVersion: classifier.ModelVersion, ArtifactHash: hash, LabelSchemaVersion: classifier.LabelSchemaVersion, PreprocessingVersion: classifier.PreprocessingVersion, LabelScores: scores, Status: domain.InferenceOK}
	ev := releaseEvidence{Classification: classification, Classifier: classifier, Encoder: encoder, Playbooks: domain.SearchResult{SchemaVersion: domain.ContractVersion, Scope: domain.Playbooks, Status: domain.LookupOK, EncoderVersion: encoder.EncoderVersion, IndexVersion: "fixture-index-v1", MappingVersion: "fixture-map-v1", FusionVersion: "fixture-fusion-v1", Hits: []domain.SearchHit{}}, History: domain.SearchResult{Scope: domain.History, Status: domain.LookupNotApplicable}, PolicyVersion: PaymentPolicy}
	id := uuid.NewString()
	e := f.s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		p, e := f.s.paymentLock(ctx, tx, i.ID)
		if e != nil {
			return e
		}
		ev.Requested, ev.Held, ev.Captured, e = f.s.totals(ctx, tx, p)
		if e != nil {
			return e
		}
		for _, m := range []struct {
			kind, version string
			manifest      any
		}{{"classifier", classifier.ModelVersion, classifier}, {"encoder", encoder.EncoderVersion, encoder}} {
			if _, e = tx.Exec(ctx, `INSERT INTO model_releases(id,kind,version,manifest,artifact_hash,approved_at) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(kind,version) DO NOTHING`, uuid.NewString(), m.kind, m.version, mustJSON(m.manifest), hash, f.clock.Now()); e != nil {
				return e
			}
		}
		if _, e = tx.Exec(ctx, `INSERT INTO payment_evidence_config(id,classifier_version,encoder_version,template_version,policy_version,revision) VALUES(1,$1,$2,'fixture-template-v1',$3,1) ON CONFLICT(id) DO NOTHING`, classifier.ModelVersion, encoder.EncoderVersion, PaymentPolicy); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `UPDATE verifications SET status='CONFIRMED',expires_at=$3,responded_at=$4 WHERE intent_id=$1 AND intent_version=$2`, i.ID, i.Version, p.deadline, f.clock.Now()); e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `INSERT INTO evidence_snapshots(id,intent_id,intent_version,consent_version,history_revision,content_digest,schema_version,policy_version,classifier_version,encoder_version,template_version,evidence,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'fixture-template-v1',$11,$12)`, id, i.ID, i.Version, p.consent, p.revision, i.Digest, domain.ContractVersion, PaymentPolicy, classifier.ModelVersion, encoder.EncoderVersion, mustJSON(ev), f.clock.Now())
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	return id
}
func TestP02ReleaseRejectsStaleAndUnavailableEvidence(t *testing.T) {
	for _, name := range []string{"empty-success", "unavailable-playbooks", "wrong-artifact", "stale-totals", "consent-change", "denied-subject", "stale-history", "invalidated", "wrong-envelope", "expired-confirmation", "inactive-subject", "future-evidence", "old-evidence", "replayed-other-intent", "missing-config", "wrong-encoder"} {
		t.Run(name, func(t *testing.T) {
			f := setupPayments(t)
			i := f.held(t)
			ev := f.evidence(t, i)
			ctx := context.Background()
			var e error
			switch name {
			case "unavailable-playbooks":
				_, e = f.s.DB.Pool.Exec(ctx, `UPDATE evidence_snapshots SET evidence=jsonb_set(evidence,'{playbooks,status}','"UNAVAILABLE"') WHERE id=$1`, ev)
			case "wrong-artifact":
				_, e = f.s.DB.Pool.Exec(ctx, `UPDATE evidence_snapshots SET evidence=jsonb_set(evidence,'{classification,artifact_hash}',$2::jsonb) WHERE id=$1`, ev, `"`+strings.Repeat("b", 64)+`"`)
			case "stale-totals":
				_, e = f.s.DB.Pool.Exec(ctx, `UPDATE evidence_snapshots SET evidence=jsonb_set(evidence,'{Requested}','1') WHERE id=$1`, ev)
			case "consent-change":
				yes := true
				_, e = f.s.ChangeConsent(ctx, actor(domain.Senior), i.ID, ConsentChange{ExpectedVersion: 1, History: &yes}, "fixture")
			case "denied-subject":
				_, e = f.s.DB.Pool.Exec(ctx, `UPDATE verifications SET status='DENIED' WHERE intent_id=$1`, i.ID)
			case "stale-history":
				_, e = f.s.DB.Pool.Exec(ctx, `UPDATE households SET history_revision=history_revision+1 WHERE id=$1`, actor(domain.Senior).HouseholdID)
			case "invalidated":
				_, e = f.s.DB.Pool.Exec(ctx, `UPDATE evidence_snapshots SET invalidated_at=$2 WHERE id=$1`, ev, f.clock.Now())
			case "wrong-envelope":
				_, e = f.s.DB.Pool.Exec(ctx, `UPDATE evidence_snapshots SET schema_version='future-unsupported-schema' WHERE id=$1`, ev)
			case "expired-confirmation":
				_, e = f.s.DB.Pool.Exec(ctx, `UPDATE verifications SET expires_at=$2 WHERE intent_id=$1`, i.ID, f.clock.Now())
			case "inactive-subject":
				_, e = f.s.DB.Pool.Exec(ctx, `UPDATE memberships SET active=false WHERE user_id=$1`, actor(domain.Subject).UserID)
			case "future-evidence":
				_, e = f.s.DB.Pool.Exec(ctx, `UPDATE evidence_snapshots SET created_at=$2 WHERE id=$1`, ev, f.clock.Now().Add(time.Second))
			case "old-evidence":
				_, e = f.s.DB.Pool.Exec(ctx, `UPDATE evidence_snapshots SET created_at=$2 WHERE id=$1`, ev, f.clock.Now().Add(-30*time.Minute))
			case "replayed-other-intent":
				other := f.held(t)
				ev = f.evidence(t, other)
			case "missing-config":
				_, e = f.s.DB.Pool.Exec(ctx, `DELETE FROM payment_evidence_config`)
			case "wrong-encoder":
				_, e = f.s.DB.Pool.Exec(ctx, `UPDATE evidence_snapshots SET evidence=jsonb_set(evidence,'{encoder,artifact_hash}',$2::jsonb) WHERE id=$1`, ev, `"`+strings.Repeat("b", 64)+`"`)
			}
			if e != nil {
				t.Fatal(e)
			}
			e = f.s.ReviewApprove(ctx, actor(domain.Reviewer), i.ID, ev, "Explicit fixture rationale", "fixture")
			if name == "empty-success" {
				if e != nil {
					t.Fatal("successful zero-hit lookup must remain usable", e)
				}
			} else if e == nil {
				t.Fatal("invalid evidence released")
			}
		})
	}
}
func TestP02CaptureVoidAndDoubleApprovalRace(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(fmt.Sprint("cancel_", cancel), func(t *testing.T) {
			f := setupPayments(t)
			i := f.held(t)
			ev := f.evidence(t, i)
			var wg sync.WaitGroup
			start := make(chan struct{})
			for n := 0; n < 2; n++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					_ = f.s.ReviewApprove(context.Background(), actor(domain.Reviewer), i.ID, ev, "Fixture rationale", "race")
				}()
			}
			if cancel {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					_, _ = f.s.Cancel(context.Background(), actor(domain.Senior), i.ID, 1, "race")
				}()
			}
			close(start)
			wg.Wait()
			var n int
			if e := f.s.DB.Pool.QueryRow(context.Background(), `SELECT count(*) FROM payment_operations WHERE intent_id=$1 AND kind IN ('CAPTURE','VOID') AND state IN ('RESERVED','DISPATCHED','UNKNOWN')`, i.ID).Scan(&n); e != nil || n != 1 {
				t.Fatalf("exclusive active outcomes %d %v", n, e)
			}
			f.work(t)
			if f.p.calls["CAPTURE"] > 1 || f.p.calls["VOID"] > 1 || (f.p.calls["CAPTURE"] > 0 && f.p.calls["VOID"] > 0) {
				t.Fatal(f.p.calls)
			}
		})
	}
}
func TestP02LostCaptureAndPendingReconcile(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(fmt.Sprint("pending_", pending), func(t *testing.T) {
			f := setupPayments(t)
			i := f.held(t)
			ev := f.evidence(t, i)
			f.p.dropCapture = !pending
			f.p.pendingCapture = pending
			if e := f.s.ReviewApprove(context.Background(), actor(domain.Reviewer), i.ID, ev, "Fixture rationale", "fixture"); e != nil {
				t.Fatal(e)
			}
			f.work(t)
			if pending && f.status(t, i.ID) == domain.Captured {
				t.Fatal("pending shown completed")
			}
			f.p.mu.Lock()
			r := f.p.states[i.ID]
			r.Status = "CAPTURED"
			r.CaptureStatus = "COMPLETED"
			f.p.states[i.ID] = r
			f.p.mu.Unlock()
			f.clock.now = f.clock.now.Add(time.Minute)
			restarted := *f.s
			// Missing analysis blocks new/replayed capture, but provider truth
			// from an already dispatched operation must still reconcile.
			restarted.sourceValidator = nil
			f.s = &restarted
			f.work(t)
			if f.status(t, i.ID) != domain.Captured || f.p.calls["CAPTURE"] != 1 {
				t.Fatal("did not converge once", f.status(t, i.ID), f.p.calls)
			}
			_, e := f.s.Cancel(context.Background(), actor(domain.Senior), i.ID, 1, "late cancel")
			if e != nil {
				t.Fatal(e)
			}
			f.work(t)
			if f.status(t, i.ID) != domain.Captured || f.p.calls["VOID"] != 0 {
				t.Fatal("late cancellation reversed capture")
			}
		})
	}
}
func TestP02DeadlineFailedVoidDeadLetterAndReadOnlyDiagnostics(t *testing.T) {
	f := setupPayments(t)
	i := f.held(t)
	f.p.failVoid = true
	f.clock.now = i.DecisionDeadline
	if e := f.s.TickPayments(context.Background()); e != nil {
		t.Fatal(e)
	}
	for n := 0; n < 12; n++ {
		f.work(t)
		f.clock.now = f.clock.now.Add(10 * time.Minute)
		if e := (&store.Jobs{DB: f.s.DB, Clock: f.clock}).ReapExhausted(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
	if f.status(t, i.ID) == domain.Voided {
		t.Fatal("failed void falsely confirmed")
	}
	var failed int
	if e := f.s.DB.Pool.QueryRow(context.Background(), `SELECT count(*) FROM jobs WHERE failed_at IS NOT NULL AND kind='PAYMENT_OPERATION'`).Scan(&failed); e != nil || failed < 1 {
		t.Fatal("no dead letter", e, failed)
	}
	if e := f.s.TickPayments(context.Background()); e != nil {
		t.Fatal(e)
	}
	out, e := f.s.Diagnostics(context.Background(), actor(domain.Reviewer), i.ID)
	if e != nil {
		t.Fatal(e)
	}
	raw := string(mustJSON(out))
	if !strings.Contains(raw, "dead_letter_at") || strings.Contains(raw, "Synthetic message") {
		t.Fatal(raw)
	}
}
func TestP02EligibilityInvalidatedBeforeDispatch(t *testing.T) {
	for _, change := range []string{"consent", "config", "deadline", "membership"} {
		t.Run(change, func(t *testing.T) {
			f := setupPayments(t)
			i := f.held(t)
			ev := f.evidence(t, i)
			if e := f.s.ReviewApprove(context.Background(), actor(domain.Reviewer), i.ID, ev, "Fixture rationale", "fixture"); e != nil {
				t.Fatal(e)
			}
			ctx := context.Background()
			switch change {
			case "consent":
				yes := true
				if _, e := f.s.ChangeConsent(ctx, actor(domain.Senior), i.ID, ConsentChange{ExpectedVersion: 1, History: &yes}, "fixture"); e != nil {
					t.Fatal(e)
				}
			case "config":
				if _, e := f.s.DB.Pool.Exec(ctx, `UPDATE payment_evidence_config SET template_version='changed-template',revision=revision+1`); e != nil {
					t.Fatal(e)
				}
			case "deadline":
				f.clock.now = i.DecisionDeadline
			case "membership":
				if _, e := f.s.DB.Pool.Exec(ctx, `UPDATE memberships SET active=false WHERE user_id=$1`, actor(domain.Reviewer).UserID); e != nil {
					t.Fatal(e)
				}
			}
			f.work(t)
			if f.p.calls["CAPTURE"] != 0 || f.status(t, i.ID) != domain.Voided {
				t.Fatalf("stale dispatch captured or failed cleanup: %v %s", f.p.calls, f.status(t, i.ID))
			}
		})
	}
}
func TestP02EarlyEventAndTemporaryMissingRead(t *testing.T) {
	f := setupPayments(t)
	i := f.draft(t)
	order := "ORDER" + strings.ReplaceAll(i.ID, "-", "")
	raw := []byte(fmt.Sprintf(`{"id":"EARLY_EVENT","event_type":"CHECKOUT.ORDER.APPROVED","resource":{"id":%q}}`, order))
	if e := f.s.IngestWebhook(context.Background(), http.Header{}, raw); e != nil {
		t.Fatal(e)
	}
	var mapped *string
	if e := f.s.DB.Pool.QueryRow(context.Background(), `SELECT mapped_intent::text FROM paypal_events WHERE id='EARLY_EVENT'`).Scan(&mapped); e != nil || mapped != nil {
		t.Fatal(mapped, e)
	}
	if _, e := f.s.ReserveCheckout(context.Background(), actor(domain.Senior), i.ID, "CREATE_ORDER", uuid.NewString(), PaymentAction{ExpectedVersion: 1}, "fixture"); e != nil {
		t.Fatal(e)
	}
	f.work(t)
	if e := f.s.TickPayments(context.Background()); e != nil {
		t.Fatal(e)
	}
	f.work(t)
	if e := f.s.DB.Pool.QueryRow(context.Background(), `SELECT mapped_intent::text FROM paypal_events WHERE id='EARLY_EVENT'`).Scan(&mapped); e != nil || mapped == nil || *mapped != i.ID {
		t.Fatal(mapped, e)
	}
	if f.status(t, i.ID) == domain.Voided {
		t.Fatal("missing authorization became voided")
	}
}
func TestP02VerifiedDuplicateReorderedAndLateEvents(t *testing.T) {
	f := setupPayments(t)
	i := f.held(t)
	raw := []byte(fmt.Sprintf(`{"id":"EVENT1","event_type":"PAYMENT.AUTHORIZATION.CREATED","resource":{"id":%q,"supplementary_data":{"related_ids":{"order_id":%q}}}}`, *i.Checkout.AuthorizationID, *i.Checkout.ProviderOrderID))
	f.p.badSignature = true
	if f.s.IngestWebhook(context.Background(), http.Header{}, raw) == nil {
		t.Fatal("invalid signature accepted")
	}
	f.p.badSignature = false
	for n := 0; n < 2; n++ {
		if e := f.s.IngestWebhook(context.Background(), http.Header{}, raw); e != nil {
			t.Fatal(e)
		}
	}
	_, e := f.s.Cancel(context.Background(), actor(domain.Senior), i.ID, 1, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	f.work(t)
	if e = f.s.TickPayments(context.Background()); e != nil {
		t.Fatal(e)
	}
	f.work(t)
	if f.status(t, i.ID) != domain.Voided {
		t.Fatal("late event reversed void")
	}
	var count int
	if e = f.s.DB.Pool.QueryRow(context.Background(), `SELECT count(*) FROM paypal_events WHERE id='EVENT1' AND processed_at IS NOT NULL`).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
	j := f.draft(t)
	_, e = f.s.ReserveCheckout(context.Background(), actor(domain.Senior), j.ID, "CREATE_ORDER", uuid.NewString(), PaymentAction{ExpectedVersion: 1}, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	f.work(t)
	j, _ = f.s.Read(context.Background(), actor(domain.Senior), j.ID)
	_, e = f.s.Cancel(context.Background(), actor(domain.Senior), j.ID, 1, "cancel before approval")
	if e != nil {
		t.Fatal(e)
	}
	if f.status(t, j.ID) != domain.Abandoned {
		t.Fatal("preapproval cancel incorrect")
	}
	_, e = f.s.ReserveCheckout(context.Background(), actor(domain.Senior), j.ID, "AUTHORIZE", uuid.NewString(), PaymentAction{ExpectedVersion: 1, OrderID: *j.Checkout.ProviderOrderID}, "late approval")
	if e != nil {
		t.Fatal(e)
	}
	f.work(t)
	if f.status(t, j.ID) != domain.Voided {
		t.Fatal("late hold not voided", f.status(t, j.ID))
	}
}

// This provider and subprocess helper are private test fixtures, never product flags.
type processProvider struct{ url, crash, kind string }

func (p processProvider) call(kind string, c domain.ProviderCommand) (domain.ProviderResult, error) {
	if kind == p.kind && p.crash == "after_dispatch" {
		os.Exit(72)
	}
	raw := mustJSON(c)
	resp, e := http.Post(p.url+"/"+kind, "application/json", bytes.NewReader(raw))
	if e != nil {
		return domain.ProviderResult{}, e
	}
	defer resp.Body.Close()
	var r domain.ProviderResult
	e = json.NewDecoder(resp.Body).Decode(&r)
	if kind == p.kind && p.crash == "after_provider" {
		os.Exit(73)
	}
	return r, e
}
func (p processProvider) CreateOrder(_ context.Context, c domain.ProviderCommand) (domain.ProviderResult, error) {
	return p.call("CREATE_ORDER", c)
}
func (p processProvider) Authorize(_ context.Context, c domain.ProviderCommand) (domain.ProviderResult, error) {
	return p.call("AUTHORIZE", c)
}
func (p processProvider) Capture(_ context.Context, c domain.ProviderCommand) (domain.ProviderResult, error) {
	return p.call("CAPTURE", c)
}
func (p processProvider) Void(_ context.Context, c domain.ProviderCommand) (domain.ProviderResult, error) {
	return p.call("VOID", c)
}
func (p processProvider) Reconcile(_ context.Context, c domain.ProviderCommand) (domain.ProviderResult, error) {
	return p.call("READ", c)
}
func TestP02ProcessWorker(t *testing.T) {
	if os.Getenv("P02_CHILD_SCHEMA") == "" {
		return
	}
	cfg, e := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if e != nil {
		t.Fatal("child DB config")
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = os.Getenv("P02_CHILD_SCHEMA")
	pool, e := pgxpool.NewWithConfig(context.Background(), cfg)
	if e != nil {
		t.Fatal("child DB open")
	}
	defer pool.Close()
	now, e := time.Parse(time.RFC3339, os.Getenv("P02_CHILD_NOW"))
	if e != nil {
		t.Fatal(e)
	}
	s := &Service{DB: &store.Store{Pool: pool}, Clock: &paymentClock{now}, Provider: processProvider{url: os.Getenv("P02_CHILD_PROVIDER"), crash: os.Getenv("P02_CHILD_CRASH"), kind: os.Getenv("P02_CHILD_KIND")}}
	installFixtureSourceValidator(s)
	if os.Getenv("P02_CHILD_CRASH") == "after_reservation" {
		os.Exit(71)
	}
	_, e = s.WorkPaymentOnce(context.Background())
	if e != nil {
		t.Fatal(e)
	}
}
func TestP02ActualProcessCrashLeaseRecovery(t *testing.T) {
	for _, kind := range []string{"VOID", "CAPTURE"} {
		t.Run(kind, func(t *testing.T) {
			for _, checkpoint := range []string{"after_reservation", "after_dispatch", "after_provider"} {
				t.Run(checkpoint, func(t *testing.T) {
					f := setupPayments(t)
					i := f.held(t)
					if kind == "VOID" {
						if _, e := f.s.Cancel(context.Background(), actor(domain.Senior), i.ID, 1, "process crash fixture"); e != nil {
							t.Fatal(e)
						}
					} else {
						ev := f.evidence(t, i)
						if e := f.s.ReviewApprove(context.Background(), actor(domain.Reviewer), i.ID, ev, "Synthetic subprocess recovery fixture", "fixture"); e != nil {
							t.Fatal(e)
						}
					}
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var c domain.ProviderCommand
						if json.NewDecoder(r.Body).Decode(&c) != nil {
							w.WriteHeader(400)
							return
						}
						result, e := f.p.invoke(strings.TrimPrefix(r.URL.Path, "/"), c)
						if e != nil {
							w.WriteHeader(503)
							return
						}
						_ = json.NewEncoder(w).Encode(result)
					}))
					defer server.Close()
					run := func(crash string) error {
						cmd := exec.Command(os.Args[0], "-test.run=^TestP02ProcessWorker$", "-test.count=1")
						cmd.Env = append(os.Environ(), "P02_CHILD_SCHEMA="+f.schema, "P02_CHILD_NOW="+f.clock.Now().Format(time.RFC3339), "P02_CHILD_PROVIDER="+server.URL, "P02_CHILD_CRASH="+crash, "P02_CHILD_KIND="+kind)
						raw, e := cmd.CombinedOutput()
						if e != nil && crash == "" {
							t.Log(string(raw))
						}
						return e
					}
					var crashed *exec.ExitError
					if e := run(checkpoint); !errors.As(e, &crashed) || crashed.ExitCode() != map[string]int{"after_reservation": 71, "after_dispatch": 72, "after_provider": 73}[checkpoint] {
						t.Fatalf("wrong child crash: %v", e)
					}
					f.clock.now = f.clock.now.Add(61 * time.Second)
					if e := run(""); e != nil {
						t.Fatal(e)
					}
					expected := domain.Voided
					if kind == "CAPTURE" {
						expected = domain.Captured
					}
					if f.status(t, i.ID) != expected || f.p.calls[kind] != 1 {
						t.Fatalf("restart failed %s calls=%v", f.status(t, i.ID), f.p.calls)
					}
					var ids int
					if e := f.s.DB.Pool.QueryRow(context.Background(), `SELECT count(DISTINCT provider_request_id) FROM payment_operations WHERE intent_id=$1 AND kind=$2`, i.ID, kind).Scan(&ids); e != nil || ids != 1 {
						t.Fatal("request key changed", ids, e)
					}
				})
			}
		})
	}
}

func TestP02CaptureRecoveryFinalityAndCurrentEvidence(t *testing.T) {
	for _, scenario := range []string{"stale-after-success-cache-missing", "pending", "voided", "expired", "cancelled", "deadline", "evidence", "membership"} {
		t.Run(scenario, func(t *testing.T) {
			f := setupPayments(t)
			i := f.held(t)
			ev := f.evidence(t, i)
			ctx := context.Background()
			if err := f.s.ReviewApprove(ctx, actor(domain.Reviewer), i.ID, ev, "Synthetic recovery fixture", "fixture"); err != nil {
				t.Fatal(err)
			}
			var opID string
			if err := f.s.DB.Pool.QueryRow(ctx, `UPDATE payment_operations SET state='DISPATCHED',dispatched_at=$2 WHERE intent_id=$1 AND kind='CAPTURE' RETURNING id::text`, i.ID, f.clock.Now()).Scan(&opID); err != nil {
				t.Fatal(err)
			}
			o, err := f.s.loadOperation(ctx, opID)
			if err != nil {
				t.Fatal(err)
			}
			f.p.before = func(kind string, c domain.ProviderCommand) {
				if kind != "CAPTURE" {
					return
				}
				var body []byte
				var key, state, hash string
				if e := f.s.DB.Pool.QueryRow(ctx, `SELECT request_body,provider_request_id,state,request_hash FROM payment_operations WHERE id=$1`, opID).Scan(&body, &key, &state, &hash); e != nil {
					t.Error(e)
					return
				}
				if key != c.RequestID || state != "DISPATCHED" || !bytes.Equal(body, paypal.Payload("CAPTURE", c)) || hash != domain.Hash(body) {
					t.Error("capture replay changed its committed key/body")
				}
			}
			switch scenario {
			case "stale-after-success-cache-missing":
				if _, err = f.p.Capture(ctx, o.cmd); err != nil {
					t.Fatal(err)
				}
				delete(f.p.keys, "CAPTURE"+i.ID) // Provider cache absent; finality must independently prevent another debit.
				f.p.staleRead = true
			case "pending":
				f.p.pendingCapture = true
				if _, err = f.p.Capture(ctx, o.cmd); err != nil {
					t.Fatal(err)
				}
			case "voided":
				r := f.p.states[i.ID]
				r.Status = "VOIDED"
				r.AuthorizationStatus = "VOIDED"
				f.p.states[i.ID] = r
			case "expired":
				r := f.p.states[i.ID]
				r.Status = "CLOSED_UNCAPTURED"
				r.AuthorizationStatus = "EXPIRED"
				f.p.states[i.ID] = r
			case "cancelled":
				_, err = f.s.Cancel(ctx, actor(domain.Senior), i.ID, 1, "late cancellation")
			case "deadline":
				f.clock.now = i.DecisionDeadline
			case "evidence":
				_, err = f.s.DB.Pool.Exec(ctx, `UPDATE evidence_snapshots SET invalidated_at=$2 WHERE id=$1`, ev, f.clock.Now())
			case "membership":
				_, err = f.s.DB.Pool.Exec(ctx, `UPDATE memberships SET active=false WHERE user_id=$1`, actor(domain.Reviewer).UserID)
			}
			if err != nil {
				t.Fatal(err)
			}
			f.work(t)
			switch scenario {
			case "stale-after-success-cache-missing":
				if f.status(t, i.ID) != domain.Reconciling {
					t.Fatal("stale read invented a terminal state")
				}
				f.clock.now = f.clock.now.Add(time.Minute)
				f.work(t)
				if f.status(t, i.ID) != domain.Captured || f.p.calls["CAPTURE"] != 2 || f.p.captures[i.ID] != 1 {
					t.Fatal("final capture duplicated or failed convergence", f.p.calls, f.p.captures)
				}
			case "pending":
				if f.status(t, i.ID) == domain.Captured || f.p.calls["CAPTURE"] != 1 || f.p.captures[i.ID] != 1 {
					t.Fatal("pending replayed or shown complete")
				}
			case "voided":
				if f.status(t, i.ID) != domain.Voided || f.p.calls["CAPTURE"] != 0 {
					t.Fatal("closed authorization replayed")
				}
			case "expired":
				if f.status(t, i.ID) != domain.ClosedUncaptured || f.p.calls["CAPTURE"] != 0 {
					t.Fatal("expired authorization replayed")
				}
			default:
				if f.p.calls["CAPTURE"] != 0 || f.p.calls["VOID"] != 0 || f.status(t, i.ID) != domain.Reconciling {
					t.Fatalf("changed evidence replayed/competed: %s %v", f.status(t, i.ID), f.p.calls)
				}
			}
		})
	}
}

func (f *paymentFixture) independentWorker(t *testing.T) *Service {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = f.schema
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	s := *f.s
	s.DB = &store.Store{Pool: pool}
	return &s
}

func TestP02IndependentWorkersAndCancelDuringDelayedCapture(t *testing.T) {
	f := setupPayments(t)
	a := f.held(t)
	b := f.held(t)
	ev := f.evidence(t, a)
	ctx := context.Background()
	if err := f.s.ReviewApprove(ctx, actor(domain.Reviewer), a.ID, ev, "Synthetic delayed provider fixture", "fixture"); err != nil {
		t.Fatal(err)
	}
	one, two := f.independentWorker(t), f.independentWorker(t)
	started, release := make(chan struct{}), make(chan struct{})
	finished := make(chan error, 1)
	f.p.before = func(kind string, c domain.ProviderCommand) {
		if kind != "CAPTURE" {
			return
		}
		// A distinct connection can acquire both decision locks while HTTP is in flight.
		err := two.DB.WithTx(ctx, func(tx pgx.Tx) error {
			if _, e := tx.Exec(ctx, `SELECT id FROM households WHERE id=$1 FOR UPDATE NOWAIT`, actor(domain.Senior).HouseholdID); e != nil {
				return e
			}
			_, e := tx.Exec(ctx, `SELECT id FROM payment_intents WHERE id=$1 FOR UPDATE NOWAIT`, c.IntentID)
			return e
		})
		if err != nil {
			t.Error("database lock held across provider call", err)
		}
		close(started)
		<-release
	}
	go func() { _, err := one.WorkPaymentOnce(ctx); finished <- err }()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("first worker did not dispatch")
	}
	if _, err := two.Cancel(ctx, actor(domain.Senior), a.ID, 1, "cancel in flight"); err != nil {
		close(release)
		t.Fatal(err)
	}
	if _, err := two.Cancel(ctx, actor(domain.Senior), b.ID, 1, "second hold cancel"); err != nil {
		close(release)
		t.Fatal(err)
	}
	for n := 0; n < 4; n++ {
		if _, err := two.WorkPaymentOnce(ctx); err != nil {
			close(release)
			t.Fatal(err)
		}
	}
	if f.status(t, b.ID) != domain.Voided || f.status(t, a.ID) == domain.Voided {
		close(release)
		t.Fatal("second worker blocked or active capture falsely cancelled")
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	f.p.before = nil
	f.clock.now = f.clock.now.Add(time.Minute)
	f.work(t)
	if f.status(t, a.ID) != domain.Captured || f.p.captures[a.ID] != 1 || f.p.calls["VOID"] != 1 {
		t.Fatalf("late success lost/duplicated: %s %v", f.status(t, a.ID), f.p.calls)
	}
}

func TestP02TotalsRequireConfirmedHoldAndRevisionTracksFinancialTruth(t *testing.T) {
	f := setupPayments(t)
	i := f.draft(t)
	ctx := context.Background()
	if _, err := f.s.ReserveCheckout(ctx, actor(domain.Senior), i.ID, "CREATE_ORDER", uuid.NewString(), PaymentAction{ExpectedVersion: 1}, "fixture"); err != nil {
		t.Fatal(err)
	}
	f.work(t)
	if _, err := f.s.DB.Pool.Exec(ctx, `UPDATE payment_intents SET payment_status='RECONCILING' WHERE id=$1`, i.ID); err != nil {
		t.Fatal(err)
	}
	var initial int64
	if err := f.s.DB.Pool.QueryRow(ctx, `SELECT history_revision FROM households WHERE id=$1`, actor(domain.Senior).HouseholdID).Scan(&initial); err != nil {
		t.Fatal(err)
	}
	if err := f.s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		p, err := f.s.paymentLock(ctx, tx, i.ID)
		if err != nil {
			return err
		}
		requested, held, captured, err := f.s.totals(ctx, tx, p)
		if err == nil && (requested != 4001 || held != 0 || captured != 0) {
			t.Errorf("unconfirmed authorization counted as held: %d/%d/%d", requested, held, captured)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.DB.Pool.Exec(ctx, `UPDATE payment_intents SET authorization_id='REV_AUTH',payment_status='HELD' WHERE id=$1`, i.ID); err != nil {
		t.Fatal(err)
	}
	var afterHold int64
	if err := f.s.DB.Pool.QueryRow(ctx, `SELECT history_revision FROM households WHERE id=$1`, actor(domain.Senior).HouseholdID).Scan(&afterHold); err != nil {
		t.Fatal(err)
	}
	if afterHold <= initial {
		t.Error("new confirmed financial exposure did not invalidate history revision")
	}
	if _, err := f.s.DB.Pool.Exec(ctx, `UPDATE payment_intents SET payment_status='CAPTURE_PENDING' WHERE id=$1`, i.ID); err != nil {
		t.Fatal(err)
	}
	var pending int64
	_ = f.s.DB.Pool.QueryRow(ctx, `SELECT history_revision FROM households WHERE id=$1`, actor(domain.Senior).HouseholdID).Scan(&pending)
	if pending != afterHold {
		t.Error("capture reservation invalidated its unchanged held totals")
	}
	if _, err := f.s.DB.Pool.Exec(ctx, `UPDATE payment_intents SET capture_id='REV_CAPTURE',payment_status='CAPTURED' WHERE id=$1`, i.ID); err != nil {
		t.Fatal(err)
	}
	var completed int64
	_ = f.s.DB.Pool.QueryRow(ctx, `SELECT history_revision FROM households WHERE id=$1`, actor(domain.Senior).HouseholdID).Scan(&completed)
	if completed <= pending {
		t.Error("completed capture did not invalidate canonical history revision")
	}
}

func TestP02ReconciliationLockOrderAllowsConcurrentCleanupQueue(t *testing.T) {
	f := setupPayments(t)
	i := f.held(t)
	ctx := context.Background()
	worker := f.independentWorker(t)
	if err := f.s.DB.WithTx(ctx, func(tx pgx.Tx) error { return f.s.queue(ctx, tx, "PAYMENT_RECONCILE", i.ID, "reconcile:"+i.ID) }); err != nil {
		t.Fatal(err)
	}
	jobs := &store.Jobs{DB: worker.DB, Clock: f.clock}
	job, err := jobs.Lease(ctx, []string{"PAYMENT_RECONCILE"}, time.Minute)
	if err != nil || job == nil {
		t.Fatal(err)
	}
	var workerPID int
	if err = worker.DB.Pool.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&workerPID); err != nil {
		t.Fatal(err)
	}
	caller, err := f.s.DB.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer caller.Rollback(ctx)
	if _, err = caller.Exec(ctx, `SELECT id FROM households WHERE id=$1 FOR UPDATE`, actor(domain.Senior).HouseholdID); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- worker.RunPaymentJob(ctx, *job) }()
	blocked := false
	for until := time.Now().Add(5 * time.Second); time.Now().Before(until); {
		if err = f.s.DB.Pool.QueryRow(ctx, `SELECT COALESCE(wait_event_type='Lock',false) FROM pg_stat_activity WHERE pid=$1`, workerPID).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		caller.Rollback(ctx)
		<-finished
		t.Fatal("worker never reached household contention")
	}
	_, lockErr := caller.Exec(ctx, `SELECT id FROM jobs WHERE id=$1 FOR UPDATE NOWAIT`, job.ID)
	caller.Rollback(ctx)
	if err = <-finished; err != nil {
		t.Fatal(err)
	}
	if lockErr != nil {
		t.Fatal("worker held job lock while waiting for household; cleanup queue would deadlock", lockErr)
	}
}

func TestP02CapturedTruthSurvivesReorderedEventsAndRetention(t *testing.T) {
	f := setupPayments(t)
	i := f.held(t)
	ev := f.evidence(t, i)
	ctx := context.Background()
	if err := f.s.ReviewApprove(ctx, actor(domain.Reviewer), i.ID, ev, "Synthetic event fixture", "fixture"); err != nil {
		t.Fatal(err)
	}
	f.work(t)
	i, err := f.s.Read(ctx, actor(domain.Senior), i.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"PAYMENT.CAPTURE.PENDING", "PAYMENT.AUTHORIZATION.CREATED", "PAYMENT.AUTHORIZATION.VOIDED"} {
		resource := *i.Checkout.AuthorizationID
		if kind == "PAYMENT.CAPTURE.PENDING" {
			resource = *i.Checkout.CaptureID
		}
		raw := []byte(fmt.Sprintf(`{"id":%q,"event_type":%q,"resource":{"id":%q,"supplementary_data":{"related_ids":{"order_id":%q}}}}`, kind, kind, resource, *i.Checkout.ProviderOrderID))
		for n := 0; n < 2; n++ {
			if err := f.s.IngestWebhook(ctx, http.Header{}, raw); err != nil {
				t.Fatal(err)
			}
		}
		f.work(t)
		if f.status(t, i.ID) != domain.Captured {
			t.Fatal("reordered event reversed completed capture")
		}
	}
	f.clock.now = f.clock.now.Add(24 * time.Hour)
	if err := f.s.TickPayments(ctx); err != nil {
		t.Fatal(err)
	}
	var events, rawBodies int
	if err := f.s.DB.Pool.QueryRow(ctx, `SELECT count(*),count(raw_body) FROM paypal_events WHERE mapped_intent=$1 AND processed_at IS NOT NULL`, i.ID).Scan(&events, &rawBodies); err != nil || events != 3 || rawBodies != 0 {
		t.Fatal("events duplicated or raw retention exceeded", events, rawBodies, err)
	}
}

func TestP02DeclinedCaptureAndUnknownVoidNeverBecomeFalseSuccess(t *testing.T) {
	f := setupPayments(t)
	i := f.held(t)
	ev := f.evidence(t, i)
	ctx := context.Background()
	if err := f.s.ReviewApprove(ctx, actor(domain.Reviewer), i.ID, ev, "Synthetic declined result fixture", "fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.DB.Pool.Exec(ctx, `UPDATE payment_operations SET state='DISPATCHED',dispatched_at=$2 WHERE intent_id=$1 AND kind='CAPTURE'`, i.ID, f.clock.Now()); err != nil {
		t.Fatal(err)
	}
	r := f.p.states[i.ID]
	r.Status = "HELD"
	r.CaptureID = "DECLINED_CAPTURE"
	r.CaptureStatus = "DECLINED"
	f.p.states[i.ID] = r
	f.work(t)
	if f.status(t, i.ID) != domain.Held || f.p.calls["CAPTURE"] != 0 {
		t.Fatal("decline became completed/replayed")
	}
	f.p.failVoid = true
	if _, err := f.s.Cancel(ctx, actor(domain.Reviewer), i.ID, 1, "decline cleanup"); err != nil {
		t.Fatal(err)
	}
	f.work(t)
	if f.status(t, i.ID) == domain.Voided {
		t.Fatal("unknown void became stopped")
	}
	f.p.failVoid = false
	f.clock.now = f.clock.now.Add(time.Minute)
	f.work(t)
	if f.status(t, i.ID) != domain.Voided || f.p.calls["VOID"] != 2 {
		t.Fatal("same-command void did not recover", f.p.calls)
	}
}
