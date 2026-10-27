package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

func (p *paypalClient) verifyWebhook(ctx context.Context, h http.Header, event json.RawMessage) error {
	if p.config.WebhookID == "" || p.config.WebhookID == "WEBHOOK_ID" {
		return errors.New("real PAYPAL_WEBHOOK_ID required; simulator events are unsupported by postback verification")
	}
	fields := map[string]string{"auth_algo": "PAYPAL-AUTH-ALGO", "cert_url": "PAYPAL-CERT-URL", "transmission_id": "PAYPAL-TRANSMISSION-ID", "transmission_sig": "PAYPAL-TRANSMISSION-SIG", "transmission_time": "PAYPAL-TRANSMISSION-TIME"}
	body := map[string]any{"webhook_id": p.config.WebhookID, "webhook_event": event}
	for k, v := range fields {
		value := h.Get(v)
		if strings.TrimSpace(value) == "" || len(value) > 8192 {
			return errors.New("required PayPal signature headers are missing or invalid")
		}
		body[k] = value
	}
	if !json.Valid(event) {
		return errors.New("invalid webhook event JSON")
	}
	b, _ := json.Marshal(body)
	resp, err := p.call(ctx, "POST", "/v1/notifications/verify-webhook-signature", "", b)
	if err != nil {
		return err
	}
	var result struct {
		Status string `json:"verification_status"`
	}
	if resp.Status != 200 || json.Unmarshal(resp.Body, &result) != nil || result.Status != "SUCCESS" {
		return errors.New("PayPal signature verification did not succeed")
	}
	return nil
}
