package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/ramst/pause/internal/domain"
	"github.com/ramst/pause/internal/llm"
)

func TestExplanationFactsAndTemplates(t *testing.T) {
	for n := 0; n < 400; n++ {
		states := []string{"PENDING", "NOT_REQUESTED", "CONFIRMED", "DENIED", "EXPIRED"}
		classifier := []string{"OK", "ABSTAIN", "UNAVAILABLE", "UNSUPPORTED"}[n%4]
		history := HistoryPacket{Status: domain.LookupNotApplicable}
		if n%2 == 0 {
			rows := []domain.TimelineRow{{Amount: 4000}, {Amount: 18000}, {Amount: 90000, Current: true}}
			totals, rules := domain.CalculateHistory(rows, domain.DefaultPatternThresholds)
			history = HistoryPacket{Status: domain.LookupUnavailable, Rows: rows, Totals: totals, Rules: rules}
		}
		flags := []llm.LabelFlag{}
		signals := []string{}
		if n%3 == 0 {
			flags = append(flags, llm.LabelFlag{Label: "urgent_payment_pressure", Excerpt: "PRIVATE EXCERPT"})
			signals = append(signals, "Signal detected: urgent payment pressure.")
		}
		missing := []string{}
		if n%2 == 0 {
			missing = append(missing, "Current independent subject confirmation")
		}
		merchant := []string{"Store", "Safe Store", strings.Repeat("m", 240), "Store https://example.test"}[n%4]
		name := []string{"Alex", "Confirmed Safe", strings.Repeat("n", 240), "Alex alex@example.test"}[n%4]
		stored := map[string]any{"binding": domain.Binding{AmountMinor: 90000, Purpose: "Supplies https://private.test help@example.test"}, "merchant_label": merchant, "verification": map[string]any{"status": states[n%5]}, "classifier_status": classifier, "signals": signals, "llm": llm.Signals{Status: []llm.Status{llm.StatusOK, llm.StatusUnavailable}[n%2], Labels: flags}, "private_history": history, "missing_checks": missing, "sources": []any{map[string]any{"title": "First https://private.test"}, map[string]any{"title": "Second"}, map[string]any{"title": "Third"}}}
		payments := []string{"DRAFT", "AWAITING_PAYER", "HELD", "CAPTURE_PENDING", "CAPTURED", "VOID_PENDING", "VOIDED", "CLOSED_UNCAPTURED", "RECONCILING", "ABANDONED", "UNKNOWN", ""}
		stored["provider"] = map[string]any{"status": payments[n%len(payments)]}
		// Round trip matches the stored JSON map contract.
		var decoded map[string]any
		_ = json.Unmarshal(mustJSON(stored), &decoded)
		facts := explanationFacts(decoded, name)
		raw := mustJSON(map[string]any{"sentences": templateExplanation(facts)})
		if _, err := llm.ValidateExplanation(raw, facts); err != nil {
			t.Fatalf("set %d: %s: %v", n, raw, err)
		}
		for _, fact := range facts {
			if strings.Contains(fact.Text, "private.test") || strings.Contains(fact.Text, "example.test") || strings.Contains(fact.Text, "PRIVATE EXCERPT") || strings.Contains(fact.Text, "Third") {
				t.Fatal("private or extra data", fact)
			}
		}
		byID := map[string]string{}
		for _, fact := range facts {
			byID[fact.ID] = fact.Text
		}
		if (byID["F3"] != "") != (classifier == "OK" || classifier == "ABSTAIN") || (byID["F4"] != "") != (n%2 == 0) {
			t.Fatal("status not respected", facts)
		}
		if !strings.Contains(byID["F1"], "$900.00") {
			t.Fatal(facts)
		}
		if n%2 == 0 && (!strings.Contains(byID["F6"], "$1,120.00") || !strings.Contains(byID["F6"], "This request is 5× the previous one.") || !strings.Contains(byID["F6"], "That is above the $1,000.00 review line.")) {
			t.Fatal(facts)
		}
	}
}

func TestExplanationCacheAndFailures(t *testing.T) {
	f := setupPayments(t)
	i := f.held(t)
	p03Engine(t, f)
	packet := answeredEvidence(t, f, i)
	ctx := context.Background()
	// Seed identical facts and model under an older prompt version to test
	// prompt-version cache isolation independently of changes to the facts.
	var raw []byte
	if err := f.s.DB.Pool.QueryRow(ctx, `SELECT evidence FROM evidence_snapshots WHERE id=$1`, packet.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	intent, err := f.s.Read(ctx, actor(domain.Senior), i.ID)
	if err != nil {
		t.Fatal(err)
	}
	facts := explanationFacts(stored, intent.SubjectName)
	oldKey := domain.Hash(append(mustJSON(facts), []byte("fixture-valid"+"pause-explain-prompt-v1"+i.ID)...))
	legacy := ExplanationDTO{Source: "AI", Model: "fixture-valid", PromptVersion: "pause-explain-prompt-v1", Facts: facts, Sentences: templateExplanation(facts)}
	if _, err := f.s.DB.Pool.Exec(ctx, `INSERT INTO evidence_explanations(facts_hash,intent_id,body,model,prompt_version,created_at) VALUES($1,$2,$3,$4,$5,$6)`, oldKey, i.ID, mustJSON(legacy), legacy.Model, legacy.PromptVersion, f.clock.Now()); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	mode := "valid"
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if mode == "outage" {
			w.WriteHeader(503)
			return
		}
		var request struct {
			Input string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		data := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(request.Input, llm.MessageStart), llm.MessageEnd))
		var facts []llm.Fact
		_ = json.Unmarshal([]byte(data), &facts)
		sentences := templateExplanation(facts)
		if mode == "invented" {
			sentences[0].Text = "Pause is holding a $950.00 purchase."
		}
		answer := string(mustJSON(map[string]any{"sentences": sentences}))
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "completed", "steps": []any{map[string]any{"type": "model_output", "content": []any{map[string]any{"type": "text", "text": answer}}}}})
	}))
	defer fake.Close()
	for _, tc := range []struct{ mode, source, reason string }{{"valid", "AI", ""}, {"invented", "TEMPLATE", "INVALID_OUTPUT"}, {"outage", "TEMPLATE", "UNAVAILABLE"}, {"disabled", "TEMPLATE", "LLM_DISABLED"}} {
		mode = tc.mode
		f.s.LLM = llm.New("fixture-key", "fixture-"+mode, fake.URL, "")
		if mode == "disabled" {
			f.s.LLM = nil
		}
		before := calls.Load()
		out, err := f.s.Explanation(ctx, actor(domain.Senior), i.ID, packet.ID)
		if err != nil || out.Source != tc.source || out.ReasonCode != tc.reason {
			t.Fatal(mode, out, err)
		}
		if out.Source == "AI" && out.PromptVersion != llm.ExplainPromptVersion {
			t.Fatal("reused an older prompt version", out)
		}
		if _, err := llm.ValidateExplanation(mustJSON(map[string]any{"sentences": out.Sentences}), out.Facts); err != nil {
			t.Fatal(err)
		}
		again, err := f.s.Explanation(ctx, actor(domain.Reviewer), i.ID, packet.ID)
		if err != nil || again.Source != tc.source {
			t.Fatal(again, err)
		}
		want := int32(2)
		if mode == "valid" {
			want = 1
		}
		if mode == "disabled" {
			want = 0
		}
		if calls.Load()-before != want {
			t.Fatal("cache calls", mode, calls.Load()-before, want)
		}
		var count int
		if err := f.s.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM evidence_explanations WHERE intent_id=$1`, i.ID).Scan(&count); err != nil || count != 2 {
			t.Fatal("only AI cached", count, err)
		}
	}
	// A new evidence snapshot with identical facts must reuse the first cache entry.
	f.s.LLM = llm.New("fixture-key", "fixture-valid", fake.URL, "")
	mode = "valid"
	fresh, err := f.s.Evidence(ctx, actor(domain.Senior), i.ID)
	if err != nil {
		t.Fatal(err)
	}
	before := calls.Load()
	out, err := f.s.Explanation(ctx, actor(domain.Senior), i.ID, fresh.ID)
	if err != nil || out.Source != "AI" || calls.Load() != before || fresh.ID == packet.ID {
		t.Fatal("stable facts cache", out, err)
	}
	other := f.held(t)
	for _, tc := range []struct {
		actor            domain.Actor
		intent, evidence string
	}{{actor(domain.Subject), i.ID, packet.ID}, {actor(domain.Senior), i.ID, "bad"}, {actor(domain.Senior), i.ID, uuid.NewString()}, {actor(domain.Senior), other.ID, packet.ID}} {
		if _, err := f.s.Explanation(ctx, tc.actor, tc.intent, tc.evidence); err == nil || err.Error() != "NOT_FOUND" {
			t.Fatal("access", err)
		}
	}
}

func TestExplanationStoppedDemoFacts(t *testing.T) {
	f := setupPayments(t)
	p03Engine(t, f)
	f.s.Config.DemoEnabled = true
	f.s.Config.Environment = "test"
	ctx := context.Background()
	run, err := f.s.LaunchDemo(ctx, "stopped", "explain-test")
	if err != nil {
		t.Fatal(err)
	}
	senior, subject := demoActor(t, f, run, domain.Senior), demoActor(t, f, run, domain.Subject)
	var contact string
	if err = f.s.DB.Pool.QueryRow(ctx, `SELECT id::text FROM contacts WHERE household_id=$1`, senior.HouseholdID).Scan(&contact); err != nil {
		t.Fatal(err)
	}
	yes := true
	i, _, err := f.s.Create(ctx, senior, Intake{Amount: "900.00", Currency: "USD", MerchantID: f.s.Config.MerchantID, SubjectContactID: contact, Purpose: "Emergency supplies", Message: "Please confirm this purchase.", HistoryConsent: &yes}, uuid.NewString(), "test")
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := f.s.Tasks(ctx, subject)
	if err != nil || len(tasks) != 1 {
		t.Fatal(tasks, err)
	}
	task := tasks[0]
	if err = f.s.RespondVerification(ctx, subject, task.ID, Answer{ExpectedVersion: task.Version, Digest: task.Digest, SnapshotHash: task.SnapshotHash, Response: "DENIED"}, "test"); err != nil {
		t.Fatal(err)
	}
	packet, err := f.s.Evidence(ctx, senior, i.ID)
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.s.Explanation(ctx, senior, i.ID, packet.ID)
	if err != nil {
		t.Fatal(err)
	}
	all := fmt.Sprint(out.Facts)
	for _, text := range []string{"$900.00", "said they did not request", "$1,120.00", "5×"} {
		if !strings.Contains(all, text) {
			t.Fatal(text, all)
		}
	}
	for _, fact := range out.Facts {
		if fact.ID == "F7" && fact.Text != "Release is blocked because of this answer." {
			t.Fatal(fact)
		}
	}
}

func TestExplanationPlainChecks(t *testing.T) {
	for _, tc := range []struct {
		name, answer string
		missing      []string
		want         string
	}{
		{"none", "PENDING", nil, "All required checks are present."},
		{"message", "PENDING", []string{"Usable approved classification"}, "Still needed before any release: the message check."},
		{"public", "PENDING", []string{"Successful approved public lookup"}, "Still needed before any release: the scam-pattern lookup."},
		{"history", "PENDING", []string{"Successful current private history lookup (run message checks to refresh)"}, "Still needed before any release: the related-request lookup."},
		{"answer", "PENDING", []string{"Current independent subject confirmation"}, "Still needed before any release: a current yes from Maya Chen."},
		{"unknown", "PENDING", []string{"Private implementation detail"}, "Still needed before any release: another required check."},
		{"two", "PENDING", []string{"Usable approved classification", "Current independent subject confirmation"}, "Still needed before any release: the message check and a current yes from Maya Chen."},
		{"three", "PENDING", []string{"Usable approved classification", "Successful approved public lookup", "Current independent subject confirmation"}, "Still needed before any release: the message check, the scam-pattern lookup and a current yes from Maya Chen."},
		{"denied", "DENIED", []string{"Current independent subject confirmation"}, "Release is blocked because of this answer."},
		{"denied without missing", "DENIED", nil, "Release is blocked because of this answer."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facts := explanationFacts(map[string]any{"verification": map[string]any{"status": tc.answer}, "missing_checks": tc.missing}, "Maya Chen")
			for _, fact := range facts {
				if fact.ID == "F7" {
					if fact.Label != "Checks" || fact.Text != tc.want {
						t.Fatal(fact)
					}
					return
				}
			}
			t.Fatal("missing F7")
		})
	}
}

func TestExplanationPaymentFactsAndTemplatePriority(t *testing.T) {
	for _, tc := range []struct{ status, text string }{
		{"DRAFT", "The PayPal approval has not finished yet; no money has been taken."},
		{"AWAITING_PAYER", "The PayPal approval has not finished yet; no money has been taken."},
		{"HELD", "PayPal is holding the money; nothing has been taken yet."},
		{"CAPTURE_PENDING", "Pause has asked PayPal to complete the payment."},
		{"CAPTURED", "PayPal confirmed the payment was completed."},
		{"VOID_PENDING", "Pause has asked PayPal to cancel the hold."},
		{"VOIDED", "PayPal confirmed the hold was cancelled; no money was taken."},
		{"CLOSED_UNCAPTURED", "PayPal confirmed the hold was cancelled; no money was taken."},
		{"RECONCILING", "Pause is checking the final result with PayPal."},
		{"ABANDONED", "This request was left before PayPal approval; no money was taken."},
		{"UNKNOWN", ""}, {"", ""},
	} {
		for _, answer := range []string{"PENDING", "CONFIRMED", "DENIED"} {
			for _, missing := range []bool{true, false} {
				t.Run(fmt.Sprintf("%s/%s/missing=%t", tc.status, answer, missing), func(t *testing.T) {
					checks := []string{}
					if missing {
						checks = append(checks, "Current independent subject confirmation")
					}
					facts := explanationFacts(map[string]any{"binding": domain.Binding{AmountMinor: 90000, Purpose: "Supplies"}, "merchant_label": "Store", "provider": map[string]any{"status": tc.status}, "verification": map[string]any{"status": answer}, "classifier_status": "OK", "missing_checks": checks}, "Maya Chen")
					byID := map[string]llm.Fact{}
					for _, fact := range facts {
						byID[fact.ID] = fact
					}
					if byID["F8"].Text != tc.text || (tc.text != "" && byID["F8"].Label != "Payment") {
						t.Fatal(byID["F8"])
					}
					sentences := templateExplanation(facts)
					if _, err := llm.ValidateExplanation(mustJSON(map[string]any{"sentences": sentences}), facts); err != nil {
						t.Fatal(sentences, err)
					}
					last := sentences[len(sentences)-1]
					wantID := "F3"
					if missing || answer == "DENIED" {
						wantID = "F7"
					}
					if tc.text != "" && (tc.status != "HELD" || (!missing && answer != "DENIED")) {
						wantID = "F8"
					}
					if len(sentences) > 4 || last.FactIDs[0] != wantID {
						t.Fatal("priority", sentences, wantID)
					}
					if wantID == "F8" && last.Text != tc.text {
						t.Fatal(last)
					}
					if wantID == "F7" && answer == "DENIED" && last.Text != "Release is blocked because of this answer." {
						t.Fatal(last)
					}
				})
			}
		}
	}
}

func TestExplanationRatioMatchesStory(t *testing.T) {
	for _, tc := range []struct {
		current, previous int64
		want              string
	}{{90000, 18000, "5"}, {325, 100, "3.3"}, {315, 100, "3.1"}, {100, 30, "3.3"}} {
		if got := explanationRatio(tc.current, tc.previous); got != tc.want {
			t.Fatal(got, tc.want)
		}
	}
}
