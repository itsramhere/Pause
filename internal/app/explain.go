package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/ramst/pause/internal/domain"
	"github.com/ramst/pause/internal/llm"
)

type ExplanationDTO struct {
	Source        string         `json:"source"`
	Model         string         `json:"model,omitempty"`
	PromptVersion string         `json:"prompt_version,omitempty"`
	Sentences     []llm.Sentence `json:"sentences"`
	Facts         []llm.Fact     `json:"facts"`
	ReasonCode    string         `json:"reason_code,omitempty"`
}

func explanationMoney(minor int64) string {
	whole := strconv.FormatInt(minor/100, 10)
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + "," + whole[i:]
	}
	return fmt.Sprintf("$%s.%02d", whole, minor%100)
}

// Match Number((current / previous).toFixed(1)) in RequestStory, including
// exact half ties. toFixed rounds the binary floating-point value half up.
func explanationRatio(current, previous int64) string {
	ratio := new(big.Rat).SetFloat64(float64(current) / float64(previous))
	ratio.Mul(ratio, big.NewRat(10, 1))
	whole, remainder := new(big.Int), new(big.Int)
	whole.QuoRem(ratio.Num(), ratio.Denom(), remainder)
	if remainder.Lsh(remainder, 1).Cmp(ratio.Denom()) >= 0 {
		whole.Add(whole, big.NewInt(1))
	}
	tenths := whole.Int64()
	return strings.TrimSuffix(fmt.Sprintf("%d.%d", tenths/10, tenths%10), ".0")
}

func explanationFacts(stored map[string]any, subjectName string) []llm.Fact {
	var e struct {
		Binding      domain.Binding `json:"binding"`
		Merchant     string         `json:"merchant_label"`
		Verification struct {
			Status string `json:"status"`
		} `json:"verification"`
		Provider struct {
			Status string `json:"status"`
		} `json:"provider"`
		Classifier string      `json:"classifier_status"`
		Signals    []string    `json:"signals"`
		LLM        llm.Signals `json:"llm"`
		Sources    []struct {
			Title string `json:"title"`
		} `json:"sources"`
		History HistoryPacket `json:"private_history"`
		Missing []string      `json:"missing_checks"`
	}
	_ = json.Unmarshal(mustJSON(stored), &e)
	facts := []llm.Fact{}
	add := func(id, label, text string) {
		facts = append(facts, llm.Fact{ID: id, Label: label, Text: domain.RedactPrivateAddresses(text)})
	}
	add("F1", "Purchase", fmt.Sprintf("Purchase: %s to %s for “%s”.", explanationMoney(e.Binding.AmountMinor), e.Merchant, e.Binding.Purpose))
	answer := subjectName + " has not answered yet."
	switch e.Verification.Status {
	case "CONFIRMED":
		answer = subjectName + " confirmed this exact purchase."
	case "DENIED":
		answer = subjectName + " said they did not request this purchase."
	case "EXPIRED":
		answer = "The answer window for " + subjectName + " has ended."
	}
	add("F2", "Answer", answer)
	if e.Classifier == "OK" || e.Classifier == "ABSTAIN" {
		signals := []string{}
		for _, s := range e.Signals {
			wording := strings.TrimSuffix(strings.TrimPrefix(s, "Signal detected: "), ".")
			// Only canonical classifier label names become wording facts.
			for _, label := range domain.Labels {
				if wording == strings.ReplaceAll(label, "_", " ") {
					signals = append(signals, wording)
					break
				}
			}
		}
		text := "The classifier found no listed wording signals."
		if len(signals) > 0 {
			text = "Classifier wording signals: " + strings.Join(signals, ", ") + "."
		}
		add("F3", "Wording", text)
	}
	if e.LLM.Status == llm.StatusOK {
		labels := []string{}
		for _, l := range e.LLM.Labels {
			for _, known := range domain.Labels {
				if l.Label == known {
					labels = append(labels, strings.ReplaceAll(l.Label, "_", " "))
					break
				}
			}
		}
		text := "The AI reading flagged no pressure signals."
		if len(labels) > 0 {
			text = "The AI reading flagged: " + strings.Join(labels, ", ") + "."
		}
		add("F4", "AI reading", text)
	}
	if len(e.Sources) > 0 {
		titles := []string{}
		for _, source := range e.Sources {
			if source.Title != "" {
				titles = append(titles, source.Title)
			}
			if len(titles) == 2 {
				break
			}
		}
		if len(titles) > 0 {
			add("F5", "Patterns", "Similar documented scam patterns: "+strings.Join(titles, "; ")+".")
		}
	}
	history := "Related-request history is off for this request."
	if e.History.Status != domain.LookupNotApplicable && e.History.Status != "" {
		history = fmt.Sprintf("%s requested across %d requests in 7 days.", explanationMoney(e.History.Totals.Requested), len(e.History.Rows))
		for _, rule := range e.History.Rules {
			if !rule.Matched {
				continue
			}
			switch rule.Reason {
			case "SEVEN_DAY_REQUESTED_TOTAL":
				history += " That is above the " + explanationMoney(rule.Thresholds["strictly_above_minor"]) + " review line."
			case "CURRENT_AMOUNT_ESCALATION":
				if previous := rule.Operands["previous_minor"]; previous > 0 {
					history += " This request is " + explanationRatio(rule.Operands["current_minor"], previous) + "\u00d7 the previous one."
				}
			}
		}
	}
	add("F6", "History", history)
	checks := "All required checks are present."
	if e.Verification.Status == "DENIED" {
		checks = "Release is blocked because of this answer."
	} else if len(e.Missing) > 0 {
		plain := make([]string, 0, len(e.Missing))
		for _, missing := range e.Missing {
			text := "another required check"
			switch missing {
			case "Usable approved classification":
				text = "the message check"
			case "Successful approved public lookup":
				text = "the scam-pattern lookup"
			case "Successful current private history lookup (run message checks to refresh)":
				text = "the related-request lookup"
			case "Current independent subject confirmation":
				text = "a current yes from " + subjectName
			}
			plain = append(plain, text)
		}
		joined := plain[0]
		if len(plain) > 1 {
			joined = strings.Join(plain[:len(plain)-1], ", ") + " and " + plain[len(plain)-1]
		}
		checks = "Still needed before any release: " + joined + "."
	}
	add("F7", "Checks", checks)
	payment := ""
	switch e.Provider.Status {
	case "DRAFT", "AWAITING_PAYER":
		payment = "The PayPal approval has not finished yet; no money has been taken."
	case "HELD":
		payment = "PayPal is holding the money; nothing has been taken yet."
	case "CAPTURE_PENDING":
		payment = "Pause has asked PayPal to complete the payment."
	case "CAPTURED":
		payment = "PayPal confirmed the payment was completed."
	case "VOID_PENDING":
		payment = "Pause has asked PayPal to cancel the hold."
	case "VOIDED", "CLOSED_UNCAPTURED":
		payment = "PayPal confirmed the hold was cancelled; no money was taken."
	case "RECONCILING":
		payment = "Pause is checking the final result with PayPal."
	case "ABANDONED":
		payment = "This request was left before PayPal approval; no money was taken."
	}
	if payment != "" {
		add("F8", "Payment", payment)
	}
	return facts
}

func templateExplanation(facts []llm.Fact) []llm.Sentence {
	byID := map[string]string{}
	for _, f := range facts {
		byID[f.ID] = f.Text
	}
	out := []llm.Sentence{}
	add := func(id, text, neutral string) {
		sentence := llm.Sentence{Text: text, FactIDs: []string{id}}
		// User-controlled names can contain verdict words or exceed the prose limit.
		// Keep the template valid without repeating that unsafe display wording.
		raw := mustJSON(map[string]any{"sentences": []llm.Sentence{sentence, sentence}})
		if _, err := llm.ValidateExplanation(raw, facts); err != nil {
			sentence.Text = neutral
		}
		out = append(out, sentence)
	}
	purchase := strings.TrimPrefix(byID["F1"], "Purchase: ")
	purchase, _, _ = strings.Cut(purchase, " for “")
	amount, merchant, _ := strings.Cut(purchase, " to ")
	add("F1", "Pause is holding a "+amount+" purchase to "+merchant+".", "Pause is holding this purchase.")
	add("F2", byID["F2"], "The contact answer is shown in the checks below.")
	history := byID["F6"]
	if _, rules, ok := strings.Cut(history, " days. "); ok {
		// Use the first matched rule as one short sentence.
		rule, _, _ := strings.Cut(rules, ". ")
		if !strings.HasSuffix(rule, ".") {
			rule += "."
		}
		add("F6", rule, "Related requests reached a review line.")
	} else {
		id := ""
		pressure := false
		for _, candidate := range []string{"F3", "F4"} {
			text, ok := byID[candidate]
			if !ok {
				continue
			}
			if id == "" {
				id = candidate
			}
			if strings.HasPrefix(text, "Classifier wording signals:") || strings.HasPrefix(text, "The AI reading flagged:") {
				id = candidate
				pressure = true
				break
			}
		}
		if id != "" {
			text := "No listed pressure signals were found in the wording."
			if pressure {
				text = "The message wording shows pressure signals."
			}
			add(id, text, "The wording checks are shown below.")
		} else {
			add("F7", "The required checks are listed below.", "The required checks are listed below.")
		}
	}
	checks := ""
	if strings.HasPrefix(byID["F7"], "Still needed") {
		checks = "Some required checks are still needed."
	} else if byID["F7"] == "Release is blocked because of this answer." {
		checks = byID["F7"]
	}
	payment := byID["F8"]
	// Keep four sentences: payment progress takes priority outside HELD;
	// while held, explain any outstanding checks or the blocking answer.
	if payment != "" && (payment != "PayPal is holding the money; nothing has been taken yet." || checks == "") {
		add("F8", payment, "The payment result is shown in the checks below.")
	} else if checks != "" {
		add("F7", checks, checks)
	}
	return out
}

// Explanation is derived display. It is NOT part of the evidence hash, and
// release never reads it. The network call happens after the read transaction.
func (s *Service) Explanation(ctx context.Context, a domain.Actor, intentID, evidenceID string) (ExplanationDTO, error) {
	var out ExplanationDTO
	intentID, err := canonicalID(intentID)
	if err != nil {
		return out, err
	}
	evidenceID, err = canonicalID(evidenceID)
	if err != nil {
		return out, err
	}
	var stored map[string]any
	var name string
	err = s.DB.WithTx(ctx, func(tx pgx.Tx) error {
		var p paymentRow
		if e := tx.QueryRow(ctx, `SELECT household_id::text,creator_id::text,reviewer_id::text FROM payment_intents WHERE id=$1`, intentID).Scan(&p.household, &p.creator, &p.reviewer); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return domain.Fail("NOT_FOUND", 404)
			}
			return e
		}
		if e := s.paymentActor(ctx, tx, a, p, false); e != nil {
			return e
		}
		var raw []byte
		if e := tx.QueryRow(ctx, `SELECT evidence FROM evidence_snapshots WHERE id=$1 AND intent_id=$2`, evidenceID, intentID).Scan(&raw); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return domain.Fail("NOT_FOUND", 404)
			}
			return e
		}
		if e := json.Unmarshal(raw, &stored); e != nil {
			return e
		}
		var binding domain.Binding
		_ = json.Unmarshal(mustJSON(stored["binding"]), &binding)
		return tx.QueryRow(ctx, `SELECT u.display_name FROM contacts c JOIN users u ON u.id=c.user_id WHERE c.id=$1 AND c.household_id=$2`, binding.SubjectContactID, p.household).Scan(&name)
	})
	if err != nil {
		return out, err
	}
	facts := explanationFacts(stored, name)
	out = ExplanationDTO{Source: "TEMPLATE", Facts: facts, Sentences: templateExplanation(facts), ReasonCode: "LLM_DISABLED"}
	if s.LLM == nil {
		return out, nil
	}
	model := s.LLM.Model()
	key := domain.Hash(append(mustJSON(facts), []byte(model+llm.ExplainPromptVersion+intentID)...))
	var cached []byte
	err = s.DB.Pool.QueryRow(ctx, `SELECT body FROM evidence_explanations WHERE facts_hash=$1 AND intent_id=$2`, key, intentID).Scan(&cached)
	if err == nil {
		var hit ExplanationDTO
		if json.Unmarshal(cached, &hit) == nil {
			return hit, nil
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		out.ReasonCode = string(llm.StatusUnavailable)
		return out, nil
	}
	callCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	result, callErr := s.LLM.Explain(callCtx, facts)
	cancel()
	out.ReasonCode = string(result.Status)
	if callErr != nil || result.Status != llm.StatusOK {
		return out, nil
	}
	ai := ExplanationDTO{Source: "AI", Model: result.Model, PromptVersion: result.PromptVersion, Sentences: result.Sentences, Facts: facts}
	_, err = s.DB.Pool.Exec(ctx, `INSERT INTO evidence_explanations(facts_hash,intent_id,body,model,prompt_version,created_at) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, key, intentID, mustJSON(ai), result.Model, result.PromptVersion, s.Clock.Now())
	if err != nil {
		out.ReasonCode = string(llm.StatusUnavailable)
		return out, nil
	}
	return ai, nil
}
