package httpapi_test

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/ramst/pause/internal/app"
	"github.com/ramst/pause/internal/auth"
	"github.com/ramst/pause/internal/httpapi"
	"strings"
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

func TestP07DemoSwitchBoundaries(t *testing.T) {
	f := newFixture(t)
	c := f.newClient("127.0.0.121")
	path := "/api/demo/runs/" + uuid.NewString() + "/session"
	body := map[string]string{"reset_token": strings.Repeat("x", 43), "role": "senior"}
	check := func(path string, body map[string]string, want int, wantCode string) {
		t.Helper()
		out := c.request("POST", path, body, nil)
		status(t, out, want)
		code(t, out, wantCode)
	}
	check(path, body, 404, "DEMO_DISABLED")
	f.svc.Config.DemoEnabled = true
	f.svc.Config.Environment = "production"
	check(path, body, 404, "DEMO_DISABLED")
	f.svc.Config.Environment = "test"
	launch := func(scenario string) app.DemoRun {
		t.Helper()
		out := c.request("POST", "/api/demo/runs", map[string]string{"scenario": scenario}, nil)
		status(t, out, 201)
		var run app.DemoRun
		if e := json.Unmarshal(out.Body.Bytes(), &run); e != nil {
			t.Fatal(e)
		}
		return run
	}
	a, b := launch("stopped"), launch("positive")
	path = "/api/demo/runs/" + a.ID + "/session"
	body["reset_token"] = a.ResetToken
	check("/api/demo/runs/not-a-uuid/session", body, 404, "NOT_FOUND")
	check("/api/demo/runs/"+uuid.NewString()+"/session", body, 404, "NOT_FOUND")
	for _, token := range []string{"", "wrong", strings.Repeat("x", 43), b.ResetToken} {
		check(path, map[string]string{"reset_token": token, "role": "senior"}, 404, "NOT_FOUND")
	}
	for _, role := range []string{"", "admin", "Senior"} {
		check(path, map[string]string{"reset_token": a.ResetToken, "role": role}, 400, "DEMO_ROLE_INVALID")
	}
	status(t, c.request("POST", path, body, map[string]string{"Origin": "https://attacker.example"}), 403)
	status(t, c.request("POST", path, body, map[string]string{"Sec-Fetch-Site": "cross-site"}), 403)
	ctx := context.Background()
	for _, state := range []string{"expired", "closed", "resetting"} {
		t.Run(state, func(t *testing.T) {
			var e error
			switch state {
			case "expired":
				_, e = f.db.Pool.Exec(ctx, `UPDATE demo_runs SET expires_at=$2 WHERE id=$1`, a.ID, f.clock.now)
			case "closed":
				_, e = f.db.Pool.Exec(ctx, `UPDATE demo_runs SET closed_at=$2 WHERE id=$1`, a.ID, f.clock.now)
			case "resetting":
				_, e = f.db.Pool.Exec(ctx, `UPDATE demo_runs SET resetting=true WHERE id=$1`, a.ID)
			}
			if e != nil {
				t.Fatal(e)
			}
			check(path, body, 410, "DEMO_RUN_ENDED")
			check(path, map[string]string{"reset_token": b.ResetToken, "role": "senior"}, 404, "NOT_FOUND")
			if _, e = f.db.Pool.Exec(ctx, `UPDATE demo_runs SET expires_at=$2,closed_at=NULL,resetting=false WHERE id=$1`, a.ID, a.ExpiresAt); e != nil {
				t.Fatal(e)
			}
		})
	}
	// An ordinary account/session can be replaced only by this run's account.
	c.login(t, "senior1@example.test")
	var normal auth.LoginResult
	if e := json.Unmarshal(c.request("GET", "/api/session", nil, nil).Body.Bytes(), &normal); e != nil {
		t.Fatal(e)
	}
	check("/api/demo/runs/"+normal.Actor.HouseholdID+"/session", body, 404, "NOT_FOUND")
	for _, role := range []string{"senior", "subject", "reviewer"} {
		old := c.cookies[httpapi.SessionCookie]
		oldCSRF := c.cookies[httpapi.CSRFCookie]
		body["role"] = role
		out := c.request("POST", path, body, nil)
		status(t, out, 200)
		var result auth.LoginResult
		if e := json.Unmarshal(out.Body.Bytes(), &result); e != nil {
			t.Fatal(e)
		}
		var household, user string
		if e := f.db.Pool.QueryRow(ctx, `SELECT d.household_id::text,m.user_id::text FROM demo_runs d JOIN memberships m ON m.household_id=d.household_id WHERE d.id=$1 AND m.role=$2`, a.ID, role).Scan(&household, &user); e != nil {
			t.Fatal(e)
		}
		if string(result.Actor.Role) != role || result.Actor.HouseholdID != household || result.Actor.UserID != user || household == normal.Actor.HouseholdID || result.Actor.Name == "" {
			t.Fatal("wrong actor", result.Actor)
		}
		cookies := out.Result().Cookies()
		if len(cookies) != 2 || c.cookies[httpapi.SessionCookie].Value == old.Value || c.cookies[httpapi.CSRFCookie].Value == oldCSRF.Value || c.cookies[httpapi.CSRFCookie].Value != result.CSRFToken {
			t.Fatal("cookies not rotated")
		}
		if !c.cookies[httpapi.SessionCookie].HttpOnly || c.cookies[httpapi.CSRFCookie].HttpOnly {
			t.Fatal("cookie visibility")
		}
		stale := f.newClient("127.0.0.122")
		stale.cookies[httpapi.SessionCookie] = old
		status(t, stale.request("GET", "/api/session", nil, nil), 401)
		status(t, c.request("GET", "/api/session", nil, nil), 200)
		status(t, c.request("POST", "/api/auth/logout", map[string]string{}, map[string]string{"X-CSRF-Token": "forged"}), 403)
		if strings.Contains(out.Body.String(), a.ResetToken) || strings.Contains(out.Body.String(), c.cookies[httpapi.SessionCookie].Value) {
			t.Fatal("capability/session token echoed")
		}
	}
	var audits int
	if e := f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action='DEMO_ROLE_SWITCH' AND reason_code='DEMO_RUN_TOKEN'`).Scan(&audits); e != nil || audits != 3 {
		t.Fatal("switch audits", audits, e)
	}
	for n := 3; n < 60; n++ {
		status(t, c.request("POST", path, body, nil), 200)
	}
	check(path, body, 429, "DEMO_THROTTLED")
	check("/api/demo/runs/"+strings.ToUpper(a.ID)+"/session", body, 429, "DEMO_THROTTLED")
	status(t, c.request("GET", "/api/session", nil, nil), 200)
	f.clock.now = f.clock.now.Add(time.Hour)
	status(t, c.request("POST", path, body, nil), 200)
	if strings.Contains(f.logs.String(), a.ResetToken) || strings.Contains(f.logs.String(), b.ResetToken) {
		t.Fatal("capability logged")
	}
}
