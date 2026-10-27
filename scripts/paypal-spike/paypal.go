package main

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
	"time"
)

type money struct {
	Currency string `json:"currency_code"`
	Value    string `json:"value"`
}
type payment struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	Amount        money  `json:"amount"`
	FinalCapture  bool   `json:"final_capture"`
	Supplementary struct {
		Related struct {
			AuthorizationID string `json:"authorization_id"`
			OrderID         string `json:"order_id"`
		} `json:"related_ids"`
	} `json:"supplementary_data"`
}
type purchaseUnit struct {
	ReferenceID string `json:"reference_id"`
	CustomID    string `json:"custom_id"`
	Amount      money  `json:"amount"`
	Payee       struct {
		MerchantID string `json:"merchant_id"`
	} `json:"payee"`
	Payments struct {
		Authorizations []payment `json:"authorizations"`
		Captures       []payment `json:"captures"`
	} `json:"payments"`
}
type order struct {
	ID     string         `json:"id"`
	Intent string         `json:"intent"`
	Status string         `json:"status"`
	Units  []purchaseUnit `json:"purchase_units"`
}
type providerResponse struct {
	Status  int
	DebugID string
	Body    []byte
}
type providerError struct {
	Status        int
	Name, DebugID string
}

func (e *providerError) Error() string {
	return fmt.Sprintf("PayPal HTTP %d (%s); debug ID %s", e.Status, e.Name, e.DebugID)
}

var safeCode = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,100}$`)

func code(s string) string {
	if safeCode.MatchString(s) {
		return s
	}
	return "unavailable"
}

type paypalClient struct {
	config      config
	http        *http.Client
	token       string
	tokenExpiry time.Time
}

func newPayPal(c config) *paypalClient {
	return &paypalClient{config: c, http: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
}

func readResponse(resp *http.Response) (providerResponse, error) {
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if err != nil || len(b) > 1<<20 {
		return providerResponse{}, errors.New("provider response unreadable; outcome unknown")
	}
	r := providerResponse{Status: resp.StatusCode, DebugID: code(resp.Header.Get("PayPal-Debug-Id")), Body: b}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var detail struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(b, &detail)
		return r, &providerError{Status: r.Status, Name: code(detail.Name), DebugID: r.DebugID}
	}
	return r, nil
}

func (p *paypalClient) accessToken(ctx context.Context) error {
	if p.token != "" && time.Now().Before(p.tokenExpiry) {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.config.PayPalURL+"/v1/oauth2/token", strings.NewReader("grant_type=client_credentials"))
	if err != nil {
		return errors.New("cannot construct PayPal authentication request")
	}
	req.SetBasicAuth(p.config.ClientID, p.config.ClientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.http.Do(req)
	if err != nil {
		return errors.New("PayPal authentication transport failed")
	}
	r, err := readResponse(resp)
	if err != nil {
		return err
	}
	var token struct {
		AccessToken string `json:"access_token"`
		Expires     int    `json:"expires_in"`
	}
	if json.Unmarshal(r.Body, &token) != nil || token.AccessToken == "" || token.Expires <= 0 {
		return errors.New("PayPal authentication response invalid")
	}
	p.token = token.AccessToken
	p.tokenExpiry = time.Now().Add(time.Duration(token.Expires-30) * time.Second)
	return nil
}

func (p *paypalClient) call(ctx context.Context, method, path, key string, body []byte) (providerResponse, error) {
	if err := p.accessToken(ctx); err != nil {
		return providerResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, method, p.config.PayPalURL+path, bytes.NewReader(body))
	if err != nil {
		return providerResponse{}, errors.New("cannot construct PayPal request")
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("PayPal-Request-Id", key)
	}
	// Void retains the normal empty 204 response. Reads and other mutations use full representation.
	if !strings.HasSuffix(path, "/void") {
		req.Header.Set("Prefer", "return=representation")
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return providerResponse{}, errors.New("PayPal transport failed; mutation outcome may be unknown")
	}
	return readResponse(resp)
}

func (p *paypalClient) getOrder(ctx context.Context, id string) (order, providerResponse, error) {
	r, err := p.call(ctx, "GET", "/v2/checkout/orders/"+url.PathEscape(id), "", nil)
	if err != nil {
		return order{}, r, err
	}
	var o order
	if r.Status != 200 || json.Unmarshal(r.Body, &o) != nil {
		return o, r, errors.New("PayPal order response invalid")
	}
	return o, r, nil
}
func (p *paypalClient) getAuthorization(ctx context.Context, id string) (payment, providerResponse, error) {
	r, err := p.call(ctx, "GET", "/v2/payments/authorizations/"+url.PathEscape(id), "", nil)
	if err != nil {
		return payment{}, r, err
	}
	var a payment
	if r.Status != 200 || json.Unmarshal(r.Body, &a) != nil {
		return a, r, errors.New("PayPal authorization response invalid")
	}
	return a, r, nil
}

func validateOrder(r *run, o order) error {
	if o.ID == "" || !providerID.MatchString(o.ID) || (r.OrderID != "" && r.OrderID != o.ID) || o.Intent != "AUTHORIZE" || len(o.Units) != 1 {
		return errors.New("provider order ID/intent/purchase-unit mismatch")
	}
	u := o.Units[0]
	if u.ReferenceID != r.ID || u.CustomID != r.Digest || u.Payee.MerchantID != r.MerchantID || u.Amount.Currency != r.Currency || u.Amount.Value != usd(r.Scenario.AmountMinor) {
		return errors.New("provider order amount/currency/merchant/binding mismatch")
	}
	return nil
}
func validatePayment(r *run, p payment) error {
	if !providerID.MatchString(p.ID) || p.Amount.Currency != r.Currency || p.Amount.Value != usd(r.Scenario.AmountMinor) {
		return errors.New("provider payment ID/amount/currency mismatch")
	}
	return nil
}

func createBody(r *run) []byte {
	b, _ := json.Marshal(map[string]any{"intent": "AUTHORIZE", "purchase_units": []any{map[string]any{
		"reference_id": r.ID, "custom_id": r.Digest, "description": "Pause synthetic sandbox merchant purchase",
		"amount": money{r.Currency, usd(r.Scenario.AmountMinor)}, "payee": map[string]string{"merchant_id": r.MerchantID},
	}}})
	return b
}
func captureBody(r *run) []byte {
	b, _ := json.Marshal(map[string]any{"amount": money{r.Currency, usd(r.Scenario.AmountMinor)}, "final_capture": true})
	return b
}
