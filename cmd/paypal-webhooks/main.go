// Explicit sandbox operator tooling. Does not print credentials or event bodies.
package main

import (
	"encoding/json"
	"flag"
	"github.com/ramst/pause/internal/bootstrap"
	"github.com/ramst/pause/internal/paypal"
	"os"
	"sort"
	"time"
)

func main() {
	mode := flag.String("mode", "inspect", "inspect, register, resend or delete")
	address := flag.String("url", "", "HTTPS webhook-only listener")
	event := flag.String("event", "", "actual app event ID")
	webhook := flag.String("webhook", "", "app webhook ID")
	flag.Parse()
	ctx, stop := bootstrap.Context()
	defer stop()
	a, e := bootstrap.Load(ctx)
	if e != nil {
		fail("CONFIGURATION_UNAVAILABLE")
	}
	defer a.DB.Pool.Close()
	p, ok := a.Provider.(*paypal.Client)
	if !ok {
		fail("PAYPAL_UNAVAILABLE")
	}
	var result any
	switch *mode {
	case "inspect":
		types, err := p.EventTypes(ctx)
		if err != nil {
			fail(err.Error())
		}
		sort.Strings(types)
		events, err := p.Events(ctx, time.Now().Add(-24*time.Hour), time.Now())
		if err != nil {
			fail(err.Error())
		}
		result = map[string]any{"environment": "sandbox", "available_event_types": types, "actual_app_events_last_24h": events}
	case "register":
		id, err := p.RegisterWebhook(ctx, *address, []string{"PAYMENT.AUTHORIZATION.CREATED", "PAYMENT.AUTHORIZATION.VOIDED", "PAYMENT.CAPTURE.COMPLETED", "PAYMENT.CAPTURE.PENDING", "PAYMENT.CAPTURE.DECLINED", "PAYMENT.CAPTURE.REVERSED", "CHECKOUT.ORDER.APPROVED"})
		if err != nil {
			fail(err.Error())
		}
		result = map[string]string{"webhook_id": id, "environment": "sandbox"}
	case "resend":
		if e = p.ResendEvent(ctx, *event, *webhook); e != nil {
			fail(e.Error())
		}
		result = map[string]string{"status": "RESEND_ACCEPTED", "event_id": *event}
	case "delete":
		if e = p.DeleteWebhook(ctx, *webhook); e != nil {
			fail(e.Error())
		}
		result = map[string]string{"status": "DELETED", "webhook_id": *webhook}
	default:
		fail("INVALID_MODE")
	}
	_ = json.NewEncoder(os.Stdout).Encode(result)
}
func fail(code string) {
	_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"error_code": code})
	os.Exit(1)
}
