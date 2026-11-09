package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ramst/pause/internal/domain"
	"github.com/ramst/pause/internal/notify"
)

type fakeMailer struct {
	messages []notify.Message
	err      error
}

func (m *fakeMailer) Send(_ context.Context, msg notify.Message) error {
	m.messages = append(m.messages, msg)
	return m.err
}

func notificationFixture(t *testing.T) (*paymentFixture, *fakeMailer) {
	t.Helper()
	f := setupPayments(t)
	m := &fakeMailer{}
	f.s.Mailer = m
	f.s.Config.MailMode = "log"
	f.s.Config.MailRedirectTo = "judge@pause-mail.dev"
	f.s.Config.Origin = "https://pause.dev"
	f.s.Config.MerchantLabel = "Fixture store"
	return f, m
}
func notificationState(t *testing.T, f *paymentFixture, id, want string) {
	t.Helper()
	var status string
	if err := f.s.DB.Pool.QueryRow(context.Background(), `SELECT status FROM contact_notifications WHERE intent_id=$1`, id).Scan(&status); err != nil || status != want {
		t.Fatalf("notification want %s got %s: %v", want, status, err)
	}
}
func notificationWork(t *testing.T, f *paymentFixture) {
	t.Helper()
	worked, err := f.s.WorkNotificationOnce(context.Background())
	if err != nil || !worked {
		t.Fatalf("notification work: %v %v", worked, err)
	}
}

func TestC6NotificationEnqueueAndSend(t *testing.T) {
	f, m := notificationFixture(t)
	ctx := context.Background()
	i := f.draft(t)
	if err := f.s.RequestVerification(ctx, actor(domain.Senior), i.ID, 1, "dedup"); err != nil {
		t.Fatal(err)
	}
	var notifications, jobs int
	if err := f.s.DB.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM contact_notifications),(SELECT count(*) FROM jobs WHERE kind='CONTACT_NOTIFY')`).Scan(&notifications, &jobs); err != nil || notifications != 1 || jobs != 1 {
		t.Fatal("enqueue dedup", notifications, jobs, err)
	}
	notificationState(t, f, i.ID, "QUEUED")
	f.s.Mailer = nil
	if worked, err := f.s.WorkNotificationOnce(ctx); worked || err != nil {
		t.Fatal("nil mailer leased")
	}
	var attempts int
	if err := f.s.DB.Pool.QueryRow(ctx, `SELECT attempts FROM jobs WHERE kind='CONTACT_NOTIFY'`).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatal("nil mailer changed job")
	}
	f.s.Mailer = m
	notificationWork(t, f)
	notificationState(t, f, i.ID, "SENT")
	if worked, err := f.s.WorkNotificationOnce(ctx); worked || err != nil {
		t.Fatal("repeat lease")
	}
	if len(m.messages) != 1 {
		t.Fatal("send count")
	}
	msg := m.messages[0]
	if msg.To != "judge@pause-mail.dev" || !strings.Contains(msg.Subject, "$40.01?") || !strings.Contains(msg.Body, "Fixture store") || !strings.Contains(msg.Body, "10:15 UTC on Oct 29") {
		t.Fatal("rendered binding")
	}
	if strings.Contains(msg.Body, "Synthetic message; fixture only") || strings.Contains(msg.Body, "token") || strings.Contains(msg.Body, "?") || strings.Contains(msg.Body, "synthetic Pause demo") {
		t.Fatal("private message or tokens in body")
	}
	for _, role := range []domain.Role{domain.Senior, domain.Reviewer} {
		read, err := f.s.Read(ctx, actor(role), i.ID)
		if err != nil || read.ContactNotification == nil || read.ContactNotification.Status != "SENT" || read.ContactNotification.SentAt == nil {
			t.Fatal("read notice", err)
		}
		list, err := f.s.List(ctx, actor(role))
		if err != nil || len(list) != 1 || list[0].ContactNotification.Status != "SENT" {
			t.Fatal("list notice", err)
		}
	}
	// A final status survives a lost Complete and prevents another send.
	if _, err := f.s.DB.Pool.Exec(ctx, `UPDATE jobs SET completed_at=NULL WHERE kind='CONTACT_NOTIFY'`); err != nil {
		t.Fatal(err)
	}
	notificationWork(t, f)
	if len(m.messages) != 1 {
		t.Fatal("resent finalized notice")
	}
}

func TestC6NotificationOff(t *testing.T) {
	f, _ := notificationFixture(t)
	f.s.Config.MailMode = "off"
	f.draft(t)
	var n int
	if err := f.s.DB.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM contact_notifications)+(SELECT count(*) FROM jobs WHERE kind='CONTACT_NOTIFY')`).Scan(&n); err != nil || n != 0 {
		t.Fatal("off enqueued", n, err)
	}
}

func TestC6NotificationSendChecks(t *testing.T) {
	for _, tc := range []struct{ name, sql string }{
		{"expired", `UPDATE verifications SET expires_at=$2 WHERE intent_id=$1`},
		{"answered", `UPDATE verifications SET status='DENIED' WHERE intent_id=$1 AND $2::timestamptz IS NOT NULL`},
		{"cleanup", `UPDATE payment_intents SET cleanup_requested=true WHERE id=$1 AND $2::timestamptz IS NOT NULL`},
		{"terminal", `UPDATE payment_intents SET payment_status='ABANDONED' WHERE id=$1 AND $2::timestamptz IS NOT NULL`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, m := notificationFixture(t)
			i := f.draft(t)
			if _, err := f.s.DB.Pool.Exec(context.Background(), tc.sql, i.ID, f.clock.Now()); err != nil {
				t.Fatal(err)
			}
			notificationWork(t, f)
			notificationState(t, f, i.ID, "SKIPPED_NOT_PENDING")
			if len(m.messages) != 0 {
				t.Fatal("sent closed request")
			}
		})
	}
}

func TestC6NotificationRecipients(t *testing.T) {
	for _, address := range []string{"a@person.test", "a@person.example", "a@person.invalid", "a@localhost", "a@example.com", "a@example.org", "a@example.net", "a@sub.example.com"} {
		t.Run(address, func(t *testing.T) {
			f, m := notificationFixture(t)
			f.s.Config.MailRedirectTo = address
			i := f.draft(t)
			notificationWork(t, f)
			notificationState(t, f, i.ID, "SKIPPED_TEST_ADDRESS")
			if len(m.messages) != 0 {
				t.Fatal("test address sent")
			}
		})
	}
	for _, production := range []bool{false, true} {
		t.Run(map[bool]string{false: "own email", true: "production ignores redirect"}[production], func(t *testing.T) {
			f, m := notificationFixture(t)
			if production {
				f.s.Config.Environment = "production"
			} else {
				f.s.Config.MailRedirectTo = ""
			}
			if _, err := f.s.DB.Pool.Exec(context.Background(), `UPDATE users SET email='contact@pause-mail.dev' WHERE id=$1`, actor(domain.Subject).UserID); err != nil {
				t.Fatal(err)
			}
			i := f.draft(t)
			notificationWork(t, f)
			notificationState(t, f, i.ID, "SENT")
			if len(m.messages) != 1 || m.messages[0].To != "contact@pause-mail.dev" {
				t.Fatal("own email priority")
			}
		})
	}
	t.Run("no recipient", func(t *testing.T) {
		f, m := notificationFixture(t)
		f.s.Config.MailRedirectTo = ""
		if _, err := f.s.DB.Pool.Exec(context.Background(), `UPDATE users SET email='' WHERE id=$1`, actor(domain.Subject).UserID); err != nil {
			t.Fatal(err)
		}
		i := f.draft(t)
		notificationWork(t, f)
		notificationState(t, f, i.ID, "SKIPPED_NO_RECIPIENT")
		if len(m.messages) != 0 {
			t.Fatal("empty recipient sent")
		}
	})
}

func TestC6NotificationFailures(t *testing.T) {
	for _, temporary := range []bool{true, false} {
		t.Run(map[bool]string{true: "temporary", false: "permanent"}[temporary], func(t *testing.T) {
			f, m := notificationFixture(t)
			i := f.draft(t)
			m.err = &notify.Error{Code: "MAIL_REJECTED", Temporary: temporary}
			limit := 1
			if temporary {
				limit = 5
			}
			for n := 1; n <= limit; n++ {
				notificationWork(t, f)
				if n < limit {
					notificationState(t, f, i.ID, "QUEUED")
					var attempts int
					var reason string
					var next time.Time
					if err := f.s.DB.Pool.QueryRow(context.Background(), `SELECT attempts,last_error_code,not_before FROM jobs WHERE kind='CONTACT_NOTIFY'`).Scan(&attempts, &reason, &next); err != nil || attempts != n || reason != "MAIL_RETRY" || !next.Equal(f.clock.Now().Add(time.Duration(n)*30*time.Second)) {
						t.Fatal("backoff", err)
					}
					if worked, err := f.s.WorkNotificationOnce(context.Background()); err != nil || worked {
						t.Fatal("retried too early")
					}
					f.clock.now = next
				}
			}
			notificationState(t, f, i.ID, "FAILED")
			if len(m.messages) != limit {
				t.Fatal("wrong retry count")
			}
			var code string
			var complete bool
			if err := f.s.DB.Pool.QueryRow(context.Background(), `SELECT n.error_code,j.completed_at IS NOT NULL FROM contact_notifications n JOIN jobs j ON j.object_id=n.verification_id AND j.kind='CONTACT_NOTIFY'`).Scan(&code, &complete); err != nil || code != "MAIL_REJECTED" || !complete {
				t.Fatal("failure status", err)
			}
		})
	}
	t.Run("unknown error sanitized", func(t *testing.T) {
		f, m := notificationFixture(t)
		i := f.draft(t)
		m.err = errors.New("private@pause.dev SMTP response secret")
		notificationWork(t, f)
		notificationState(t, f, i.ID, "FAILED")
		var code string
		if err := f.s.DB.Pool.QueryRow(context.Background(), `SELECT error_code FROM contact_notifications`).Scan(&code); err != nil || code != "MAIL_FAILED" {
			t.Fatal("unsafe error", err)
		}
	})
}

func TestC6DemoNotifications(t *testing.T) {
	f, m := notificationFixture(t)
	f.s.Config.DemoEnabled = true
	f.s.Config.Environment = "test"
	ctx := context.Background()
	r, err := f.s.LaunchDemo(ctx, "stopped", "judge-peer", " judge@judge-mail.dev ")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(r)
	if strings.Contains(string(raw), "judge@judge-mail.dev") || strings.Contains(string(raw), "notify_email") {
		t.Fatal("email echoed")
	}
	var ciphertext []byte
	if err = f.s.DB.Pool.QueryRow(ctx, `SELECT notify_email FROM demo_runs WHERE id=$1`, r.ID).Scan(&ciphertext); err != nil || strings.Contains(string(ciphertext), "judge@judge-mail.dev") {
		t.Fatal("unencrypted", err)
	}
	decoded, err := f.s.Box.Decrypt(ciphertext, "demo-notify:"+r.ID)
	if err != nil || decoded != "judge@judge-mail.dev" {
		t.Fatal("encrypted recipient", err)
	}
	i := demoDraft(t, f, r, "900.00")
	for n := 0; n < 3; n++ {
		notificationWork(t, f)
	}
	if len(m.messages) != 1 || m.messages[0].To != "judge@judge-mail.dev" {
		t.Fatal("judge override / seeded sends")
	}
	notificationState(t, f, i.ID, "SENT")
	var skipped int
	if err = f.s.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM contact_notifications n JOIN synthetic_history h ON h.intent_id=n.intent_id WHERE n.status='SKIPPED_NOT_PENDING'`).Scan(&skipped); err != nil || skipped != 2 {
		t.Fatal("seeded requests", err)
	}
	body := m.messages[0].Body
	if !strings.HasPrefix(body, "This is a synthetic Pause demo. No real money moves.\n") || strings.Contains(body, "Planned purchase; please confirm with me.") || strings.Contains(body, r.ResetToken) || strings.Contains(body, "token") {
		t.Fatal("unsafe demo body")
	}
	if state, err := f.s.ResetDemo(ctx, r.ID, r.ResetToken); err != nil || state != "CLOSED" {
		t.Fatal("reset", state, err)
	}
	if err = f.s.DB.Pool.QueryRow(ctx, `SELECT notify_email FROM demo_runs WHERE id=$1`, r.ID).Scan(&ciphertext); err != nil || ciphertext != nil {
		t.Fatal("reset did not purge", err)
	}
	// Expiry purging is also run by the worker without requiring a reset.
	r, err = f.s.LaunchDemo(ctx, "positive", "second-peer", "other@judge-mail.dev")
	if err != nil {
		t.Fatal(err)
	}
	f.clock.now = f.clock.now.Add(3 * time.Hour)
	if err = f.s.PurgeNotificationEmails(ctx); err != nil {
		t.Fatal(err)
	}
	if err = f.s.DB.Pool.QueryRow(ctx, `SELECT notify_email FROM demo_runs WHERE id=$1`, r.ID).Scan(&ciphertext); err != nil || ciphertext != nil {
		t.Fatal("expiry did not purge", err)
	}
}

func TestC6RenderStripsLineBreaks(t *testing.T) {
	f, m := notificationFixture(t)
	ctx := context.Background()
	if _, err := f.s.DB.Pool.Exec(ctx, `UPDATE users SET display_name=E'Senior\r\nInjected' WHERE id=$1`, actor(domain.Senior).UserID); err != nil {
		t.Fatal(err)
	}
	f.s.Config.MerchantLabel = "Store\r\nInjected"
	f.draft(t)
	notificationWork(t, f)
	if len(m.messages) != 1 || strings.ContainsAny(m.messages[0].Subject, "\r\n") || !strings.Contains(m.messages[0].Body, "StoreInjected") {
		t.Fatal("interpolated CR/LF")
	}
}
