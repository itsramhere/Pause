package app

import (
	"context"
	"github.com/google/uuid"
	"github.com/ramst/pause/internal/domain"
	"testing"
	"time"
)

func demoActor(t *testing.T, f *paymentFixture, r DemoRun, role domain.Role) domain.Actor {
	t.Helper()
	var a domain.Actor
	a.Role = role
	if e := f.s.DB.Pool.QueryRow(context.Background(), `SELECT m.user_id::text,m.household_id::text FROM memberships m JOIN demo_runs d ON d.household_id=m.household_id WHERE d.id=$1 AND m.role=$2`, r.ID, role).Scan(&a.UserID, &a.HouseholdID); e != nil {
		t.Fatal(e)
	}
	return a
}
func demoDraft(t *testing.T, f *paymentFixture, r DemoRun, amount string) domain.Intent {
	t.Helper()
	a := demoActor(t, f, r, domain.Senior)
	var contact string
	if e := f.s.DB.Pool.QueryRow(context.Background(), `SELECT id::text FROM contacts WHERE household_id=$1`, a.HouseholdID).Scan(&contact); e != nil {
		t.Fatal(e)
	}
	no := false
	i, _, e := f.s.Create(context.Background(), a, Intake{Amount: amount, Currency: "USD", MerchantID: f.s.Config.MerchantID, SubjectContactID: contact, Purpose: "Synthetic judge purchase", Message: "Planned purchase; please confirm with me.", HistoryConsent: &no}, uuid.NewString(), "test")
	if e != nil {
		t.Fatal(e)
	}
	return i
}
func TestP05DemoIsolationLimitsAndReset(t *testing.T) {
	f := setupPayments(t)
	f.s.Config.DemoEnabled = true
	f.s.Config.Environment = "test"
	ctx := context.Background()
	a, e := f.s.LaunchDemo(ctx, "positive", "peer-a")
	if e != nil {
		t.Fatal(e)
	}
	b, e := f.s.LaunchDemo(ctx, "stopped", "peer-a")
	if e != nil {
		t.Fatal(e)
	}
	if a.ID == b.ID || a.Accounts[0].Email == b.Accounts[0].Email || a.Accounts[0].Password == b.Accounts[0].Password {
		t.Fatal("runs reused identities")
	}
	if _, e = f.s.LaunchDemo(ctx, "positive", "peer-a"); e == nil || e.Error() != "DEMO_THROTTLED" {
		t.Fatal("launch admission", e)
	}
	i := demoDraft(t, f, a, "900.00")
	owner := demoActor(t, f, a, domain.Senior)
	if _, e = f.s.Read(ctx, demoActor(t, f, b, domain.Senior), i.ID); e == nil {
		t.Fatal("cross-run read")
	}
	if _, e = f.s.Cancel(ctx, demoActor(t, f, b, domain.Reviewer), i.ID, 1, "test"); e == nil {
		t.Fatal("cross-run cancel")
	}
	key := uuid.NewString()
	if _, e = f.s.ReserveCheckout(ctx, owner, i.ID, "CREATE_ORDER", key, PaymentAction{ExpectedVersion: 1}, "test"); e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.ReserveCheckout(ctx, owner, i.ID, "CREATE_ORDER", key, PaymentAction{ExpectedVersion: 1}, "test"); e != nil {
		t.Fatal("idempotent reservation", e)
	}
	j := demoDraft(t, f, a, "101.00")
	if _, e = f.s.ReserveCheckout(ctx, owner, j.ID, "CREATE_ORDER", uuid.NewString(), PaymentAction{ExpectedVersion: 1}, "test"); e == nil || e.Error() != "DEMO_PAYMENT_LIMIT" {
		t.Fatal("spending cap", e)
	}
	if _, e = f.s.ResetDemo(ctx, a.ID, b.ResetToken); e == nil {
		t.Fatal("foreign reset token")
	}
	state, e := f.s.ResetDemo(ctx, a.ID, a.ResetToken)
	if e != nil || state != "CLEANUP_PENDING" {
		t.Fatal("unresolved order erased", state, e)
	}
	var n int
	if e = f.s.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM payment_operations WHERE intent_id=$1`, i.ID).Scan(&n); e != nil || n != 1 {
		t.Fatal("operations lost", n, e)
	}
	if _, e = f.s.ReserveCheckout(ctx, owner, j.ID, "CREATE_ORDER", uuid.NewString(), PaymentAction{ExpectedVersion: 1}, "test"); e == nil {
		t.Fatal("order during reset")
	}
	state, e = f.s.ResetDemo(ctx, b.ID, b.ResetToken)
	if e != nil || state != "CLOSED" {
		t.Fatal("draft-only reset", state, e)
	}
	f.clock.now = f.clock.now.Add(3 * time.Hour)
	if _, e = f.s.ReserveCheckout(ctx, owner, j.ID, "CREATE_ORDER", uuid.NewString(), PaymentAction{ExpectedVersion: 1}, "test"); e == nil {
		t.Fatal("expired launch")
	}
}
func TestP05DemoOrderCountCapAndDefaultOff(t *testing.T) {
	f := setupPayments(t)
	ctx := context.Background()
	if _, e := f.s.LaunchDemo(ctx, "positive", "peer"); e == nil {
		t.Fatal("default demo on")
	}
	f.s.Config.DemoEnabled = true
	f.s.Config.Environment = "production"
	if _, e := f.s.LaunchDemo(ctx, "positive", "peer"); e == nil {
		t.Fatal("production demo on")
	}
	f.s.Config.Environment = "test"
	r, e := f.s.LaunchDemo(ctx, "positive", "peer")
	if e != nil {
		t.Fatal(e)
	}
	for n := 0; n < 3; n++ {
		i := demoDraft(t, f, r, "1.00")
		_, e = f.s.ReserveCheckout(ctx, demoActor(t, f, r, domain.Senior), i.ID, "CREATE_ORDER", uuid.NewString(), PaymentAction{ExpectedVersion: 1}, "test")
		if (n < 2 && e != nil) || (n == 2 && (e == nil || e.Error() != "DEMO_PAYMENT_LIMIT")) {
			t.Fatal("order cap", n, e)
		}
	}
}

func TestP05DemoResetHeldAuthorizationAndGlobalCap(t *testing.T) {
	f := setupPayments(t)
	f.s.Config.DemoEnabled = true
	f.s.Config.Environment = "test"
	ctx := context.Background()
	r, e := f.s.LaunchDemo(ctx, "positive", "peer-zero")
	if e != nil {
		t.Fatal(e)
	}
	i := demoDraft(t, f, r, "1.00")
	a := demoActor(t, f, r, domain.Senior)
	if _, e = f.s.ReserveCheckout(ctx, a, i.ID, "CREATE_ORDER", uuid.NewString(), PaymentAction{ExpectedVersion: 1}, "test"); e != nil {
		t.Fatal(e)
	}
	f.work(t)
	i, e = f.s.Read(ctx, a, i.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.ReserveCheckout(ctx, a, i.ID, "AUTHORIZE", uuid.NewString(), PaymentAction{ExpectedVersion: 1, OrderID: *i.Checkout.ProviderOrderID}, "test"); e != nil {
		t.Fatal(e)
	}
	f.work(t)
	state, e := f.s.ResetDemo(ctx, r.ID, r.ResetToken)
	if e != nil || state != "CLEANUP_PENDING" {
		t.Fatal(state, e)
	}
	f.work(t)
	state, e = f.s.ResetDemo(ctx, r.ID, r.ResetToken)
	if e != nil || state != "CLOSED" {
		t.Fatal(state, e)
	}
	i, e = f.s.Read(ctx, a, i.ID)
	if e != nil || i.Payment != domain.Voided {
		t.Fatal("reset must confirm void", i.Payment, e)
	}
	for n := 1; n < 6; n++ {
		if _, e = f.s.LaunchDemo(ctx, "positive", uuid.NewString()); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = f.s.LaunchDemo(ctx, "positive", "peer-seven"); e == nil || e.Error() != "DEMO_THROTTLED" {
		t.Fatal("global limit", e)
	}
}
