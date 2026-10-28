package domain

import "time"

const PatternPolicy = "pause-pattern-policy-v1"
const HistoryTemplate = "private-history-template-v1"

type TimelineRow struct {
	IntentID       string     `json:"intent_id"`
	Version        int        `json:"version"`
	ConsentVersion int        `json:"consent_version"`
	Revision       int        `json:"projection_revision"`
	Time           time.Time  `json:"event_time"`
	Amount         int64      `json:"amount_minor"`
	Purpose        string     `json:"purpose"`
	Merchant       string     `json:"merchant_id"`
	Status         string     `json:"status"`
	Response       string     `json:"subject_response"`
	ResponseSource string     `json:"response_source"`
	ResponseAt     *time.Time `json:"response_at"`
	Source         string     `json:"source"`
	OutcomeSource  string     `json:"outcome_source"`
	Current        bool       `json:"current"`
}
type HistoryTotals struct {
	Requested      int64 `json:"requested_minor"`
	Held           int64 `json:"held_minor"`
	Captured       int64 `json:"captured_minor"`
	Pending        int64 `json:"pending_minor"`
	Unknown        int64 `json:"unknown_minor"`
	SeededCaptured int64 `json:"seeded_captured_minor"`
}
type PatternRule struct {
	Reason     string           `json:"reason"`
	Matched    bool             `json:"matched"`
	Applicable bool             `json:"applicable"`
	Operands   map[string]int64 `json:"operands"`
	Thresholds map[string]int64 `json:"thresholds"`
	IDs        []string         `json:"matched_request_ids"`
	Policy     string           `json:"policy_version"`
}

// Prototype thresholds are explicit decision-support policy, never probabilities.
type PatternThresholds struct{ Requested, Minimum, Multiplier int64 }

var DefaultPatternThresholds = PatternThresholds{Requested: 100000, Minimum: 10000, Multiplier: 3}

func CalculateHistory(rows []TimelineRow, thresholds PatternThresholds) (HistoryTotals, []PatternRule) {
	var totals HistoryTotals
	ids := []string{}
	var current, previous *TimelineRow
	for n := range rows {
		r := &rows[n]
		totals.Requested += r.Amount
		ids = append(ids, r.IntentID)
		switch r.Status {
		case "HELD":
			totals.Held += r.Amount
		case "CAPTURED":
			totals.Captured += r.Amount
		case "CAPTURE_PENDING", "VOID_PENDING":
			totals.Pending += r.Amount
		case "RECONCILING":
			totals.Unknown += r.Amount
		case "SEEDED_CAPTURED":
			totals.SeededCaptured += r.Amount
		}
		if r.Current {
			current = r
			if n > 0 {
				previous = &rows[n-1]
			}
		}
	}
	rules := []PatternRule{{Reason: "SEVEN_DAY_REQUESTED_TOTAL", Applicable: true, Matched: totals.Requested > thresholds.Requested, Operands: map[string]int64{"requested_minor": totals.Requested}, Thresholds: map[string]int64{"strictly_above_minor": thresholds.Requested}, IDs: ids, Policy: PatternPolicy}}
	escalation := PatternRule{Reason: "CURRENT_AMOUNT_ESCALATION", Applicable: current != nil && previous != nil && previous.Amount > 0, Operands: map[string]int64{}, Thresholds: map[string]int64{"minimum_minor": thresholds.Minimum, "multiplier": thresholds.Multiplier}, IDs: []string{}, Policy: PatternPolicy}
	if escalation.Applicable {
		escalation.Operands = map[string]int64{"current_minor": current.Amount, "previous_minor": previous.Amount}
		escalation.IDs = []string{previous.IntentID, current.IntentID}
		// Division avoids multiplying untrusted aggregate values or rounding ratios.
		escalation.Matched = current.Amount >= thresholds.Minimum && current.Amount/previous.Amount >= thresholds.Multiplier
	}
	return totals, append(rules, escalation)
}
