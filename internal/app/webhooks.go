package app

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/ramst/pause/internal/domain"
	"github.com/ramst/pause/internal/paypal"
)

type WebhookVerifier interface {
	VerifyWebhook(context.Context, http.Header, json.RawMessage) error
}

var WebhookTypes = map[string]bool{"PAYMENT.AUTHORIZATION.CREATED": true, "PAYMENT.AUTHORIZATION.VOIDED": true, "PAYMENT.CAPTURE.COMPLETED": true, "PAYMENT.CAPTURE.PENDING": true, "PAYMENT.CAPTURE.DENIED": true, "PAYMENT.CAPTURE.DECLINED": true, "PAYMENT.CAPTURE.REVERSED": true, "CHECKOUT.ORDER.APPROVED": true}

func (s *Service) IngestWebhook(ctx context.Context, h http.Header, raw []byte) error {
	verifier, ok := s.Provider.(WebhookVerifier)
	if !ok {
		return domain.Fail("WEBHOOK_UNAVAILABLE", 503)
	}
	if len(raw) > 256*1024 || !json.Valid(raw) {
		return domain.Fail("WEBHOOK_INVALID", 400)
	}
	if e := verifier.VerifyWebhook(ctx, h, raw); e != nil {
		_, temporary, unknown := paypal.Failure(e)
		if temporary || unknown {
			return domain.Fail("WEBHOOK_VERIFICATION_UNAVAILABLE", 503)
		}
		return domain.Fail("WEBHOOK_SIGNATURE_INVALID", 400)
	}
	var event struct {
		ID       string `json:"id"`
		Type     string `json:"event_type"`
		Resource struct {
			ID            string `json:"id"`
			Supplementary struct {
				Related struct {
					OrderID         string `json:"order_id"`
					AuthorizationID string `json:"authorization_id"`
					CaptureID       string `json:"capture_id"`
				} `json:"related_ids"`
			} `json:"supplementary_data"`
		} `json:"resource"`
	}
	if json.Unmarshal(raw, &event) != nil || len(event.ID) < 1 || len(event.ID) > 128 || len(event.Type) > 128 || len(event.Resource.ID) < 1 || len(event.Resource.ID) > 128 {
		return domain.Fail("WEBHOOK_INVALID", 400)
	}
	order, auth, capture := event.Resource.Supplementary.Related.OrderID, event.Resource.Supplementary.Related.AuthorizationID, event.Resource.Supplementary.Related.CaptureID
	switch event.Type {
	case "CHECKOUT.ORDER.APPROVED":
		order = event.Resource.ID
	case "PAYMENT.AUTHORIZATION.CREATED", "PAYMENT.AUTHORIZATION.VOIDED":
		auth = event.Resource.ID
	default:
		if WebhookTypes[event.Type] {
			capture = event.Resource.ID
		}
	}
	return s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		result := "WAITING_FOR_MAPPING"
		var processed any
		if !WebhookTypes[event.Type] {
			result = "IGNORED_UNSUPPORTED_TYPE"
			processed = s.Clock.Now()
		}
		_, e := tx.Exec(ctx, `INSERT INTO paypal_events(id,event_type,environment,resource_id,order_id,authorization_id,capture_id,verified_at,raw_body,retain_until,result_code,processed_at) VALUES($1,$2,'sandbox',$3,NULLIF($4,''),NULLIF($5,''),NULLIF($6,''),$7,$8,$9,$10,$11) ON CONFLICT(id) DO NOTHING`, event.ID, event.Type, event.Resource.ID, order, auth, capture, s.Clock.Now(), raw, s.Clock.Now().Add(24*time.Hour), result, processed)
		if e != nil {
			return e
		}
		return s.mapWebhookEventsTx(ctx, tx)
	})
}
func (s *Service) mapWebhookEvents(ctx context.Context) error {
	return s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		return s.mapWebhookEventsTx(ctx, tx)
	})
}
func (s *Service) mapWebhookEventsTx(ctx context.Context, tx pgx.Tx) error {
	rows, e := tx.Query(ctx, `SELECT e.id,i.id::text FROM paypal_events e JOIN payment_intents i ON (e.order_id=i.provider_order_id OR e.authorization_id=i.authorization_id OR e.capture_id=i.capture_id) WHERE e.processed_at IS NULL AND e.mapped_intent IS NULL ORDER BY e.verified_at LIMIT 100 FOR UPDATE OF e SKIP LOCKED`)
	if e != nil {
		return e
	}
	type link struct{ event, intent string }
	links := []link{}
	for rows.Next() {
		var l link
		if e = rows.Scan(&l.event, &l.intent); e != nil {
			rows.Close()
			return e
		}
		links = append(links, l)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, l := range links {
		if _, e = tx.Exec(ctx, `UPDATE paypal_events SET mapped_intent=$2,result_code='RECONCILIATION_QUEUED' WHERE id=$1`, l.event, l.intent); e != nil {
			return e
		}
		if e = s.queue(ctx, tx, "PAYMENT_RECONCILE", l.intent, "reconcile:"+l.intent); e != nil {
			return e
		}
	}
	return nil
}
