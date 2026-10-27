package paypal

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/ramst/pause/internal/domain"
)

func command() domain.ProviderCommand {
	b, _ := (domain.Binding{IntentID: uuid.NewString(), Version: 2, AmountMinor: 4001, Currency: "USD", PayeeID: "TEST_MERCHANT", SubjectContactID: uuid.NewString(), Purpose: "Synthetic purchase"}).Canonical()
	d, _ := b.Digest()
	return domain.ProviderCommand{IntentID: b.IntentID, Version: b.Version, Digest: d, Binding: b, RequestID: uuid.NewString(), Environment: Sandbox}
}
func fixtureOrder(in domain.ProviderCommand, authStatus, captureStatus string) order {
	o := order{ID: "ORDER123", Intent: "AUTHORIZE", Status: "COMPLETED", Units: []unit{{Reference: in.IntentID + ":v2", Custom: in.Digest, Amount: money{"USD", "40.01"}}}}
	o.Units[0].Payee.ID = in.Binding.PayeeID
	if authStatus != "" {
		o.Units[0].Payments.Authorizations = []payment{{ID: "AUTH123", Status: authStatus, Amount: money{"USD", "40.01"}}}
	}
	if captureStatus != "" {
		o.Units[0].Payments.Captures = []payment{{ID: "CAP123", Status: captureStatus, Amount: money{"USD", "40.01"}, Final: true}}
	}
	return o
}
func fakeClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	c := New("private-client", "private-secret", "REAL_TEST_WEBHOOK")
	c.base = server.URL
	c.http = server.Client()
	return c
}
func TestAdapterExactPayloadTokenRefreshAndVoid204(t *testing.T) {
	in := command()
	var tokens, creates, voids atomic.Int32
	o := fixtureOrder(in, "CREATED", "")
	authStatus := "CREATED"
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/oauth2/token" {
			tokens.Add(1)
			id, secret, ok := r.BasicAuth()
			if !ok || id != "private-client" || secret != "private-secret" {
				t.Error("OAuth credentials missing")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "private-token", "expires_in": 3600})
			return
		}
		if r.Header.Get("Authorization") != "Bearer private-token" {
			t.Error("token missing")
		}
		w.Header().Set("PayPal-Debug-Id", "DEBUG123")
		switch {
		case r.Method == "POST" && r.URL.Path == "/v2/checkout/orders":
			n := creates.Add(1)
			if r.Header.Get("PayPal-Request-Id") != in.RequestID {
				t.Error("durable request ID missing")
			}
			raw, _ := io.ReadAll(r.Body)
			if string(raw) != string(Payload("CREATE_ORDER", in)) {
				t.Error("payload mismatch")
			}
			if n == 1 {
				w.WriteHeader(401)
				return
			}
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(o)
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/void"):
			voids.Add(1)
			if r.Header.Get("PayPal-Request-Id") != in.RequestID {
				t.Error("void idempotency missing")
			}
			authStatus = "VOIDED"
			w.WriteHeader(204)
		case r.URL.Path == "/v2/checkout/orders/ORDER123":
			_ = json.NewEncoder(w).Encode(o)
		case r.URL.Path == "/v2/payments/authorizations/AUTH123":
			_ = json.NewEncoder(w).Encode(payment{ID: "AUTH123", Status: authStatus, Amount: money{"USD", "40.01"}})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	r, e := c.CreateOrder(context.Background(), in)
	if e != nil || r.AuthorizationID != "AUTH123" || r.Status != "HELD" {
		t.Fatalf("create %v %+v", e, r)
	}
	in.OrderID = r.OrderID
	in.AuthorizationID = r.AuthorizationID
	r, e = c.Void(context.Background(), in)
	if e != nil || r.Status != "VOIDED" {
		t.Fatalf("void %v %+v", e, r)
	}
	if tokens.Load() != 2 || creates.Load() != 2 || voids.Load() != 1 {
		t.Fatalf("counts %d %d %d", tokens.Load(), creates.Load(), voids.Load())
	}
}
func TestAdapterRejectsEveryBindingMismatch(t *testing.T) {
	for _, name := range []string{"amount", "currency", "merchant", "intent", "version", "digest", "order", "authorization", "environment", "related"} {
		t.Run(name, func(t *testing.T) {
			in := command()
			o := fixtureOrder(in, "CREATED", "")
			switch name {
			case "amount":
				o.Units[0].Amount.Value = "40.02"
			case "currency":
				o.Units[0].Amount.Currency = "EUR"
			case "merchant":
				o.Units[0].Payee.ID = "OTHER"
			case "intent":
				o.Intent = "CAPTURE"
			case "version":
				o.Units[0].Reference = in.IntentID + ":v1"
			case "digest":
				o.Units[0].Custom = strings.Repeat("a", 64)
			case "order":
				in.OrderID = "OTHER"
			case "authorization":
				in.AuthorizationID = "OTHER"
			case "environment":
				in.Environment = "live"
			case "related":
				o.Units[0].Payments.Authorizations[0].Supplementary.Related.OrderID = "OTHER"
			}
			if _, e := normalize(o, in, ""); e == nil {
				t.Fatal("mismatch accepted")
			}
		})
	}
}
func TestPendingCaptureIsNotCompleted(t *testing.T) {
	in := command()
	for _, status := range []string{"PENDING", "COMPLETED", "DECLINED"} {
		o := fixtureOrder(in, "CREATED", status)
		r, e := normalize(o, in, "")
		if e != nil {
			t.Fatal(e)
		}
		expected := map[string]string{"PENDING": "CAPTURE_PENDING", "COMPLETED": "CAPTURED", "DECLINED": "HELD"}[status]
		if r.Status != expected {
			t.Fatalf("%s -> %s", status, r.Status)
		}
	}
}
func TestProviderFailureClassesAndRedaction(t *testing.T) {
	for _, code := range []int{400, 401, 403, 404, 409, 422, 429, 500, 503} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/oauth2/token" {
					io.WriteString(w, `{"access_token":"private-token","expires_in":3600}`)
					return
				}
				w.WriteHeader(code)
				io.WriteString(w, `{"name":"SYNTHETIC_FAILURE","message":"private-secret private-token private-client"}`)
			})
			_, e := c.CreateOrder(context.Background(), command())
			if e == nil {
				t.Fatal("failure accepted")
			}
			reason, temporary, _ := Failure(e)
			if strings.Contains(e.Error(), "private-") || !strings.HasPrefix(reason, "PROVIDER_") {
				t.Fatal("secret error")
			}
			if temporary != (code == 429 || code >= 500) {
				t.Fatalf("temporary %d = %v", code, temporary)
			}
		})
	}
}
func TestInvalidJSONAndWebhookVerification(t *testing.T) {
	var calls int
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/token") {
			io.WriteString(w, `{"access_token":"token","expires_in":3600}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/verify-webhook-signature") {
			calls++
			var v map[string]any
			_ = json.NewDecoder(r.Body).Decode(&v)
			if v["transmission_id"] == "valid" && v["webhook_id"] == "REAL_TEST_WEBHOOK" {
				io.WriteString(w, `{"verification_status":"SUCCESS"}`)
			} else {
				io.WriteString(w, `{"verification_status":"FAILURE"}`)
			}
			return
		}
		io.WriteString(w, `{broken`)
	})
	_, e := c.CreateOrder(context.Background(), command())
	if e == nil {
		t.Fatal("invalid JSON accepted")
	}
	h := http.Header{}
	for _, name := range []string{"PAYPAL-AUTH-ALGO", "PAYPAL-CERT-URL", "PAYPAL-TRANSMISSION-ID", "PAYPAL-TRANSMISSION-SIG", "PAYPAL-TRANSMISSION-TIME"} {
		h.Set(name, "valid")
	}
	if e = c.VerifyWebhook(context.Background(), h, json.RawMessage(`{"id":"event"}`)); e != nil {
		t.Fatal(e)
	}
	h.Set("PAYPAL-TRANSMISSION-ID", "wrong")
	if c.VerifyWebhook(context.Background(), h, json.RawMessage(`{}`)) == nil {
		t.Fatal("invalid signature accepted")
	}
	h.Set("PAYPAL-TRANSMISSION-ID", "valid")
	c.webhookID = "OTHER_APP_WEBHOOK"
	if c.VerifyWebhook(context.Background(), h, json.RawMessage(`{}`)) == nil {
		t.Fatal("wrong app webhook ID accepted")
	}
	c.webhookID = "WEBHOOK_ID"
	if c.VerifyWebhook(context.Background(), h, json.RawMessage(`{}`)) == nil {
		t.Fatal("simulator accepted")
	}
	if calls != 3 {
		t.Fatal(calls)
	}
}

func TestAuthorizationTerminalTruthIsExplicit(t *testing.T) {
	in := command()
	for status, want := range map[string]string{"DENIED": "CLOSED_UNCAPTURED", "EXPIRED": "CLOSED_UNCAPTURED", "VOIDED": "VOIDED", "CAPTURED": "RECONCILING", "PARTIALLY_CAPTURED": "RECONCILING"} {
		t.Run(status, func(t *testing.T) {
			r, err := normalize(fixtureOrder(in, status, ""), in, "")
			if err != nil || r.Status != want {
				t.Fatalf("%s became %s %v", status, r.Status, err)
			}
		})
	}
}

func TestReturnedResourceBindingsAndCaptureLinks(t *testing.T) {
	for _, name := range []string{"authorization-payee", "capture-payee", "discovered-authorization", "capture-up-link", "wrong-up-link", "live-up-link"} {
		t.Run(name, func(t *testing.T) {
			in := command()
			in.OrderID, in.AuthorizationID, in.CaptureID = "ORDER123", "AUTH123", "CAP123"
			o := fixtureOrder(in, "CREATED", "")
			a := map[string]any{"id": "AUTH123", "status": "CREATED", "amount": money{"USD", "40.01"}, "payee": map[string]string{"merchant_id": in.Binding.PayeeID}}
			p := map[string]any{"id": "CAP123", "status": "COMPLETED", "amount": money{"USD", "40.01"}, "payee": map[string]string{"merchant_id": in.Binding.PayeeID}, "links": []any{map[string]string{"rel": "up", "method": "GET", "href": "https://api-m.sandbox.paypal.com/v2/payments/authorizations/AUTH123"}}}
			if name != "capture-up-link" {
				p["supplementary_data"] = map[string]any{"related_ids": map[string]string{"order_id": "ORDER123", "authorization_id": "AUTH123"}}
			}
			switch name {
			case "authorization-payee":
				a["payee"] = map[string]string{"merchant_id": "OTHER"}
			case "capture-payee":
				p["payee"] = map[string]string{"merchant_id": "OTHER"}
			case "wrong-up-link":
				p["links"] = []any{map[string]string{"rel": "up", "method": "GET", "href": "https://api-m.sandbox.paypal.com/v2/payments/authorizations/OTHER"}}
			case "live-up-link":
				p["links"] = []any{map[string]string{"rel": "up", "method": "GET", "href": "https://api-m.paypal.com/v2/payments/authorizations/AUTH123"}}
			case "discovered-authorization":
				in.AuthorizationID, in.CaptureID = "", ""
				o = fixtureOrder(in, "CREATED", "COMPLETED")
				o.Units[0].Payments.Captures[0].Supplementary.Related.AuthorizationID = "OTHER"
				if _, err := normalize(o, in, ""); err == nil {
					t.Fatal("capture linked to another discovered authorization accepted")
				}
				return
			}
			c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/oauth2/token":
					io.WriteString(w, `{"access_token":"fixture-token","expires_in":3600}`)
				case "/v2/checkout/orders/ORDER123":
					json.NewEncoder(w).Encode(o)
				case "/v2/payments/authorizations/AUTH123":
					json.NewEncoder(w).Encode(a)
				case "/v2/payments/captures/CAP123":
					json.NewEncoder(w).Encode(p)
				default:
					t.Error("unexpected path")
					w.WriteHeader(404)
				}
			})
			r, err := c.Reconcile(context.Background(), in)
			if name == "capture-up-link" {
				if err != nil || r.Status != "CAPTURED" {
					t.Fatalf("documented authorization up link rejected: %s %v", r.Status, err)
				}
			} else if err == nil {
				t.Fatal("mismatched returned resource accepted")
			}
		})
	}
}
