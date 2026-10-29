package httpapi_test

import (
	"context"
	"encoding/json"
	"github.com/ramst/pause/internal/domain"
	"github.com/ramst/pause/internal/investigation"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

func browserJSON(v any) []byte { b, _ := json.Marshal(v); return b }

// Compiled only by `go test`: no product config/HTTP path installs these adapters.
type browserProvider struct {
	mu      sync.Mutex
	results map[string]domain.ProviderResult
}

func (p *browserProvider) invoke(kind string, c domain.ProviderCommand) (domain.ProviderResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r, ok := p.results[c.IntentID]
	if !ok {
		r = domain.ProviderResult{OrderID: "FIXTURE_ORDER_" + c.IntentID, Status: "AWAITING_PAYER", Confirmed: true, AmountMinor: c.Binding.AmountMinor, Currency: c.Binding.Currency, PayeeID: c.Binding.PayeeID, Environment: "sandbox", OrderStatus: "CREATED"}
	}
	switch kind {
	case "AUTHORIZE":
		r.AuthorizationID = "FIXTURE_AUTH_" + c.IntentID
		r.AuthorizationStatus = "CREATED"
		r.Status = "HELD"
	case "CAPTURE":
		r.CaptureID = "FIXTURE_CAPTURE_" + c.IntentID
		r.CaptureStatus = "COMPLETED"
		r.AuthorizationStatus = "CAPTURED"
		r.Status = "CAPTURED"
	case "VOID":
		r.AuthorizationStatus = "VOIDED"
		r.Status = "VOIDED"
	}
	p.results[c.IntentID] = r
	return r, nil
}
func (p *browserProvider) CreateOrder(_ context.Context, c domain.ProviderCommand) (domain.ProviderResult, error) {
	return p.invoke("CREATE_ORDER", c)
}
func (p *browserProvider) Authorize(_ context.Context, c domain.ProviderCommand) (domain.ProviderResult, error) {
	return p.invoke("AUTHORIZE", c)
}
func (p *browserProvider) Capture(_ context.Context, c domain.ProviderCommand) (domain.ProviderResult, error) {
	return p.invoke("CAPTURE", c)
}
func (p *browserProvider) Void(_ context.Context, c domain.ProviderCommand) (domain.ProviderResult, error) {
	return p.invoke("VOID", c)
}
func (p *browserProvider) Reconcile(_ context.Context, c domain.ProviderCommand) (domain.ProviderResult, error) {
	return p.invoke("READ", c)
}

func TestP05BrowserJourneys(t *testing.T) {
	if os.Getenv("P05_BROWSER") != "1" {
		t.Skip("P05_BROWSER=1 runs Chromium journeys against isolated PostgreSQL and deterministic adapters")
	}
	runBrowserFixture(t, "browser.mjs", true)
}

func TestP06CheckoutApprovalRecovery(t *testing.T) {
	if os.Getenv("P05_BROWSER") != "1" {
		t.Skip("P05_BROWSER=1 runs isolated Chromium checkout recovery")
	}
	runBrowserFixture(t, "checkout-recovery.mjs", false)
}

func runBrowserFixture(t *testing.T, script string, requireCapture bool) {
	t.Helper()
	f := newFixture(t)
	f.clock.now = time.Now().UTC()
	f.svc.Config.Environment = "test"
	f.svc.Config.WebDir = "../../web/dist"
	f.svc.Config.MerchantLabel = "Synthetic Sandbox Store"
	f.svc.Config.PayPalClientID = "TEST_ONLY"
	f.svc.Config.DemoEnabled = true
	f.svc.Provider = &browserProvider{results: map[string]domain.ProviderResult{}}
	browserEngine(t, f)
	server := httptest.NewServer(f.handler)
	defer server.Close()
	f.svc.Config.Origin = server.URL
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Millisecond):
				_, _ = f.svc.WorkPaymentOnce(ctx)
			}
		}
	}()
	defer func() { stop(); <-done }()
	testCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(testCtx, "node", "../../web/tests/"+script)
	command.Env = append(os.Environ(), "P05_BASE_URL="+server.URL)
	out, e := command.CombinedOutput()
	t.Log(string(out))
	if e != nil {
		t.Fatal(e)
	}
	var captured, voided int
	if e = f.db.Pool.QueryRow(context.Background(), `SELECT count(*) FILTER(WHERE payment_status='CAPTURED'),count(*) FILTER(WHERE payment_status='VOIDED') FROM payment_intents`).Scan(&captured, &voided); e != nil || (requireCapture && captured < 1) || voided < 1 {
		t.Fatal("canonical terminal outcomes", captured, voided, e)
	}
}
func browserEngine(t *testing.T, f *fixture) *string {
	t.Helper()
	e := &investigation.Engine{HTTP: &http.Client{Timeout: 5 * time.Second}, CorpusHash: strings.Repeat("c", 64), Corpus: []investigation.Playbook{{ID: "approved", Revision: 1, Title: "Original FTC summary", Summary: "Original reviewed text.", URL: "https://consumer.ftc.gov/articles/scams", Publisher: "Federal Trade Commission", Language: "en", Active: true, Reviewed: "2026-10-27"}}}
	for path, dst := range map[string]any{"../../models/classifier.manifest.json": &e.Classifier, "../../models/encoder.manifest.json": &e.Encoder} {
		raw, err := os.ReadFile(path)
		if err != nil || json.Unmarshal(raw, dst) != nil {
			t.Fatal("manifest", err)
		}
	}
	mode := "valid"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode == "outage" {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var out any
		switch {
		case r.URL.Path == "/readyz":
			out = map[string]any{"status": "OK", "schema_version": domain.ContractVersion, "classifier_artifact_hash": e.Classifier.ArtifactHash, "encoder_artifact_hash": e.Encoder.ArtifactHash}
			if mode == "wrong-ready-artifact" {
				out = map[string]any{"status": "OK", "schema_version": domain.ContractVersion}
			}
		case r.URL.Path == "/classify":
			var in domain.InferenceInput
			_ = json.NewDecoder(r.Body).Decode(&in)
			scores := map[string]float64{}
			for _, label := range domain.Labels {
				scores[label] = 0
			}
			status := domain.InferenceOK
			if mode == "abstain" {
				status = domain.Abstain
			}
			if mode == "unsupported" {
				status = domain.Unsupported
				scores = nil
			}
			out = domain.Classification{SchemaVersion: domain.ContractVersion, IntentID: in.IntentID, IntentVersion: in.IntentVersion, ContentDigest: in.ContentDigest, ModelVersion: e.Classifier.ModelVersion, ArtifactHash: e.Classifier.ArtifactHash, LabelSchemaVersion: e.Classifier.LabelSchemaVersion, PreprocessingVersion: e.Classifier.PreprocessingVersion, LabelScores: scores, TriggeredLabels: []string{}, Status: status}
		case r.URL.Path == "/embed":
			var in domain.EmbedInput
			_ = json.NewDecoder(r.Body).Decode(&in)
			v := make([]float64, 384)
			v[0] = 1
			out = domain.Embedding{SchemaVersion: domain.ContractVersion, InputDigest: in.InputDigest, EncoderVersion: e.Encoder.EncoderVersion, ArtifactHash: e.Encoder.ArtifactHash, PreprocessingVersion: e.Encoder.PreprocessingVersion, Normalized: true, Vector: v}
		case strings.HasSuffix(r.URL.Path, "/_mapping"):
			out = map[string]any{investigation.IndexVersion: map[string]any{"mappings": map[string]any{"_meta": map[string]any{"mapping_version": "playbooks-v1", "encoder_version": e.Encoder.EncoderVersion, "corpus_hash": e.CorpusHash, "ingest_complete": "true"}, "properties": map[string]any{"embedding": map[string]any{"dims": 384, "similarity": "cosine"}}}}}
		case strings.HasSuffix(r.URL.Path, "/_count"):
			out = map[string]any{"count": 1}
		case strings.HasSuffix(r.URL.Path, "/_mget"):
			p := e.Corpus[0]
			if mode == "inactive" {
				p.Active = false
			}
			if mode == "revision" {
				p.Revision++
			}
			if mode == "changed-content" {
				p.Summary = "Unreviewed replacement"
			}
			var doc map[string]any
			_ = json.Unmarshal(browserJSON(p), &doc)
			doc["encoder_version"] = e.Encoder.EncoderVersion
			out = map[string]any{"docs": []any{map[string]any{"_index": investigation.IndexVersion, "_id": p.ID, "found": mode != "deleted", "_source": doc}}}
		case strings.HasSuffix(r.URL.Path, "/_search"):
			hits := []any{}
			if mode != "empty" {
				hits = append(hits, map[string]any{"_index": investigation.IndexVersion, "_id": "approved", "_source": map[string]any{"playbook_id": "approved", "revision": 1, "encoder_version": e.Encoder.EncoderVersion}})
			}
			out = map[string]any{"hits": map[string]any{"hits": hits}}
		default:
			t.Error("unexpected dependency route", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(server.Close)
	e.Config.MLURL = server.URL
	e.Config.ElasticURL = server.URL
	e.Config.PlaybookAlias = "pause-playbooks"
	f.svc.Investigation = e
	if err := f.svc.ApproveReleases(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &mode
}
