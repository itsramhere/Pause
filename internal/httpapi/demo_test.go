package httpapi_test

import (
	"encoding/json"
	"github.com/ramst/pause/internal/app"
	"testing"
	"time"
)

func TestP05DemoHTTPBoundariesAndExpiry(t *testing.T) {
	f := newFixture(t)
	c := f.newClient("127.0.0.120")
	status(t, c.request("POST", "/api/demo/runs", map[string]string{"scenario": "positive"}, nil), 404)
	f.svc.Config.DemoEnabled = true
	status(t, c.request("POST", "/api/demo/runs", map[string]string{"scenario": "positive"}, map[string]string{"Origin": "https://attacker.example"}), 403)
	out := c.request("POST", "/api/demo/runs", map[string]string{"scenario": "positive"}, nil)
	status(t, out, 201)
	var r app.DemoRun
	if e := json.Unmarshal(out.Body.Bytes(), &r); e != nil {
		t.Fatal(e)
	}
	account := r.Accounts[0]
	status(t, c.request("POST", "/api/auth/login", map[string]string{"email": account.Email, "password": account.Password}, nil), 200)
	status(t, c.request("GET", "/api/session", nil, nil), 200)
	status(t, c.request("POST", "/api/intents", map[string]string{}, map[string]string{"X-CSRF-Token": "forged"}), 403)
	f.clock.now = f.clock.now.Add(3 * time.Hour)
	status(t, c.request("GET", "/api/session", nil, nil), 401)
	status(t, c.request("POST", "/api/auth/login", map[string]string{"email": account.Email, "password": account.Password}, nil), 401)
	status(t, c.request("POST", "/api/demo/runs/"+r.ID+"/reset", map[string]string{"reset_token": r.ResetToken}, nil), 200)
}
