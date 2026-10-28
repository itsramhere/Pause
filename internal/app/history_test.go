package app

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/ramst/pause/internal/domain"
	"github.com/ramst/pause/internal/store"
)

func p04Request(t *testing.T, f *paymentFixture, amount string, consent, hold bool) domain.Intent {
	t.Helper()
	ctx := context.Background()
	i, _, err := f.s.Create(ctx, actor(domain.Senior), Intake{Amount: amount, Currency: "USD", MerchantID: f.s.Config.MerchantID, SubjectContactID: store.SeedID("contact-subject-1"), Purpose: "Synthetic clinic transport", Message: "Please help with the scheduled clinic ride and medication purchase.", HistoryConsent: &consent}, uuid.NewString(), "p04-test")
	if err != nil {
		t.Fatal(err)
	}
	if hold {
		if _, err = f.s.ReserveCheckout(ctx, actor(domain.Senior), i.ID, "CREATE_ORDER", uuid.NewString(), PaymentAction{ExpectedVersion: 1}, "test"); err != nil {
			t.Fatal(err)
		}
		f.work(t)
		i, err = f.s.Read(ctx, actor(domain.Senior), i.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.s.ReserveCheckout(ctx, actor(domain.Senior), i.ID, "AUTHORIZE", uuid.NewString(), PaymentAction{ExpectedVersion: 1, OrderID: *i.Checkout.ProviderOrderID}, "test"); err != nil {
			t.Fatal(err)
		}
		f.work(t)
	}
	return i
}
func p04History(t *testing.T, f *paymentFixture, id string) HistoryPacket {
	t.Helper()
	_, h, err := f.s.historyForAnalysis(context.Background(), actor(domain.Reviewer), id)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// Card, subject inbox, evidence and canonical timeline use the same exclusive
// task expiry boundary. Rendering expiry never overwrites the recorded answer.
func TestP04VerificationExpiryConsistentAcrossViews(t *testing.T) {
	for _, status := range []string{"PENDING", "CONFIRMED", "DENIED"} {
		t.Run(status, func(t *testing.T) {
			f := setupPayments(t)
			ctx := context.Background()
			i := p04Request(t, f, "50.00", true, false)
			p03Engine(t, f)
			task, answer := taskAnswer(t, f, i)
			if status != "PENDING" {
				answer.Response = status
				if err := f.s.RespondVerification(ctx, actor(domain.Subject), task.ID, answer, "expiry-display-test"); err != nil {
					t.Fatal(err)
				}
			}
			check := func(expected string) {
				t.Helper()
				card, err := f.s.Read(ctx, actor(domain.Senior), i.ID)
				if err != nil || string(card.Verification) != expected {
					t.Fatal("card", card.Verification, err)
				}
				history := p04History(t, f, i.ID)
				if len(history.Rows) != 1 || history.Rows[0].Response != expected {
					t.Fatal("timeline", history.Rows)
				}
				packet, err := f.s.Evidence(ctx, actor(domain.Senior), i.ID)
				if err != nil {
					t.Fatal(err)
				}
				verification := packet.Verification.(map[string]any)
				if verification["status"] != expected {
					t.Fatal("evidence", verification)
				}
				tasks, err := f.s.Tasks(ctx, actor(domain.Subject))
				if err != nil || len(tasks) != 1 || tasks[0].Status != expected {
					t.Fatal("inbox", tasks, err)
				}
				if status != "PENDING" && (history.Rows[0].ResponseSource != "AUTHENTICATED_RESPONSE" || history.Rows[0].ResponseAt == nil) {
					t.Fatal("recorded answer provenance lost")
				}
			}
			f.clock.now = task.ExpiresAt.Add(-time.Microsecond)
			check(status)
			expected := "EXPIRED"
			if status == "DENIED" {
				expected = "DENIED"
			}
			f.clock.now = task.ExpiresAt
			check(expected)
			var stored string
			if err := f.s.DB.Pool.QueryRow(ctx, `SELECT status FROM verifications WHERE id=$1`, task.ID).Scan(&stored); err != nil || stored != status {
				t.Fatal("rendering rewrote answer", stored, err)
			}
		})
	}
}

func TestP04CanonicalAmountsVersionsScopeAndProvenance(t *testing.T) {
	f := setupPayments(t)
	ctx := context.Background()
	now := f.clock.now
	f.clock.now = now.Add(-4 * 24 * time.Hour)
	first := p04Request(t, f, "40.00", false, true)
	evidence := f.evidence(t, first)
	if err := f.s.ReviewApprove(ctx, actor(domain.Reviewer), first.ID, evidence, "Fixture confirmed exact purchase", "test"); err != nil {
		t.Fatal(err)
	}
	f.work(t)
	yes := true
	if _, err := f.s.ChangeConsent(ctx, actor(domain.Senior), first.ID, ConsentChange{ExpectedVersion: 1, History: &yes}, "test"); err != nil {
		t.Fatal(err)
	}
	f.clock.now = now.Add(-2 * 24 * time.Hour)
	second := p04Request(t, f, "180.00", true, true)
	if _, err := f.s.Cancel(ctx, actor(domain.Senior), second.ID, 1, "test"); err != nil {
		t.Fatal(err)
	}
	f.work(t)
	f.clock.now = now
	current := p04Request(t, f, "900.00", true, true)
	h := p04History(t, f, current.ID)
	if len(h.Rows) != 3 || h.Totals.Requested != 112000 || h.Totals.Captured != 4000 || h.Totals.Held != 90000 || h.Totals.Pending != 0 || !h.Rules[0].Matched || !h.Rules[1].Matched {
		t.Fatal(h)
	}
	// Replayed provider observations cannot add a canonical request.
	if err := f.s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		p, err := f.s.paymentLock(ctx, tx, current.ID)
		if err != nil {
			return err
		}
		return f.s.applyTruth(ctx, tx, &p, f.p.states[current.ID])
	}); err != nil {
		t.Fatal(err)
	}
	again := p04History(t, f, current.ID)
	if len(again.Rows) != 3 || again.Totals != h.Totals {
		t.Fatal(again)
	}
	if h.Rows[0].OutcomeSource != "PROVIDER_EVENT" || h.Rows[1].Status != "VOIDED" {
		t.Fatal(h.Rows)
	}
	// Same merchant and nickname with another stable contact must not link.
	if _, err := f.s.DB.Pool.Exec(ctx, `INSERT INTO contacts(id,household_id,user_id,relationship,enrolled_at,provenance) VALUES($1,$2,$3,'Same nickname',$4,'SYNTHETIC_TEST')`, uuid.NewString(), actor(domain.Senior).HouseholdID, actor(domain.Subject).UserID, now); err != nil {
		t.Fatal(err)
	}
	var other string
	if err := f.s.DB.Pool.QueryRow(ctx, `SELECT id::text FROM contacts WHERE household_id=$1 AND id<>$2`, actor(domain.Senior).HouseholdID, store.SeedID("contact-subject-1")).Scan(&other); err != nil {
		t.Fatal(err)
	}
	_, _, err := f.s.Create(ctx, actor(domain.Senior), Intake{Amount: "100.00", Currency: "USD", MerchantID: f.s.Config.MerchantID, SubjectContactID: other, Purpose: "Same clinic purpose", Message: "Same clinic ride request", HistoryConsent: &yes}, uuid.NewString(), "test")
	if err != nil {
		t.Fatal(err)
	}
	again = p04History(t, f, current.ID)
	if len(again.Rows) != 3 || again.Totals != h.Totals {
		t.Fatal("contact merged", again)
	}
	no := false
	if _, err = f.s.ChangeConsent(ctx, actor(domain.Senior), first.ID, ConsentChange{ExpectedVersion: 2, History: &no}, "withdraw"); err != nil {
		t.Fatal(err)
	}
	again = p04History(t, f, current.ID)
	if len(again.Rows) != 2 || again.Totals.Requested != 108000 || again.Totals.Captured != 0 || again.PendingDeletes == 0 {
		t.Fatal("withdrawal reused history", again)
	}
	if _, err = f.s.ChangeConsent(ctx, actor(domain.Senior), current.ID, ConsentChange{ExpectedVersion: 1, History: &no}, "withdraw"); err != nil {
		t.Fatal(err)
	}
	again = p04History(t, f, current.ID)
	if again.Status != domain.LookupNotApplicable || len(again.Rows) != 0 || again.PendingDeletes == 0 {
		t.Fatal(again)
	}
}

func TestP04UTCWindowTiesEffectiveVersionsAndHydration(t *testing.T) {
	f := setupPayments(t)
	ctx := context.Background()
	now := f.clock.now
	f.s.Config.MessageRetention = 8 * 24 * time.Hour
	f.clock.now = now.Add(-7*24*time.Hour - time.Microsecond)
	outside := p04Request(t, f, "10.00", true, false)
	f.clock.now = now.Add(-7 * 24 * time.Hour)
	boundary := p04Request(t, f, "20.00", true, false)
	f.clock.now = now
	draft := p04Request(t, f, "30.00", true, false)
	yes := true
	corrected, err := f.s.Correct(ctx, actor(domain.Senior), draft.ID, Correction{ExpectedVersion: 1, ExpectedConsentVersion: 1, Intake: Intake{Amount: "35.00", Currency: "USD", MerchantID: f.s.Config.MerchantID, SubjectContactID: draft.Binding.SubjectContactID, Purpose: "Corrected entered purpose", Message: "Corrected private medical purchase", HistoryConsent: &yes}}, "test")
	if err != nil {
		t.Fatal(err)
	}
	current := p04Request(t, f, "100.00", true, false)
	h := p04History(t, f, current.ID)
	if len(h.Rows) != 3 || h.Totals.Requested != 15500 {
		t.Fatal(h)
	}
	for n, r := range h.Rows {
		if r.IntentID == outside.ID {
			t.Fatal("outside row")
		}
		if n > 0 && r.Time.Equal(h.Rows[n-1].Time) && r.IntentID < h.Rows[n-1].IntentID {
			t.Fatal("unstable tie order")
		}
		if r.IntentID == corrected.ID && r.Version != 2 {
			t.Fatal("superseded version")
		}
	}
	// Stale revision, cross-household ID, unknown source and current intent are
	// discarded by PG hydration regardless of what a search adapter returns.
	err = f.s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		result := domain.SearchResult{Status: domain.LookupOK, Hits: []domain.SearchHit{{SourceID: boundary.ID, SourceVersion: 1, ConsentVersion: 1, ProjectionRevision: 1}, {SourceID: corrected.ID, SourceVersion: 1, ConsentVersion: 1, ProjectionRevision: 1}, {SourceID: outside.ID, SourceVersion: 1, ConsentVersion: 1, ProjectionRevision: 1}, {SourceID: current.ID, SourceVersion: 1, ConsentVersion: 1, ProjectionRevision: 1}, {SourceID: uuid.NewString(), SourceVersion: 1, ConsentVersion: 1, ProjectionRevision: 1}}}
		return f.s.hydrateHistoryPacket(ctx, tx, &h, result)
	})
	if err != nil || len(h.Matches) != 1 || h.Matches[0].IntentID != boundary.ID {
		t.Fatal(err, h.Matches)
	}
	if !strings.Contains(strings.Join(h.Notes, " "), "Corrected entered purpose") {
		t.Fatal("purpose provenance missing", h.Notes)
	}
}

func TestP04SeededScenarioClockAndBenignIsolation(t *testing.T) {
	f := setupPayments(t)
	ctx := context.Background()
	f.s.Config.Environment = "test"
	for _, scenario := range []string{"f04", "benign"} {
		if _, err := f.s.SeedPatterns(ctx, scenario, "Synthetic-fixture-password!"); err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.SeedPatterns(ctx, scenario, "Synthetic-fixture-password!"); err != nil {
			t.Fatal(err)
		}
		a := domain.Actor{UserID: store.SeedID("patterns-" + scenario + "-senior"), HouseholdID: store.SeedID("patterns-" + scenario), Role: domain.Senior}
		items, err := f.s.List(ctx, a)
		if err != nil || len(items) != 3 {
			t.Fatal(err, len(items))
		}
		_, h, err := f.s.historyForAnalysis(ctx, a, items[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		expected := int64(112000)
		if scenario == "benign" {
			expected = 19000
		}
		if h.Totals.Requested != expected || h.Totals.Captured != 0 || h.Totals.Held != 0 || h.Rows[0].Source != "SYNTHETIC_SEEDED_HISTORY" || h.Totals.SeededCaptured != h.Rows[0].Amount+h.Rows[1].Amount {
			t.Fatal(h)
		}
		if scenario == "benign" && patternAttention(h) {
			t.Fatal("benign seed tripped prototype rule")
		}
		for _, i := range items {
			if i.Checkout.ProviderOrderID != nil {
				t.Fatal("seed invented provider receipt")
			}
			var card map[string]any
			if err = json.Unmarshal(mustJSON(i), &card); err != nil {
				t.Fatal(err)
			}
			seed, seeded := card["synthetic_history"].(map[string]any)
			if i.ID == items[0].ID {
				if seeded {
					t.Fatal("current request mislabeled as completed seed")
				}
			} else if !seeded || seed["status"] != "SEEDED_CAPTURED" || seed["scenario"] != scenario {
				t.Fatal("saved card omits synthetic outcome provenance", card)
			}
		}
	}
}

func TestP04StandaloneHydrationUsesCanonicalWindowAndTieOrder(t *testing.T) {
	f := setupPayments(t)
	ctx := context.Background()
	f.s.Config.MessageRetention = 10 * 24 * time.Hour
	items := []domain.Intent{p04Request(t, f, "40.00", true, false), p04Request(t, f, "50.00", true, false)}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	query := domain.SearchQuery{Scope: domain.History, HouseholdID: actor(domain.Senior).HouseholdID, SubjectContactID: items[0].Binding.SubjectContactID, ExcludeIntentID: items[0].ID, ConsentVersion: 1, Cutoff: f.clock.Now().Add(-7 * 24 * time.Hour), Now: f.clock.Now(), EncoderVersion: "fixture-encoder", Limit: 3}
	hit := domain.SearchHit{SourceID: items[1].ID, SourceVersion: 1, ConsentVersion: 1, ProjectionRevision: 1}
	if _, err := f.s.HydrateHistory(ctx, actor(domain.Senior), query, hit); err == nil {
		t.Fatal("later tied request exposed as prior context")
	}
	query.ExcludeIntentID, hit.SourceID = items[1].ID, items[0].ID
	if _, err := f.s.HydrateHistory(ctx, actor(domain.Senior), query, hit); err != nil {
		t.Fatal("permitted earlier tied request rejected", err)
	}
	// A caller-provided older cutoff cannot widen the canonical seven-day window.
	f.clock.now = f.clock.now.Add(8 * 24 * time.Hour)
	query.Now = f.clock.Now()
	if _, err := f.s.HydrateHistory(ctx, actor(domain.Senior), query, hit); err == nil {
		t.Fatal("stale query widened the canonical event window")
	}
}

func TestP04HistorySnapshotChangesOnPeerReservation(t *testing.T) {
	f := setupPayments(t)
	ctx := context.Background()
	first := p04Request(t, f, "40.00", true, true)
	f.clock.now = f.clock.now.Add(time.Second)
	second := p04Request(t, f, "180.00", true, true)
	before := p04History(t, f, second.ID)
	err := f.s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		p, err := f.s.paymentLock(ctx, tx, first.ID)
		if err != nil {
			return err
		}
		return f.s.state(ctx, tx, &p, domain.CapturePending, "TEST_RESERVED_EXPOSURE")
	})
	if err != nil {
		t.Fatal(err)
	}
	after := p04History(t, f, second.ID)
	if after.SnapshotHash == before.SnapshotHash || after.Totals.Pending != 4000 || after.Totals.Held != 18000 {
		t.Fatal(before, after)
	}
	own := p04History(t, f, first.ID)
	err = f.s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		p, err := f.s.paymentLock(ctx, tx, first.ID)
		if err != nil {
			return err
		}
		return f.s.state(ctx, tx, &p, domain.Held, "TEST_REVERT_RESERVATION")
	})
	if err != nil {
		t.Fatal(err)
	}
	// Normalize the current request's reservation in its own decision hash.
	if own.SnapshotHash != p04History(t, f, first.ID).SnapshotHash {
		t.Fatal("own reservation invalidated its snapshot")
	}
}

// Two independent transactions contend on the real household row. Each has a
// valid snapshot before the first reservation; exactly one may reserve capture.
func TestP04ConcurrentHouseholdDecisionsReserveExposure(t *testing.T) {
	f := setupPayments(t)
	ctx := context.Background()
	first := p04Request(t, f, "40.00", true, true)
	f.clock.now = f.clock.now.Add(time.Second)
	second := p04Request(t, f, "180.00", true, true)
	ids := []domain.Intent{first, second}
	evidences := []string{}
	for _, i := range ids {
		evidences = append(evidences, f.evidence(t, i))
	}
	for n, i := range ids {
		id := evidences[n]
		err := f.s.DB.WithTx(ctx, func(tx pgx.Tx) error {
			p, err := f.s.paymentLock(ctx, tx, i.ID)
			if err != nil {
				return err
			}
			h, err := f.s.canonicalHistory(ctx, tx, p)
			if err != nil {
				return err
			}
			var raw []byte
			if err = tx.QueryRow(ctx, `SELECT evidence FROM evidence_snapshots WHERE id=$1`, id).Scan(&raw); err != nil {
				return err
			}
			var ev releaseEvidence
			if err = json.Unmarshal(raw, &ev); err != nil {
				return err
			}
			ev.HistoryHash = h.SnapshotHash
			ev.History = domain.SearchResult{SchemaVersion: domain.ContractVersion, Scope: domain.History, Status: domain.LookupOK, EncoderVersion: ev.Encoder.EncoderVersion, IndexVersion: "fixture-private", MappingVersion: "fixture-v1", FusionVersion: "fixture-v1", Hits: []domain.SearchHit{}}
			_, err = tx.Exec(ctx, `UPDATE evidence_snapshots SET evidence=$2 WHERE id=$1`, id, mustJSON(ev))
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for n, i := range ids {
		proof, err := f.s.validateReleaseSources(ctx, i.ID, evidences[n])
		if err != nil {
			t.Fatal(err)
		}
		if err = f.s.DB.WithTx(ctx, func(tx pgx.Tx) error {
			p, err := f.s.paymentLock(ctx, tx, i.ID)
			if err != nil {
				return err
			}
			_, err = f.s.eligibility(ctx, tx, p, actor(domain.Reviewer), evidences[n], proof)
			return err
		}); err != nil {
			t.Fatal("decision was invalid before concurrency", err)
		}
	}
	blocker, err := f.s.DB.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	if _, err = blocker.Exec(ctx, `SELECT id FROM households WHERE id=$1 FOR UPDATE`, actor(domain.Senior).HouseholdID); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	started := make(chan struct{}, 2)
	for n, i := range ids {
		wg.Add(1)
		go func(n int, i domain.Intent) {
			defer wg.Done()
			started <- struct{}{}
			results <- f.s.ReviewApprove(ctx, actor(domain.Reviewer), i.ID, evidences[n], "Reviewed current pattern operands and confirmation", "race-test")
		}(n, i)
	}
	<-started
	<-started
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("expected one reservation, got %d", successes)
	}
	var reserved int
	if err = f.s.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM payment_operations WHERE kind='CAPTURE' AND state='RESERVED'`).Scan(&reserved); err != nil || reserved != 1 {
		t.Fatal(reserved, err)
	}
}

func TestP04PrivateOutageKeepsRulesAndIndependentDenial(t *testing.T) {
	f := setupPayments(t)
	ctx := context.Background()
	now := f.clock.now
	f.clock.now = now.Add(-time.Hour)
	_ = p04Request(t, f, "180.00", true, false)
	f.clock.now = now
	current := p04Request(t, f, "900.00", true, true)
	p03Engine(t, f) // deterministic public service; no private mapping exists
	if _, err := f.s.Analyze(ctx, actor(domain.Reviewer), current.ID, 1); err != nil {
		t.Fatal(err)
	}
	packet, err := f.s.Evidence(ctx, actor(domain.Reviewer), current.ID)
	if err != nil {
		t.Fatal(err)
	}
	if packet.History.Status != domain.LookupUnavailable || packet.History.Totals.Requested != 108000 || !patternAttention(packet.History) || len(packet.History.Matches) != 0 {
		t.Fatal(packet.History)
	}
	task, answer := taskAnswer(t, f, current)
	answer.Response = "DENIED"
	if err = f.s.RespondVerification(ctx, actor(domain.Subject), task.ID, answer, "private-outage-denial"); err != nil {
		t.Fatal(err)
	}
	f.work(t)
	if f.status(t, current.ID) != domain.Voided || f.p.calls["CAPTURE"] != 0 {
		t.Fatal("private outage prevented denial/void")
	}
}
