package app

import (
	"context"
	"testing"
	"time"

	"github.com/ramst/pause/internal/domain"
)

// A response's release window must expire for open holds without rewriting
// the historical independent answer on an already completed receipt.
func TestP05CompletedReceiptPreservesIndependentAnswerAfterExpiry(t *testing.T) {
	for _, capture := range []bool{false, true} {
		name := "open_hold_expires"
		if capture {
			name = "captured_answer_remains_recorded"
		}
		t.Run(name, func(t *testing.T) {
			f := setupPayments(t)
			ctx := context.Background()
			i := f.held(t)
			p03Engine(t, f)
			task, answer := taskAnswer(t, f, i)
			if err := f.s.RespondVerification(ctx, actor(domain.Subject), task.ID, answer, "test"); err != nil {
				t.Fatal(err)
			}
			if _, err := f.s.Analyze(ctx, actor(domain.Reviewer), i.ID, 1); err != nil {
				t.Fatal(err)
			}
			packet, err := f.s.Evidence(ctx, actor(domain.Reviewer), i.ID)
			if err != nil {
				t.Fatal(err)
			}
			if capture {
				err = f.s.Review(ctx, actor(domain.Reviewer), i.ID, ReviewDecision{ExpectedVersion: 1, EvidenceID: packet.ID, EvidenceHash: packet.Hash, Decision: "APPROVED", Rationale: "Reviewed the exact independent answer and merchant."}, "test")
				if err != nil {
					t.Fatal(err)
				}
				f.work(t)
				if f.status(t, i.ID) != domain.Captured {
					t.Fatal("capture not confirmed")
				}
			}
			f.clock.now = f.clock.now.Add(16 * time.Minute)
			want := "EXPIRED"
			if capture {
				want = "CONFIRMED"
			}
			current, err := f.s.Read(ctx, actor(domain.Senior), i.ID)
			if err != nil || string(current.Verification) != want {
				t.Fatalf("intent answer = %s; want %s; err %v", current.Verification, want, err)
			}
			packet, err = f.s.Evidence(ctx, actor(domain.Reviewer), i.ID)
			if err != nil || packet.Verification.(map[string]any)["status"] != want {
				t.Fatalf("evidence answer = %v; want %s; err %v", packet.Verification, want, err)
			}
			// A historical confirmation never grants fresh release authority.
			if !packet.ReleaseEvidence.Unresolved {
				t.Fatal("expired confirmation became usable release evidence")
			}
			tasks, err := f.s.Tasks(ctx, actor(domain.Subject))
			if err != nil || len(tasks) != 1 || tasks[0].Status != want {
				t.Fatalf("subject inbox = %v; want %s; err %v", tasks, want, err)
			}
			yes := true
			if _, err := f.s.ChangeConsent(ctx, actor(domain.Senior), i.ID, ConsentChange{ExpectedVersion: 1, History: &yes}, "test"); err != nil {
				t.Fatal(err)
			}
			history := p04History(t, f, i.ID)
			if len(history.Rows) != 1 || history.Rows[0].Response != want {
				t.Fatalf("canonical timeline = %v; want %s", history.Rows, want)
			}
		})
	}
}

func TestP05ExpiredConfirmationAllowsOnlyExactOpenPurchaseDenial(t *testing.T) {
	for _, scenario := range []string{"late-denial", "late-confirmation", "changed-binding", "application-expired", "already-closed"} {
		t.Run(scenario, func(t *testing.T) {
			f := setupPayments(t)
			ctx := context.Background()
			i := f.held(t)
			task, answer := taskAnswer(t, f, i)
			answer.Response = "DENIED"
			f.clock.now = task.ExpiresAt
			switch scenario {
			case "late-confirmation":
				answer.Response = "CONFIRMED"
			case "changed-binding":
				answer.ExpectedVersion++
			case "application-expired":
				f.clock.now = i.DecisionDeadline
			case "already-closed":
				if _, err := f.s.Cancel(ctx, actor(domain.Senior), i.ID, i.Version, "test"); err != nil {
					t.Fatal(err)
				}
				f.work(t)
			}
			tasks, err := f.s.Tasks(ctx, actor(domain.Subject))
			wantDenial := scenario != "application-expired" && scenario != "already-closed"
			if err != nil || len(tasks) != 1 || tasks[0].CanDeny != wantDenial {
				t.Fatalf("denial action availability = %v, want %v, err %v", tasks, wantDenial, err)
			}
			err = f.s.RespondVerification(ctx, actor(domain.Subject), task.ID, answer, "test")
			if scenario != "late-denial" {
				if err == nil {
					t.Fatal("stale or positive expired answer accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			f.work(t)
			current, err := f.s.Read(ctx, actor(domain.Senior), i.ID)
			if err != nil || current.Payment != domain.Voided || string(current.Verification) != "DENIED" || f.p.calls["CAPTURE"] != 0 {
				t.Fatalf("late denial did not safely void: %+v, %v", current, err)
			}
		})
	}
}
