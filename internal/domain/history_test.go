package domain

import "testing"

func TestP04PatternBoundariesAndBuckets(t *testing.T) {
	for _, c := range []struct {
		name                     string
		previous, current, total int64
		escalates, cumulative    bool
	}{{"exact1000", 4000, 96000, 100000, true, false}, {"over1000", 4000, 96001, 100001, true, true}, {"exact3and100", 3333, 10000, 13333, true, false}, {"below100", 3000, 9999, 12999, false, false}, {"below3", 4000, 11999, 15999, false, false}, {"exact3", 4000, 12000, 16000, true, false}} {
		t.Run(c.name, func(t *testing.T) {
			totals, rules := CalculateHistory([]TimelineRow{{IntentID: "prior", Amount: c.previous, Status: "VOIDED"}, {IntentID: "current", Amount: c.current, Current: true, Status: "HELD"}}, DefaultPatternThresholds)
			if totals.Requested != c.total || totals.Held != c.current || totals.Captured != 0 || rules[0].Matched != c.cumulative || rules[1].Matched != c.escalates {
				t.Fatal(totals, rules)
			}
		})
	}
	totals, rules := CalculateHistory([]TimelineRow{{Amount: 90000, Current: true, Status: "CAPTURE_PENDING"}}, DefaultPatternThresholds)
	if rules[1].Applicable || rules[1].Matched || totals.Held != 0 || totals.Captured != 0 || totals.Pending != 90000 {
		t.Fatal(totals, rules)
	}
	totals, _ = CalculateHistory([]TimelineRow{{Amount: 4000, Status: "CAPTURED"}, {Amount: 18000, Status: "VOIDED"}, {Amount: 90000, Status: "HELD", Current: true}, {Amount: 1000, Status: "SEEDED_CAPTURED"}, {Amount: 2000, Status: "RECONCILING"}}, DefaultPatternThresholds)
	if totals.Requested != 115000 || totals.Captured != 4000 || totals.Held != 90000 || totals.SeededCaptured != 1000 || totals.Unknown != 2000 {
		t.Fatal(totals)
	}
	_, rules = CalculateHistory([]TimelineRow{{Amount: 0}, {Amount: 10000, Current: true}}, DefaultPatternThresholds)
	if rules[1].Applicable {
		t.Fatal("zero prior amount must not divide")
	}
}
