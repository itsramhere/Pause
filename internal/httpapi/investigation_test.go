package httpapi_test

import (
	"github.com/ramst/pause/internal/app"
	"testing"
)

func TestP03RoutesRequireOwnSessionCSRFAndRole(t *testing.T) {
	f := newFixture(t)
	senior := f.newClient("127.0.0.90")
	senior.login(t, "senior1@example.test")
	i := readIntent(t, senior.request("POST", "/api/intents", intake("1.00", false), map[string]string{"Idempotency-Key": "p03-route-check"}))
	for _, path := range []string{"analyze", "request-verification", "review"} {
		out := senior.request("POST", "/api/intents/"+i.ID+"/"+path, map[string]int{"expected_version": 1}, map[string]string{"X-CSRF-Token": "forged"})
		status(t, out, 403)
		out = senior.request("POST", "/api/intents/"+i.ID+"/"+path, map[string]int{"expected_version": 1}, map[string]string{"Origin": "https://attacker.example"})
		status(t, out, 403)
	}
	status(t, senior.request("POST", "/api/intents/"+i.ID+"/review", app.ReviewDecision{}, nil), 403)
	subject := f.newClient("127.0.0.91")
	subject.login(t, "subject1@example.test")
	status(t, subject.request("GET", "/api/intents/"+i.ID+"/evidence", nil, nil), 403)
	status(t, senior.request("POST", "/api/verifications/"+i.ID+"/respond", app.Answer{}, nil), 403)
}
