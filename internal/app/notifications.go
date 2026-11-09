package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/ramst/pause/internal/notify"
	"github.com/ramst/pause/internal/store"
)

func mailField(s string) string { return strings.NewReplacer("\r", "", "\n", "").Replace(s) }

func testMailAddress(address string) bool {
	domain := strings.ToLower(address[strings.LastIndex(address, "@")+1:])
	for _, reserved := range []string{"test", "example", "invalid", "localhost", "example.com", "example.org", "example.net"} {
		if domain == reserved || strings.HasSuffix(domain, "."+reserved) {
			return true
		}
	}
	return false
}

// WorkNotificationOnce reloads canonical task state before each advisory send.
// It never mutates verification, review, release eligibility or payment state.
func (s *Service) WorkNotificationOnce(ctx context.Context) (bool, error) {
	if s.Mailer == nil {
		return false, nil
	}
	jobs := &store.Jobs{DB: s.DB, Clock: s.Clock}
	job, err := jobs.Lease(ctx, []string{"CONTACT_NOTIFY"}, 2*time.Minute)
	if err != nil || job == nil {
		return false, err
	}
	now := s.Clock.Now()
	finish := func(status, code string) (bool, error) {
		_, err := s.DB.Pool.Exec(ctx, `UPDATE contact_notifications SET status=$2,updated_at=$3,sent_at=CASE WHEN $2='SENT' THEN $3 ELSE sent_at END,error_code=NULLIF($4,'') WHERE verification_id=$1`, job.ObjectID, status, s.Clock.Now(), code)
		if err != nil {
			return true, err
		}
		return true, jobs.Complete(ctx, *job)
	}
	var status, notificationStatus, payment, email, senior string
	var expires, deadline time.Time
	var cleanup, seeded, current, demoEnded bool
	var runID *string
	var encrypted, snapshot []byte
	err = s.DB.Pool.QueryRow(ctx, `SELECT t.status,n.status,t.expires_at,i.payment_status,i.cleanup_requested,i.decision_deadline,i.current_version=t.intent_version,EXISTS(SELECT 1 FROM synthetic_history h WHERE h.intent_id=i.id),u.email,senior.display_name,t.request_snapshot,d.id::text,d.notify_email,COALESCE(d.resetting OR d.closed_at IS NOT NULL OR d.expires_at<=$2,false)
 FROM verifications t JOIN contact_notifications n ON n.verification_id=t.id JOIN payment_intents i ON i.id=t.intent_id JOIN users u ON u.id=t.subject_user_id JOIN users senior ON senior.id=i.creator_id LEFT JOIN demo_runs d ON d.household_id=i.household_id WHERE t.id=$1`, job.ObjectID, now).Scan(&status, &notificationStatus, &expires, &payment, &cleanup, &deadline, &current, &seeded, &email, &senior, &snapshot, &runID, &encrypted, &demoEnded)
	if err != nil {
		return true, err
	}
	// A crash after storing a final result must not send that notice again.
	if notificationStatus != "QUEUED" {
		return true, jobs.Complete(ctx, *job)
	}
	if status != "PENDING" || !expires.After(now) || seeded || cleanup || !current || !deadline.After(now) || demoEnded || payment == "CAPTURED" || payment == "VOIDED" || payment == "CLOSED_UNCAPTURED" || payment == "ABANDONED" {
		return finish("SKIPPED_NOT_PENDING", "")
	}
	if len(encrypted) > 0 && runID != nil {
		email, err = s.Box.Decrypt(encrypted, "demo-notify:"+*runID)
		if err != nil {
			return finish("FAILED", "MAIL_RECIPIENT")
		}
	} else if s.Config.Environment != "production" && s.Config.MailRedirectTo != "" {
		email = s.Config.MailRedirectTo
	}
	if strings.TrimSpace(email) == "" {
		return finish("SKIPPED_NO_RECIPIENT", "")
	}
	address, err := mail.ParseAddress(email)
	if err != nil || strings.ContainsAny(email, "\r\n") {
		return finish("FAILED", "MAIL_RECIPIENT")
	}
	if testMailAddress(address.Address) {
		return finish("SKIPPED_TEST_ADDRESS", "")
	}
	var task taskSnapshot
	if json.Unmarshal(snapshot, &task) != nil {
		return finish("FAILED", "MAIL_CONTENT")
	}
	amount := explanationMoney(task.Binding.AmountMinor)
	senior = mailField(senior)
	body := fmt.Sprintf("%s started a %s purchase at %s for “%s”.\nPause is holding it until you answer.\nPlease answer by %s.\nSign in to Pause to confirm or deny the exact purchase: %s\nFor your safety, this email contains no answer buttons. If you did not ask for this, say so in Pause.", senior, amount, mailField(task.MerchantLabel), mailField(task.Binding.Purpose), expires.UTC().Format("15:04 UTC on Jan 2"), mailField(s.Config.Origin))
	if runID != nil {
		body = "This is a synthetic Pause demo. No real money moves.\n" + body
	}
	err = s.Mailer.Send(ctx, notify.Message{To: address.Address, Subject: fmt.Sprintf("Did you ask %s for %s?", senior, amount), Body: body})
	if err == nil {
		return finish("SENT", "")
	}
	code := "MAIL_FAILED"
	var mailErr *notify.Error
	if errors.As(err, &mailErr) {
		// Only bounded safe codes are retained; never persist dependency text.
		if safeMailCode(mailErr.Code) {
			code = mailErr.Code
		}
		if mailErr.Temporary && job.Attempts < 5 {
			if _, err = s.DB.Pool.Exec(ctx, `UPDATE contact_notifications SET updated_at=$2,error_code=$3 WHERE verification_id=$1`, job.ObjectID, s.Clock.Now(), code); err != nil {
				return true, err
			}
			return true, jobs.Retry(ctx, *job, "MAIL_RETRY", time.Duration(job.Attempts)*30*time.Second)
		}
	}
	return finish("FAILED", code)
}

func safeMailCode(code string) bool {
	if !strings.HasPrefix(code, "MAIL_") || len(code) > 64 {
		return false
	}
	for _, r := range code {
		if r != '_' && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

func (s *Service) PurgeNotificationEmails(ctx context.Context) error {
	_, err := s.DB.Pool.Exec(ctx, `UPDATE demo_runs SET notify_email=NULL WHERE notify_email IS NOT NULL AND (expires_at<=$1 OR closed_at IS NOT NULL)`, s.Clock.Now())
	return err
}
