package app

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/ramst/pause/internal/domain"
)

func TestRequestStoryDemo(t *testing.T) {
	for _, scenario := range []string{"stopped", "positive"} {
		t.Run(scenario, func(t *testing.T) {
			f := setupPayments(t)
			f.s.Config.DemoEnabled = true
			f.s.Config.Environment = "test"
			f.s.Investigation = nil
			ctx := context.Background()
			run, err := f.s.LaunchDemo(ctx, scenario, "story-test")
			if err != nil {
				t.Fatal(err)
			}
			senior := demoActor(t, f, run, domain.Senior)
			subject := demoActor(t, f, run, domain.Subject)
			reviewer := demoActor(t, f, run, domain.Reviewer)
			tasks, err := f.s.Tasks(ctx, subject)
			if err != nil || len(tasks) != 0 {
				t.Fatal("seed tasks", tasks, err)
			}
			amount, total, previous := "900.00", int64(112000), int64(18000)
			if scenario == "positive" {
				amount, total, previous = "1.00", 5600, 3000
			}
			var contact string
			if err = f.s.DB.Pool.QueryRow(ctx, `SELECT id::text FROM contacts WHERE household_id=$1`, senior.HouseholdID).Scan(&contact); err != nil {
				t.Fatal(err)
			}
			yes := true
			i, _, err := f.s.Create(ctx, senior, Intake{Amount: amount, Currency: "USD", MerchantID: f.s.Config.MerchantID, SubjectContactID: contact, Purpose: "Synthetic judge purchase", Message: "Planned purchase; please confirm with me.", HistoryConsent: &yes}, uuid.NewString(), "test")
			if err != nil {
				t.Fatal(err)
			}
			for _, a := range []domain.Actor{senior, reviewer} {
				story, err := f.s.RequestStory(ctx, a, i.ID)
				if err != nil {
					t.Fatal(err)
				}
				if !story.HistoryEnabled || len(story.Rows) != 3 || len(story.Rules) != 2 || story.Totals.Requested != total || story.Attention != (scenario == "stopped") {
					t.Fatalf("story: %+v", story)
				}
				if !story.Rows[0].Time.Before(story.Rows[1].Time) || !story.Rows[1].Time.Before(story.Rows[2].Time) || !story.Rows[2].Current || story.Rows[1].Amount != previous {
					t.Fatal("timeline", story.Rows)
				}
				for _, rule := range story.Rules {
					if rule.Matched != (scenario == "stopped") {
						t.Fatal("rule", rule)
					}
				}
			}
			tasks, err = f.s.Tasks(ctx, subject)
			if err != nil || len(tasks) != 1 || tasks[0].IntentID != i.ID {
				t.Fatal("real task", tasks, err)
			}
			if _, err = f.s.RequestStory(ctx, subject, i.ID); err == nil {
				t.Fatal("subject allowed")
			}
			for _, role := range []domain.Role{domain.Senior, domain.Reviewer} {
				if _, err = f.s.RequestStory(ctx, actor(role), i.ID); err == nil || err.Error() != "NOT_FOUND" {
					t.Fatal("foreign access", err)
				}
			}
			no := false
			if _, err = f.s.ChangeConsent(ctx, senior, i.ID, ConsentChange{ExpectedVersion: 1, History: &no}, "test"); err != nil {
				t.Fatal(err)
			}
			story, err := f.s.RequestStory(ctx, senior, i.ID)
			if err != nil || story.HistoryEnabled {
				t.Fatal("consent off", story, err)
			}
			for _, row := range story.Rows {
				if !row.Current {
					t.Fatal("earlier row with consent off", row)
				}
			}
		})
	}
}
