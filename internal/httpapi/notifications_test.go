package httpapi_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ramst/pause/internal/app"
)

func TestC6DemoEmailHTTP(t *testing.T) {
	f := newFixture(t)
	f.svc.Config.DemoEnabled = true
	c := f.newClient("127.0.0.122")
	for _, email := range []string{"invalid", "Name <private@judge-mail.dev>", "<private@judge-mail.dev>", "one@judge-mail.dev,two@judge-mail.dev", strings.Repeat("a", 255) + "@judge-mail.dev", "one@judge-mail.dev\r\nBcc: two@judge-mail.dev"} {
		out := c.request("POST", "/api/demo/runs", map[string]string{"scenario": "stopped", "notify_email": email}, nil)
		status(t, out, 422)
		code(t, out, "DEMO_EMAIL_INVALID")
		if strings.Contains(out.Body.String(), email) {
			t.Fatal("invalid email echoed")
		}
	}
	email := "private@judge-mail.dev"
	out := c.request("POST", "/api/demo/runs", map[string]string{"scenario": "stopped", "notify_email": " " + email + " "}, nil)
	status(t, out, 201)
	if strings.Contains(out.Body.String(), email) || strings.Contains(out.Body.String(), "notify_email") || strings.Contains(f.logs.String(), email) {
		t.Fatal("launch leaked email")
	}
	var run app.DemoRun
	if err := json.Unmarshal(out.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	var encrypted []byte
	if err := f.db.Pool.QueryRow(context.Background(), `SELECT notify_email FROM demo_runs WHERE id=$1`, run.ID).Scan(&encrypted); err != nil || len(encrypted) == 0 {
		t.Fatal("email missing", err)
	}
	if decoded, err := f.svc.Box.Decrypt(encrypted, "demo-notify:"+run.ID); err != nil || decoded != email {
		t.Fatal("email not encrypted for run", err)
	}
}

func TestC6NotificationHTTPReads(t *testing.T) {
	f := newFixture(t)
	f.svc.Config.MailMode = "log"
	f.svc.Config.MailRedirectTo = "judge@judge-mail.dev"
	f.svc.Mailer = &recordingMailer{}
	ctx := context.Background()
	i, _, err := f.svc.Create(ctx, f.actor("senior", 1), intake("900.00", false), uuid.NewString(), "notification-http")
	if err != nil {
		t.Fatal(err)
	}
	senior, reviewer := f.newClient("127.0.0.123"), f.newClient("127.0.0.124")
	senior.login(t, "senior1@example.test")
	reviewer.login(t, "reviewer1@example.test")
	check := func(want string) {
		t.Helper()
		for _, c := range []*client{senior, reviewer} {
			out := c.request("GET", "/api/intents/"+i.ID, nil, nil)
			status(t, out, 200)
			var dto struct {
				Notification *struct {
					Status string  `json:"status"`
					SentAt *string `json:"sent_at"`
				} `json:"contact_notification"`
			}
			if err := json.Unmarshal(out.Body.Bytes(), &dto); err != nil || dto.Notification == nil || dto.Notification.Status != want || (want == "SENT" && dto.Notification.SentAt == nil) {
				t.Fatal("notification DTO", err)
			}
			out = c.request("GET", "/api/intents", nil, nil)
			status(t, out, 200)
			var list []map[string]json.RawMessage
			if err := json.Unmarshal(out.Body.Bytes(), &list); err != nil || len(list) != 1 || !strings.Contains(string(list[0]["contact_notification"]), want) {
				t.Fatal("list notification DTO", err)
			}
			if strings.Contains(out.Body.String(), "judge@judge-mail.dev") {
				t.Fatal("recipient leaked")
			}
		}
	}
	check("QUEUED")
	if worked, err := f.svc.WorkNotificationOnce(ctx); err != nil || !worked {
		t.Fatal("notification worker", err)
	}
	check("SENT")
}
