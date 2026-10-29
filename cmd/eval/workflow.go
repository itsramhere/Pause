package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/ramst/pause/internal/app"
	"github.com/ramst/pause/internal/domain"
	"github.com/ramst/pause/internal/investigation"
	"os"
	"time"
)

type workflowCase struct {
	ID       string `json:"id"`
	Sequence string `json:"sequence"`
	Step     int    `json:"step"`
	Amount   int64  `json:"amount_minor"`
	Purpose  string `json:"purpose"`
	Text     string `json:"text"`
	Language string `json:"language"`
}

func workflow(ctx context.Context, s *app.Service, output string) error {
	raw, err := os.ReadFile("tests/fixtures/p06-workflow.json")
	if err != nil {
		return err
	}
	manifest, err := os.ReadFile("tests/fixtures/p06-workflow.manifest.json")
	if err != nil {
		return err
	}
	var meta struct {
		Hash  string `json:"sha256"`
		Count int    `json:"count"`
	}
	var cases []workflowCase
	if json.Unmarshal(manifest, &meta) != nil || meta.Hash != domain.Hash(raw) || json.Unmarshal(raw, &cases) != nil || len(cases) != 40 || meta.Count != 40 {
		return errors.New("frozen inputs mismatch")
	}
	histories := map[string][]domain.TimelineRow{}
	rows := []any{}
	for _, c := range cases {
		prior := histories[c.Sequence]
		if c.Step != len(prior)+1 {
			return errors.New("noncausal sequence")
		}
		timeline := append(append([]domain.TimelineRow{}, prior...), domain.TimelineRow{IntentID: c.ID, Amount: c.Amount, Purpose: c.Purpose, Current: true, Status: "DRAFT"})
		totals, rules := domain.CalculateHistory(timeline, domain.DefaultPatternThresholds)
		reasons := []string{}
		for _, r := range rules {
			if r.Matched {
				reasons = append(reasons, r.Reason)
			}
		}
		in := domain.InferenceInput{IntentID: uuid.NewString(), IntentVersion: 1, ContentDigest: domain.Hash([]byte(c.Text)), Text: c.Text, Language: c.Language, ModelVersion: s.Investigation.Classifier.ModelVersion}
		classification, ce := s.Investigation.Classify(ctx, in)
		result, receipt, se := s.Investigation.Retrieve(ctx, c.Text)
		signals := append([]string{}, classification.TriggeredLabels...)
		status := string(classification.Status)
		if ce != nil {
			status = "UNAVAILABLE"
		}
		lookup := "OK"
		if se != nil {
			lookup = "UNAVAILABLE"
		}
		sources := []string{}
		for _, h := range result.Hits {
			sources = append(sources, h.SourceID)
		}
		// Every variant retains the same independent-answer/human payment gate.
		// This evaluator performs no payment operations or invented answers.
		variants := map[string]any{}
		for _, v := range []string{"rules", "rules_classifier", "full"} {
			count := len(reasons)
			labels := []string{}
			refs := []string{}
			if v != "rules" {
				labels = signals
				count += len(signals)
				if status != "OK" {
					count++
				}
			}
			if v == "full" {
				refs = sources
			}
			variants[v] = map[string]any{"rule_reasons": reasons, "signal_labels": labels, "attention_reason_count": count, "source_ids": refs, "human_approval_required": true, "authenticated_answer_present": false, "payment_release_performed": false, "evidence_components_present": map[string]bool{"causal_authoritative_inputs": true, "classifier": v != "rules" && ce == nil, "public_retrieval": v == "full" && se == nil, "authenticated_answer": false}}
		}
		rows = append(rows, map[string]any{"id": c.ID, "step": c.Step, "prior_rows": len(prior), "totals": totals, "variants": variants, "classifier_status": status, "lookup_status": lookup, "retrieval_ms": receipt.DurationMS})
		timeline[len(timeline)-1].Current = false
		histories[c.Sequence] = timeline
	}
	report := map[string]any{"schema_version": "pause-workflow-ablation-v1", "denominator": 40, "fixture_sha256": meta.Hash, "classifier": s.Investigation.Classifier, "encoder": s.Investigation.Encoder, "policy": domain.PatternPolicy, "fusion": investigation.FusionVersion, "template": domain.HistoryTemplate, "rows": rows, "recorded_at": time.Now().UTC(), "scope": "Causal authored synthetic evidence/reason ablation; no real payment or authenticated answer is fabricated; PG/payment authority is evaluated by separate executable 40-case catalog. No scam prediction accuracy claim."}
	f, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	e := json.NewEncoder(f)
	e.SetIndent("", "  ")
	return e.Encode(report)
}
