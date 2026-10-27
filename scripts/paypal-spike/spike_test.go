package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, value any) *http.Response {
	var b []byte
	if value != nil {
		b, _ = json.Marshal(value)
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Paypal-Debug-Id": []string{"fixture-debug"}}, Body: io.NopCloser(bytes.NewReader(b))}
}

type fakePayPal struct {
	t                        *testing.T
	dir                      string
	orders                   map[string]*order
	createKeys               map[string]string
	creates, captures, voids int
	loseCapture              bool
	mismatch                 bool
}

func fixture(t *testing.T) (*spike, *fakePayPal) {
	t.Helper()
	c := config{AppEnv: "test", AppURL: "http://127.0.0.1:8787", PayPalURL: sandboxURL, ClientID: "FIXTURE_CLIENT", ClientSecret: "FIXTURE_CLIENT_SECRET", MerchantID: "MERCHANT123", MerchantLabel: "Synthetic Store", DeadlineMinutes: 30, EvidenceDir: t.TempDir()}
	store, err := openStore(c.EvidenceDir)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakePayPal{t: t, dir: c.EvidenceDir, orders: map[string]*order{}, createKeys: map[string]string{}}
	p := newPayPal(c)
	p.http = &http.Client{Transport: transportFunc(f.roundTrip)}
	return &spike{config: c, store: store, paypal: p, now: time.Now, evidenceSource: "offline-fixture"}, f
}

func (f *fakePayPal) roundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "api-m.sandbox.paypal.com" {
		f.t.Fatalf("unexpected outbound host: %s", req.URL.Host)
	}
	path := req.URL.Path
	if path == "/v1/oauth2/token" {
		return response(200, map[string]any{"access_token": "FIXTURE_ACCESS_TOKEN_SECRET", "expires_in": 300}), nil
	}
	if req.Method == "POST" {
		key := req.Header.Get("PayPal-Request-Id")
		if key == "" {
			f.t.Fatal("mutation has no persisted key")
		}
		st, err := openStore(f.dir)
		if err != nil {
			f.t.Fatal(err)
		}
		found := false
		for _, r := range st.data.Runs {
			for _, op := range r.Operations {
				if op.RequestID == key && r.Pending == op.Kind && op.Outcome == "DISPATCHED" {
					found = true
				}
			}
		}
		if !found {
			f.t.Fatal("provider mutation dispatched before durable reservation")
		}
	}
	if path == "/v2/checkout/orders" {
		key := req.Header.Get("PayPal-Request-Id")
		if id := f.createKeys[key]; id != "" {
			return response(200, f.orders[id]), nil
		}
		var o order
		if err := json.NewDecoder(req.Body).Decode(&o); err != nil {
			f.t.Fatal(err)
		}
		if o.Intent != "AUTHORIZE" || len(o.Units) != 1 {
			f.t.Fatal("create is not authorize-only")
		}
		f.creates++
		o.ID = fmt.Sprintf("ORDER%06d", f.creates)
		o.Status = "CREATED"
		f.orders[o.ID] = &o
		f.createKeys[key] = o.ID
		if f.mismatch {
			o.Units[0].Payee.MerchantID = "WRONGMERCHANT"
		}
		return response(201, o), nil
	}
	for _, o := range f.orders {
		if path == "/v2/checkout/orders/"+o.ID {
			return response(200, o), nil
		}
		if path == "/v2/checkout/orders/"+o.ID+"/authorize" {
			if o.Status != "APPROVED" {
				return response(422, map[string]string{"name": "ORDER_NOT_APPROVED"}), nil
			}
			a := payment{ID: "AUTH" + o.ID, Status: "CREATED", Amount: o.Units[0].Amount}
			o.Units[0].Payments.Authorizations = []payment{a}
			o.Status = "COMPLETED"
			return response(201, o), nil
		}
		if len(o.Units[0].Payments.Authorizations) == 0 {
			continue
		}
		a := &o.Units[0].Payments.Authorizations[0]
		if path == "/v2/payments/authorizations/"+a.ID {
			return response(200, a), nil
		}
		if path == "/v2/payments/authorizations/"+a.ID+"/capture" {
			f.captures++
			a.Status = "CAPTURED"
			p := payment{ID: "CAP" + o.ID, Status: "COMPLETED", Amount: a.Amount, FinalCapture: true}
			p.Supplementary.Related.AuthorizationID = a.ID
			o.Units[0].Payments.Captures = []payment{p}
			if f.loseCapture {
				f.loseCapture = false
				return nil, errors.New("synthetic response loss after provider commit")
			}
			return response(201, p), nil
		}
		if path == "/v2/payments/authorizations/"+a.ID+"/void" {
			if a.Status == "CAPTURED" {
				f.t.Fatal("void attempted on captured authorization")
			}
			f.voids++
			a.Status = "VOIDED"
			return response(204, nil), nil
		}
		for _, p := range o.Units[0].Payments.Captures {
			if path == "/v2/payments/captures/"+p.ID {
				return response(200, p), nil
			}
		}
	}
	f.t.Fatalf("unexpected fixture request: %s %s", req.Method, path)
	return nil, errors.New("unexpected request")
}

func held(t *testing.T, s *spike, f *fakePayPal, scenarioID string) *run {
	t.Helper()
	r, err := s.create(context.Background(), scenarioID)
	if err != nil {
		t.Fatal(err)
	}
	f.orders[r.OrderID].Status = "APPROVED"
	if err = s.action(context.Background(), r, "authorize", r.OrderID); err != nil {
		t.Fatal(err)
	}
	if r.State != "HELD" || !r.AuthorizedWithoutCapture || f.captures != 0 {
		t.Fatal("approval did not preserve uncaptured hold")
	}
	return r
}

func TestSeparateAuthorizeCaptureAndVoid(t *testing.T) {
	s, f := fixture(t)
	ctx := context.Background()
	r := held(t, s, f, "confirmed-purchase")
	if err := s.action(ctx, r, "capture", ""); err != nil {
		t.Fatal(err)
	}
	if r.State != "CAPTURED" || r.CaptureStatus != "COMPLETED" || f.captures != 1 {
		t.Fatal("capture not confirmed")
	}
	if err := s.action(ctx, r, "void", ""); err == nil {
		t.Fatal("captured authorization allowed void")
	}
	other, err := s.create(ctx, "suspicious-request")
	if err != nil {
		t.Fatal(err)
	}
	f.orders[other.OrderID].Status = "APPROVED"
	if err = s.action(ctx, other, "authorize", other.OrderID); err != nil {
		t.Fatal(err)
	}
	if err = s.action(ctx, other, "capture", ""); err == nil {
		t.Fatal("suspicious scenario allowed capture")
	}
	if err = s.action(ctx, other, "void", ""); err != nil {
		t.Fatal(err)
	}
	if other.State != "VOIDED" || other.AuthorizationStatus != "VOIDED" || f.voids != 1 || other.AuthorizationID == r.AuthorizationID {
		t.Fatal("separate void not confirmed")
	}
	files, _ := filepath.Glob(filepath.Join(s.config.EvidenceDir, "*.json"))
	for _, p := range files {
		b, _ := os.ReadFile(p)
		for _, secret := range []string{"FIXTURE_CLIENT_SECRET", "FIXTURE_ACCESS_TOKEN_SECRET", "email_address", "payer_id"} {
			if bytes.Contains(b, []byte(secret)) {
				t.Fatalf("private provider data persisted in %s", filepath.Base(p))
			}
		}
	}
}

func TestDuplicateCreateReusesDurableKeyAcrossReload(t *testing.T) {
	s, f := fixture(t)
	ctx := context.Background()
	r, err := s.create(ctx, "confirmed-purchase")
	if err != nil {
		t.Fatal(err)
	}
	key := r.Operations["create"].RequestID
	id := r.OrderID
	s.store, err = openStore(s.config.EvidenceDir)
	if err != nil {
		t.Fatal(err)
	}
	r = s.store.data.Runs[r.ID]
	if err = s.action(ctx, r, "duplicate-create", ""); err != nil {
		t.Fatal(err)
	}
	if r.OrderID != id || r.Operations["create"].RequestID != key || r.Operations["create"].Attempts != 2 || !r.DuplicateCreateSameID || f.creates != 1 {
		t.Fatal("duplicate did not reuse the original order and key")
	}
}

func TestLostCaptureReconcilesAfterRestartWithoutSecondMutation(t *testing.T) {
	s, f := fixture(t)
	r := held(t, s, f, "confirmed-purchase")
	f.loseCapture = true
	if err := s.action(context.Background(), r, "capture", ""); err == nil {
		t.Fatal("lost response must be unknown")
	}
	if r.State != "RECONCILING" || r.Pending != "capture" {
		t.Fatal("unknown capture was not preserved")
	}
	if err := s.action(context.Background(), r, "void", ""); err == nil {
		t.Fatal("competing void was accepted")
	}
	st, err := openStore(s.config.EvidenceDir)
	if err != nil {
		t.Fatal(err)
	}
	s.store = st
	r = st.data.Runs[r.ID]
	key := r.Operations["capture"].RequestID
	if err = s.action(context.Background(), r, "retry", ""); err != nil {
		t.Fatal(err)
	}
	if r.State != "CAPTURED" || r.Pending != "" || f.captures != 1 || r.Operations["capture"].RequestID != key {
		t.Fatal("capture was repeated instead of reconciled")
	}
}

func TestDeadlineAndMismatchedApprovalBlockCapture(t *testing.T) {
	s, f := fixture(t)
	ctx := context.Background()
	r, err := s.create(ctx, "confirmed-purchase")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.action(ctx, r, "authorize", "OTHERORDER123"); err == nil {
		t.Fatal("foreign order approval accepted")
	}
	f.orders[r.OrderID].Status = "APPROVED"
	if err = s.action(ctx, r, "authorize", r.OrderID); err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return r.Deadline }
	if err = s.action(ctx, r, "capture", ""); err == nil || f.captures != 0 {
		t.Fatal("exact deadline permitted capture")
	}
	if err = s.action(ctx, r, "void", ""); err != nil {
		t.Fatal(err)
	}
}

func TestProviderMismatchAndStorageFailureStopMutations(t *testing.T) {
	s, f := fixture(t)
	f.mismatch = true
	r, err := s.create(context.Background(), "confirmed-purchase")
	if err == nil || r.State != "RECONCILING" || r.Pending != "create" {
		t.Fatal("merchant mismatch did not fail closed")
	}
	s2, f2 := fixture(t)
	s2.store.dir = filepath.Join(t.TempDir(), "missing-directory")
	if _, err = s2.create(context.Background(), "confirmed-purchase"); err == nil || f2.creates != 0 {
		t.Fatal("provider request happened before durable save")
	}
}

func TestSandboxConfigurationAndNamedMissingSecrets(t *testing.T) {
	c := config{AppEnv: "demo", PayPalURL: sandboxURL, AppURL: "http://127.0.0.1:8787"}
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"https://api-m.paypal.com", "https://api-m.sandbox.paypal.com.evil.test", "https://api-m.sandbox.paypal.com/", "https://user:pass@api-m.sandbox.paypal.com", "https://api-m.sandbox.paypal.com?live=1"} {
		c.PayPalURL = host
		if c.validate() == nil {
			t.Fatalf("forbidden host accepted: %s", host)
		}
	}
	if err := c.requirePayPal(); err == nil || !strings.Contains(err.Error(), "PAYPAL_CLIENT_SECRET") {
		t.Fatal("missing secret error does not identify configuration name")
	}
}

func TestLegacyAISettingsDoNotBlockPaymentConfiguration(t *testing.T) {
	t.Setenv("AI_API_KEY", "")
	t.Setenv("AI_MODEL", "")
	t.Setenv("AI_TIMEOUT_SECONDS", "invalid-legacy-value")
	t.Setenv("AI_MAX_OUTPUT_TOKENS", "invalid-legacy-value")
	c, err := getConfig(t.TempDir())
	if err != nil {
		t.Fatalf("unused legacy settings blocked payment configuration: %v", err)
	}
	if _, err = c.legacyAIConfig(); err == nil || !strings.Contains(err.Error(), "AI_TIMEOUT_SECONDS") {
		t.Fatal("explicit legacy command must still validate its settings by name")
	}
}

func TestHTTPRequiresHostOriginCSRFAndBoundInput(t *testing.T) {
	s, _ := fixture(t)
	u, _ := url.Parse(s.config.AppURL)
	server := &localServer{spike: s, csrf: "fixture-csrf", appURL: u}
	h := server.handler()
	for _, tc := range []struct {
		host, origin, csrf, body string
		want                     int
	}{
		{"evil.test", s.config.AppURL, "fixture-csrf", `{"scenario":"confirmed-purchase"}`, 403},
		{u.Host, "https://evil.test", "fixture-csrf", `{"scenario":"confirmed-purchase"}`, 403},
		{u.Host, s.config.AppURL, "wrong", `{"scenario":"confirmed-purchase"}`, 403},
		{u.Host, s.config.AppURL, "fixture-csrf", `{"scenario":"confirmed-purchase","amount_minor":1}`, 400},
		{u.Host, s.config.AppURL, "fixture-csrf", `{"scenario":"confirmed-purchase"}`, 201},
	} {
		req := httptest.NewRequest("POST", s.config.AppURL+"/api/runs", strings.NewReader(tc.body))
		req.Host = tc.host
		req.Header.Set("Origin", tc.origin)
		req.Header.Set("X-CSRF-Token", tc.csrf)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Fatalf("got HTTP %d want %d: %s", w.Code, tc.want, w.Body.String())
		}
	}
	for _, path := range []string{"/", "/app.js", "/style.css", "/healthz", "/api/config", "/api/runs"} {
		req := httptest.NewRequest("GET", s.config.AppURL+path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("%s HTTP %d", path, w.Code)
		}
		if strings.Contains(w.Body.String(), s.config.ClientSecret) {
			t.Fatal("server exposed a secret")
		}
	}
}

func TestWebhookNeverBypassesPostback(t *testing.T) {
	c := config{PayPalURL: sandboxURL}
	p := newPayPal(c)
	event := json.RawMessage(`{"id":"WH-fixture","event_type":"PAYMENT.AUTHORIZATION.CREATED"}`)
	if p.verifyWebhook(context.Background(), http.Header{}, event) == nil {
		t.Fatal("missing webhook config accepted")
	}
	c.WebhookID = "WEBHOOK_ID"
	p.config = c
	if p.verifyWebhook(context.Background(), http.Header{}, event) == nil {
		t.Fatal("simulator ID accepted")
	}
	c.WebhookID = "APP_WEBHOOK123"
	p.config = c
	p.token = "fixture"
	p.tokenExpiry = time.Now().Add(time.Hour)
	h := http.Header{}
	for _, k := range []string{"PAYPAL-AUTH-ALGO", "PAYPAL-CERT-URL", "PAYPAL-TRANSMISSION-ID", "PAYPAL-TRANSMISSION-SIG", "PAYPAL-TRANSMISSION-TIME"} {
		h.Set(k, "fixture")
	}
	for _, status := range []string{"FAILURE", "SUCCESS"} {
		p.http = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/v1/notifications/verify-webhook-signature" {
				t.Fatal("wrong verification endpoint")
			}
			return response(200, map[string]string{"verification_status": status}), nil
		})}
		err := p.verifyWebhook(context.Background(), h, event)
		if (err == nil) != (status == "SUCCESS") {
			t.Fatal("verification result ignored")
		}
	}
}

func TestModelSchemaQuotaAndValidOutput(t *testing.T) {
	valid := `{"claimed_requester":"Alex","purpose":"emergency kit","amount_minor":90000,"currency":"USD","urgent":true,"secrecy":true,"source_excerpts":["Please pay $900 USD for an emergency kit right now."]}`
	if _, err := validateClaims([]byte(valid), scenarios[1].Message); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.Replace(valid, "90000", "90000.5", 1), strings.Replace(valid, "true", "null", 1), strings.Replace(valid, "Please pay $900 USD for an emergency kit right now.", "fabricated quote", 1), strings.Replace(valid, `"currency":"USD",`, "", 1)} {
		if _, err := validateClaims([]byte(bad), scenarios[1].Message); err == nil {
			t.Fatal("invalid claims accepted")
		}
	}
	c := config{AIProvider: "openai", AIModel: "fixture-model", AIKey: "FIXTURE_AI_SECRET", AITimeout: time.Second, AIMaxTokens: 800}
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		return response(429, map[string]any{"error": map[string]string{"code": "insufficient_quota", "type": "insufficient_quota", "message": "FIXTURE_AI_SECRET must never be logged"}}), nil
	})}
	record, err := runAI(context.Background(), c, client)
	b, _ := json.Marshal(record)
	if err == nil || record["failure_category"] != "quota" || bytes.Contains(b, []byte(c.AIKey)) {
		t.Fatal("quota failure not safely recorded")
	}
	client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		return response(200, map[string]any{"model": "fixture-model-snapshot", "status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]string{"type": "output_text", "text": valid}}}}}), nil
	})
	record, err = runAI(context.Background(), c, client)
	if err != nil || record["status"] != "schema-valid" {
		t.Fatalf("valid model response rejected: %v", err)
	}
}

func TestLiteralEnvAndInvalidState(t *testing.T) {
	key := "P00_LITERAL_FIXTURE"
	t.Setenv(key, "")
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte(key+"='$(do-not-execute)'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := loadEnv(path); err != nil {
		t.Fatal(err)
	}
	if os.Getenv(key) != "" {
		t.Fatal("explicit process environment was overwritten")
	}
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	if err := loadEnv(path); err != nil {
		t.Fatal(err)
	}
	if os.Getenv(key) != "$(do-not-execute)" {
		t.Fatal("literal environment content was expanded or changed")
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"schema_version":99}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := openStore(dir); err == nil {
		t.Fatal("invalid state was silently reset")
	}
}
