// Package paypal owns sandbox credentials and strict provider normalization.
package paypal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ramst/pause/internal/domain"
)

const Sandbox = "sandbox"

type Error struct {
	Code, DebugID      string
	HTTP               int
	Temporary, Unknown bool
}

func (e *Error) Error() string { return e.Code } // Never include bodies, URLs, credentials or payer data.
func Failure(err error) (string, bool, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e.Code, e.Temporary, e.Unknown
	}
	return "PROVIDER_UNAVAILABLE", true, true
}

type Client struct {
	base, clientID, secret, webhookID string
	http                              *http.Client
	mu                                sync.Mutex
	token                             string
	expires                           time.Time
}

func New(clientID, secret, webhookID string) *Client {
	return &Client{base: "https://api-m.sandbox.paypal.com", clientID: clientID, secret: secret, webhookID: webhookID, http: &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

var safeID = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)

func safe(s string) string {
	if safeID.MatchString(s) {
		return s
	}
	return ""
}
func response(resp *http.Response) ([]byte, string, error) {
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	debug := safe(resp.Header.Get("PayPal-Debug-Id"))
	if e != nil || len(b) > 1<<20 {
		return nil, debug, &Error{Code: "PROVIDER_UNREADABLE", Temporary: true, Unknown: true}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var d struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(b, &d)
		code := "PROVIDER_" + safe(d.Name)
		if d.Name == "" || safe(d.Name) == "" {
			code = "PROVIDER_HTTP_ERROR"
		}
		return nil, debug, &Error{Code: code, DebugID: debug, HTTP: resp.StatusCode, Temporary: resp.StatusCode == 429 || resp.StatusCode >= 500, Unknown: resp.StatusCode >= 500 || resp.StatusCode == 409 || resp.StatusCode == 422 || resp.StatusCode == 404}
	}
	return b, debug, nil
}
func (c *Client) access(ctx context.Context, refresh bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !refresh && c.token != "" && time.Now().Before(c.expires) {
		return c.token, nil
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", c.base+"/v1/oauth2/token", strings.NewReader("grant_type=client_credentials"))
	req.SetBasicAuth(c.clientID, c.secret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, e := c.http.Do(req)
	if e != nil {
		return "", &Error{Code: "PROVIDER_AUTH_TRANSPORT", Temporary: true}
	}
	b, _, e := response(resp)
	if e != nil {
		return "", e
	}
	var t struct {
		Token   string `json:"access_token"`
		Expires int    `json:"expires_in"`
	}
	if json.Unmarshal(b, &t) != nil || t.Token == "" || t.Expires < 1 {
		return "", &Error{Code: "PROVIDER_AUTH_INVALID"}
	}
	c.token = t.Token
	ttl := time.Duration(t.Expires) * time.Second
	if ttl > time.Minute {
		ttl -= 30 * time.Second
	}
	c.expires = time.Now().Add(ttl)
	return c.token, nil
}
func (c *Client) call(ctx context.Context, method, path, key string, body []byte) ([]byte, string, error) {
	token, e := c.access(ctx, false)
	if e != nil {
		return nil, "", e
	}
	for attempt := 0; attempt < 2; attempt++ {
		req, _ := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		if len(body) > 0 {
			req.Header.Set("Content-Type", "application/json")
		}
		if key != "" {
			req.Header.Set("PayPal-Request-Id", key)
		}
		if !strings.HasSuffix(path, "/void") {
			req.Header.Set("Prefer", "return=representation")
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, "", &Error{Code: "PROVIDER_TRANSPORT", Temporary: true, Unknown: method != "GET"}
		}
		if resp.StatusCode == 401 && attempt == 0 {
			resp.Body.Close()
			token, e = c.access(ctx, true)
			if e != nil {
				return nil, "", e
			}
			continue
		}
		return response(resp)
	}
	return nil, "", &Error{Code: "PROVIDER_AUTH_REJECTED"}
}

type money struct {
	Currency string `json:"currency_code"`
	Value    string `json:"value"`
}
type payment struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Amount money  `json:"amount"`
	Final  bool   `json:"final_capture"`
	Payee  struct {
		ID string `json:"merchant_id"`
	} `json:"payee"`
	Links []struct {
		Href   string `json:"href"`
		Rel    string `json:"rel"`
		Method string `json:"method"`
	} `json:"links"`
	Supplementary struct {
		Related struct {
			OrderID         string `json:"order_id"`
			AuthorizationID string `json:"authorization_id"`
		} `json:"related_ids"`
	} `json:"supplementary_data"`
}
type unit struct {
	Reference string `json:"reference_id"`
	Custom    string `json:"custom_id"`
	Amount    money  `json:"amount"`
	Payee     struct {
		ID string `json:"merchant_id"`
	} `json:"payee"`
	Payments struct {
		Authorizations []payment `json:"authorizations"`
		Captures       []payment `json:"captures"`
	} `json:"payments"`
}
type order struct {
	ID     string `json:"id"`
	Intent string `json:"intent"`
	Status string `json:"status"`
	Units  []unit `json:"purchase_units"`
}

func USD(n int64) string { return fmt.Sprintf("%d.%02d", n/100, n%100) }
func Payload(kind string, c domain.ProviderCommand) []byte {
	var v any
	switch kind {
	case "CREATE_ORDER":
		v = map[string]any{"intent": "AUTHORIZE", "purchase_units": []any{map[string]any{"reference_id": fmt.Sprintf("%s:v%d", c.IntentID, c.Version), "custom_id": c.Digest, "description": "Pause sandbox merchant purchase", "amount": money{c.Binding.Currency, USD(c.Binding.AmountMinor)}, "payee": map[string]string{"merchant_id": c.Binding.PayeeID}}}}
	case "AUTHORIZE":
		return []byte("{}")
	case "CAPTURE":
		v = map[string]any{"amount": money{c.Binding.Currency, USD(c.Binding.AmountMinor)}, "final_capture": true}
	case "VOID":
		return nil
	}
	b, _ := json.Marshal(v)
	return b
}
func valid(c domain.ProviderCommand) error {
	b, e := c.Binding.Canonical()
	if e != nil || b.IntentID != c.IntentID || b.Version != c.Version || c.Environment != Sandbox {
		return &Error{Code: "PROVIDER_BINDING_MISMATCH"}
	}
	d, _ := b.Digest()
	if d != c.Digest {
		return &Error{Code: "PROVIDER_BINDING_MISMATCH"}
	}
	return nil
}
func validateMoney(p payment, c domain.ProviderCommand) error {
	if safe(p.ID) == "" || p.Amount.Currency != c.Binding.Currency || p.Amount.Value != USD(c.Binding.AmountMinor) || (p.Payee.ID != "" && p.Payee.ID != c.Binding.PayeeID) || (p.Supplementary.Related.OrderID != "" && p.Supplementary.Related.OrderID != c.OrderID) || (p.Supplementary.Related.AuthorizationID != "" && c.AuthorizationID != "" && p.Supplementary.Related.AuthorizationID != c.AuthorizationID) {
		return &Error{Code: "PROVIDER_RESOURCE_MISMATCH", Unknown: true}
	}
	for _, link := range p.Links {
		if link.Rel != "self" && link.Rel != "up" {
			continue
		}
		u, err := url.Parse(link.Href)
		if err != nil || u.Scheme != "https" || (u.Host != "api-m.sandbox.paypal.com" && u.Host != "api.sandbox.paypal.com") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || link.Method != "GET" {
			return &Error{Code: "PROVIDER_RESOURCE_LINK_MISMATCH", Unknown: true}
		}
		if link.Rel == "self" {
			if u.EscapedPath() != "/v2/payments/authorizations/"+url.PathEscape(p.ID) && u.EscapedPath() != "/v2/payments/captures/"+url.PathEscape(p.ID) {
				return &Error{Code: "PROVIDER_RESOURCE_LINK_MISMATCH", Unknown: true}
			}
		} else if !paymentUpLink(link.Href, c) {
			return &Error{Code: "PROVIDER_RESOURCE_LINK_MISMATCH", Unknown: true}
		}
	}
	return nil
}
func paymentUpLink(href string, c domain.ProviderCommand) bool {
	u, err := url.Parse(href)
	if err != nil {
		return false
	}
	return (c.OrderID != "" && u.EscapedPath() == "/v2/checkout/orders/"+url.PathEscape(c.OrderID)) || (c.AuthorizationID != "" && u.EscapedPath() == "/v2/payments/authorizations/"+url.PathEscape(c.AuthorizationID))
}
func normalize(o order, c domain.ProviderCommand, debug string) (domain.ProviderResult, error) {
	r := domain.ProviderResult{OrderID: o.ID, OrderStatus: o.Status, AmountMinor: c.Binding.AmountMinor, Currency: c.Binding.Currency, PayeeID: c.Binding.PayeeID, Environment: Sandbox, DebugID: debug, Status: "AWAITING_PAYER", Confirmed: true}
	if valid(c) != nil || safe(o.ID) == "" || (c.OrderID != "" && o.ID != c.OrderID) || o.Intent != "AUTHORIZE" || len(o.Units) != 1 {
		return r, &Error{Code: "PROVIDER_ORDER_MISMATCH", Unknown: true}
	}
	u := o.Units[0]
	if u.Reference != fmt.Sprintf("%s:v%d", c.IntentID, c.Version) || u.Custom != c.Digest || u.Amount.Currency != c.Binding.Currency || u.Amount.Value != USD(c.Binding.AmountMinor) || u.Payee.ID != c.Binding.PayeeID {
		return r, &Error{Code: "PROVIDER_ORDER_MISMATCH", Unknown: true}
	}
	if len(u.Payments.Authorizations) > 1 || len(u.Payments.Captures) > 1 {
		return r, &Error{Code: "PROVIDER_MULTIPLE_PAYMENTS", Unknown: true}
	}
	c.OrderID = o.ID
	for _, a := range u.Payments.Authorizations {
		if e := validateMoney(a, c); e != nil {
			return r, e
		}
		if c.AuthorizationID != "" && c.AuthorizationID != a.ID {
			return r, &Error{Code: "PROVIDER_AUTHORIZATION_MISMATCH", Unknown: true}
		}
		r.AuthorizationID = a.ID
		c.AuthorizationID = a.ID
		r.AuthorizationStatus = a.Status
		switch a.Status {
		case "CREATED":
			r.Status = "HELD"
		case "VOIDED":
			r.Status = "VOIDED"
		case "DENIED", "EXPIRED":
			r.Status = "CLOSED_UNCAPTURED"
		default:
			r.Status = "RECONCILING"
		}
	}
	for _, p := range u.Payments.Captures {
		if e := validateMoney(p, c); e != nil {
			return r, e
		}
		if c.CaptureID != "" && c.CaptureID != p.ID {
			return r, &Error{Code: "PROVIDER_CAPTURE_MISMATCH", Unknown: true}
		}
		r.CaptureID = p.ID
		r.CaptureStatus = p.Status
		switch p.Status {
		case "COMPLETED":
			r.Status = "CAPTURED"
		case "PENDING":
			r.Status = "CAPTURE_PENDING"
		case "DECLINED", "FAILED", "DENIED":
			if r.AuthorizationStatus == "CREATED" {
				r.Status = "HELD"
			} else {
				r.Status = "RECONCILING"
			}
		default:
			r.Status = "RECONCILING"
		}
	}
	return r, nil
}
func (c *Client) get(ctx context.Context, path string, out any) (string, error) {
	b, d, e := c.call(ctx, "GET", path, "", nil)
	if e != nil {
		return d, e
	}
	if json.Unmarshal(b, out) != nil {
		return d, &Error{Code: "PROVIDER_INVALID_JSON", Temporary: true, Unknown: true}
	}
	return d, nil
}
func (c *Client) CreateOrder(ctx context.Context, in domain.ProviderCommand) (domain.ProviderResult, error) {
	if e := valid(in); e != nil {
		return domain.ProviderResult{}, e
	}
	b, d, e := c.call(ctx, "POST", "/v2/checkout/orders", in.RequestID, Payload("CREATE_ORDER", in))
	if e != nil {
		return domain.ProviderResult{}, e
	}
	var o order
	if json.Unmarshal(b, &o) != nil {
		return domain.ProviderResult{}, &Error{Code: "PROVIDER_INVALID_JSON", Temporary: true, Unknown: true}
	}
	return normalize(o, in, d)
}
func (c *Client) Authorize(ctx context.Context, in domain.ProviderCommand) (domain.ProviderResult, error) {
	if valid(in) != nil || safe(in.OrderID) == "" {
		return domain.ProviderResult{}, &Error{Code: "PROVIDER_BINDING_MISMATCH"}
	}
	b, d, e := c.call(ctx, "POST", "/v2/checkout/orders/"+url.PathEscape(in.OrderID)+"/authorize", in.RequestID, Payload("AUTHORIZE", in))
	if e != nil {
		return domain.ProviderResult{}, e
	}
	var o order
	if json.Unmarshal(b, &o) != nil {
		return domain.ProviderResult{}, &Error{Code: "PROVIDER_INVALID_JSON", Temporary: true, Unknown: true}
	}
	r, e := normalize(o, in, d)
	if e != nil {
		return r, e
	}
	in.AuthorizationID = r.AuthorizationID
	return c.Reconcile(ctx, in)
}
func (c *Client) Capture(ctx context.Context, in domain.ProviderCommand) (domain.ProviderResult, error) {
	if valid(in) != nil || safe(in.AuthorizationID) == "" {
		return domain.ProviderResult{}, &Error{Code: "PROVIDER_BINDING_MISMATCH"}
	}
	b, d, e := c.call(ctx, "POST", "/v2/payments/authorizations/"+url.PathEscape(in.AuthorizationID)+"/capture", in.RequestID, Payload("CAPTURE", in))
	if e != nil {
		return domain.ProviderResult{}, e
	}
	var p payment
	if json.Unmarshal(b, &p) != nil {
		return domain.ProviderResult{}, &Error{Code: "PROVIDER_INVALID_JSON", Temporary: true, Unknown: true}
	}
	if e = validateMoney(p, in); e != nil {
		return domain.ProviderResult{}, e
	}
	in.CaptureID = p.ID
	r, e := c.Reconcile(ctx, in)
	if r.DebugID == "" {
		r.DebugID = d
	}
	return r, e
}
func (c *Client) Void(ctx context.Context, in domain.ProviderCommand) (domain.ProviderResult, error) {
	if valid(in) != nil || safe(in.AuthorizationID) == "" {
		return domain.ProviderResult{}, &Error{Code: "PROVIDER_BINDING_MISMATCH"}
	}
	b, _, e := c.call(ctx, "POST", "/v2/payments/authorizations/"+url.PathEscape(in.AuthorizationID)+"/void", in.RequestID, nil)
	if e != nil {
		return domain.ProviderResult{}, e
	}
	if len(bytes.TrimSpace(b)) > 0 {
		var p payment
		if json.Unmarshal(b, &p) != nil {
			return domain.ProviderResult{}, &Error{Code: "PROVIDER_INVALID_JSON", Unknown: true}
		}
		if e = validateMoney(p, in); e != nil {
			return domain.ProviderResult{}, e
		}
	}
	return c.Reconcile(ctx, in)
}
func (c *Client) Reconcile(ctx context.Context, in domain.ProviderCommand) (domain.ProviderResult, error) {
	if e := valid(in); e != nil {
		return domain.ProviderResult{}, e
	}
	if safe(in.OrderID) == "" {
		return domain.ProviderResult{}, &Error{Code: "PROVIDER_ORDER_UNKNOWN", Temporary: true, Unknown: true}
	}
	var o order
	d, e := c.get(ctx, "/v2/checkout/orders/"+url.PathEscape(in.OrderID), &o)
	if e != nil {
		return domain.ProviderResult{}, e
	}
	r, e := normalize(o, in, d)
	if e != nil {
		return r, e
	}
	if r.AuthorizationID != "" {
		in.AuthorizationID = r.AuthorizationID
		var a payment
		_, e = c.get(ctx, "/v2/payments/authorizations/"+url.PathEscape(r.AuthorizationID), &a)
		if e != nil {
			return r, e
		}
		if e = validateMoney(a, in); e != nil || a.ID != r.AuthorizationID {
			if e == nil {
				e = &Error{Code: "PROVIDER_AUTHORIZATION_MISMATCH", Unknown: true}
			}
			return r, e
		}
		o.Units[0].Payments.Authorizations = []payment{a}
	}
	if in.CaptureID != "" {
		var p payment
		_, e = c.get(ctx, "/v2/payments/captures/"+url.PathEscape(in.CaptureID), &p)
		if e != nil {
			return r, e
		}
		if e = validateMoney(p, in); e != nil || p.ID != in.CaptureID {
			if e == nil {
				e = &Error{Code: "PROVIDER_CAPTURE_MISMATCH", Unknown: true}
			}
			return r, e
		}
		linked := p.Supplementary.Related.OrderID == in.OrderID || (in.AuthorizationID != "" && p.Supplementary.Related.AuthorizationID == in.AuthorizationID)
		for _, link := range p.Links {
			if link.Rel == "up" && paymentUpLink(link.Href, in) {
				linked = true
			}
		}
		for _, known := range o.Units[0].Payments.Captures {
			if known.ID == p.ID {
				linked = true
			}
		}
		if !linked {
			return r, &Error{Code: "PROVIDER_CAPTURE_LINK_MISMATCH", Unknown: true}
		}
		o.Units[0].Payments.Captures = []payment{p}
	}
	return normalize(o, in, d)
}
func (c *Client) VerifyWebhook(ctx context.Context, h http.Header, event json.RawMessage) error {
	if c.webhookID == "" || c.webhookID == "WEBHOOK_ID" {
		return &Error{Code: "WEBHOOK_NOT_CONFIGURED", Temporary: true}
	}
	body := map[string]any{"webhook_id": c.webhookID, "webhook_event": event}
	for key, header := range map[string]string{"auth_algo": "PAYPAL-AUTH-ALGO", "cert_url": "PAYPAL-CERT-URL", "transmission_id": "PAYPAL-TRANSMISSION-ID", "transmission_sig": "PAYPAL-TRANSMISSION-SIG", "transmission_time": "PAYPAL-TRANSMISSION-TIME"} {
		v := h.Get(header)
		if strings.TrimSpace(v) == "" || len(v) > 8192 {
			return &Error{Code: "WEBHOOK_SIGNATURE_INVALID"}
		}
		body[key] = v
	}
	b, _ := json.Marshal(body)
	b, _, e := c.call(ctx, "POST", "/v1/notifications/verify-webhook-signature", "", b)
	if e != nil {
		return e
	}
	var result struct {
		Status string `json:"verification_status"`
	}
	if json.Unmarshal(b, &result) != nil {
		return &Error{Code: "WEBHOOK_VERIFIER_INVALID", Temporary: true}
	}
	if result.Status != "SUCCESS" {
		return &Error{Code: "WEBHOOK_SIGNATURE_INVALID"}
	}
	return nil
}
