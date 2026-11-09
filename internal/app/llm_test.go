package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ramst/pause/internal/domain"
	"github.com/ramst/pause/internal/llm"
)

// llmFake is the Gemini fake in the p03Engine style: one server, one mode
// variable, no retries. It quotes excerpts from the exact redacted user text
// it receives, so grounded flags stay verbatim through client validation.
func llmFake(t *testing.T) (*string, *llm.Client) {
	t.Helper()
	mode := "flags"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/interactions" {
			t.Error("unexpected llm route", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		var body struct {
			Input string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		sent := strings.TrimSpace(body.Input)
		sent = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(sent, llm.MessageStart), llm.MessageEnd))
		if mode == "outage" {
			w.WriteHeader(503)
			return
		}
		answer := map[string]any{}
		for _, l := range domain.Labels {
			answer[l] = map[string]any{"present": false, "excerpt": ""}
		}
		switch mode {
		case "flags":
			answer["urgent_payment_pressure"] = map[string]any{"present": true, "excerpt": firstRunes(sent, 18)}
		case "ungrounded":
			answer["urgent_payment_pressure"] = map[string]any{"present": true, "excerpt": "An excerpt the model invented"}
		case "none":
		default:
			t.Error("unknown llm fake mode", mode)
			w.WriteHeader(500)
			return
		}
		raw, _ := json.Marshal(map[string]any{"labels": answer})
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "completed", "steps": []any{map[string]any{"type": "model_output", "content": []any{map[string]any{"type": "text", "text": string(raw)}}}}})
	}))
	t.Cleanup(server.Close)
	return &mode, llm.New("test-only-key", "gemini-3.7-flash", server.URL, "")
}

func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// answeredEvidence runs the full happy path up to a fresh packet: contact
// confirmation, bounded analysis and an evidence read.
func answeredEvidence(t *testing.T, f *paymentFixture, i domain.Intent) EvidencePacket {
	t.Helper()
	task, answer := taskAnswer(t, f, i)
	if err := f.s.RespondVerification(context.Background(), actor(domain.Subject), task.ID, answer, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Analyze(context.Background(), actor(domain.Reviewer), i.ID, 1); err != nil {
		t.Fatal(err)
	}
	packet, err := f.s.Evidence(context.Background(), actor(domain.Reviewer), i.ID)
	if err != nil {
		t.Fatal(err)
	}
	return packet
}

func TestLLMGroundingDropsInventedExcerpts(t *testing.T) {
	f := setupPayments(t)
	i := f.held(t)
	p03Engine(t, f)
	llmMode, fake := llmFake(t)
	*llmMode = "ungrounded"
	f.s.LLM = fake
	packet := answeredEvidence(t, f, i)
	if packet.LLM.Status != llm.StatusOK || len(packet.LLM.Labels) != 0 {
		t.Fatal("invented excerpt survived grounding", packet.LLM)
	}
	// A grounded flag on the same message survives.
	*llmMode = "flags"
	if _, err := f.s.Analyze(context.Background(), actor(domain.Reviewer), i.ID, 1); err != nil {
		t.Fatal(err)
	}
	flagged, err := f.s.Evidence(context.Background(), actor(domain.Reviewer), i.ID)
	if err != nil || flagged.LLM.Status != llm.StatusOK || len(flagged.LLM.Labels) != 1 || flagged.LLM.Labels[0].Label != "urgent_payment_pressure" || flagged.LLM.Labels[0].Excerpt == "" {
		t.Fatal("grounded flag lost", err, flagged.LLM)
	}
}

func TestLLMOutageLeavesMissingAndReleaseUnchanged(t *testing.T) {
	f := setupPayments(t)
	i := f.held(t)
	p03Engine(t, f)
	llmMode, fake := llmFake(t)
	*llmMode = "outage"
	f.s.LLM = fake
	packet := answeredEvidence(t, f, i)
	if packet.LLM.Status != llm.StatusUnavailable || len(packet.LLM.Labels) != 0 {
		t.Fatal("outage not recorded on its own block", packet.LLM)
	}
	// The extractor disabled must produce identical required checks and
	// attention: the LLM is never one of the checks.
	f.s.LLM = nil
	if _, err := f.s.Analyze(context.Background(), actor(domain.Reviewer), i.ID, 1); err != nil {
		t.Fatal(err)
	}
	disabled, err := f.s.Evidence(context.Background(), actor(domain.Reviewer), i.ID)
	if err != nil {
		t.Fatal(err)
	}
	if disabled.LLM.Status != llm.StatusDisabled {
		t.Fatal("disabled state not recorded", disabled.LLM)
	}
	if strings.Join(disabled.Missing, "|") != strings.Join(packet.Missing, "|") || disabled.Attention != packet.Attention {
		t.Fatal("LLM outcome changed required checks or attention", disabled.Missing, packet.Missing)
	}
	// Release eligibility is unchanged by the outage: approve and capture.
	decision := ReviewDecision{ExpectedVersion: 1, EvidenceID: disabled.ID, EvidenceHash: disabled.Hash, Decision: "APPROVED", Rationale: "Checked exact enrolled response."}
	if err := f.s.Review(context.Background(), actor(domain.Reviewer), i.ID, decision, "test"); err != nil {
		t.Fatal(err)
	}
	f.work(t)
	if f.p.calls["CAPTURE"] != 1 || f.status(t, i.ID) != domain.Captured {
		t.Fatal("LLM outage blocked an eligible release", f.p.calls, f.status(t, i.ID))
	}
}

func TestLLMOnlyFlagRaisesAttention(t *testing.T) {
	f := setupPayments(t)
	i := f.held(t)
	p03Engine(t, f)
	llmMode, fake := llmFake(t)
	*llmMode = "flags"
	f.s.LLM = fake
	packet := answeredEvidence(t, f, i)
	// The p03Engine classifier triggers nothing, so the flag is LLM-only.
	if packet.ClassifierStatus != domain.InferenceOK || len(packet.Signals) != 0 {
		t.Fatal("fixture classifier unexpectedly triggered", packet.Signals)
	}
	if !packet.Attention {
		t.Fatal("LLM-only flag failed to raise attention", packet.LLM, packet.Attention)
	}
	if packet.LLM.Status != llm.StatusOK || len(packet.LLM.Labels) != 1 {
		t.Fatal("flag block wrong", packet.LLM)
	}
	// Without the flag, the same packet must not require attention.
	*llmMode = "none"
	if _, err := f.s.Analyze(context.Background(), actor(domain.Reviewer), i.ID, 1); err != nil {
		t.Fatal(err)
	}
	quiet, err := f.s.Evidence(context.Background(), actor(domain.Reviewer), i.ID)
	if err != nil || quiet.Attention {
		t.Fatal("quiet LLM reading raised attention", err, quiet.Attention)
	}
}

func TestEvidenceHashCoversLLMBlock(t *testing.T) {
	f := setupPayments(t)
	i := f.held(t)
	p03Engine(t, f)
	llmMode, fake := llmFake(t)
	f.s.LLM = fake
	packet := answeredEvidence(t, f, i)
	if packet.LLM.Status != llm.StatusOK || len(packet.LLM.Labels) != 1 {
		t.Fatal("flagged packet expected", packet.LLM)
	}
	*llmMode = "none"
	if _, err := f.s.Analyze(context.Background(), actor(domain.Reviewer), i.ID, 1); err != nil {
		t.Fatal(err)
	}
	quiet, err := f.s.Evidence(context.Background(), actor(domain.Reviewer), i.ID)
	if err != nil || quiet.LLM.Status != llm.StatusOK || len(quiet.LLM.Labels) != 0 {
		t.Fatal("quiet packet expected", err, quiet.LLM)
	}
	if quiet.Hash == packet.Hash {
		t.Fatal("LLM block change did not change the evidence hash")
	}
}

func TestStaleLLMPacketRejectedWithConflict(t *testing.T) {
	f := setupPayments(t)
	i := f.held(t)
	p03Engine(t, f)
	llmMode, fake := llmFake(t)
	f.s.LLM = fake
	packet := answeredEvidence(t, f, i)
	*llmMode = "none"
	if _, err := f.s.Analyze(context.Background(), actor(domain.Reviewer), i.ID, 1); err != nil {
		t.Fatal(err)
	}
	fresh, err := f.s.Evidence(context.Background(), actor(domain.Reviewer), i.ID)
	if err != nil {
		t.Fatal(err)
	}
	// A decision carrying a hash from a different LLM reading cannot match the
	// stored snapshot it names.
	stale := ReviewDecision{ExpectedVersion: 1, EvidenceID: packet.ID, EvidenceHash: fresh.Hash, Decision: "APPROVED", Rationale: "Stale packet mix-up must not release."}
	if err := f.s.Review(context.Background(), actor(domain.Reviewer), i.ID, stale, "test"); err == nil || err.Error() != "EVIDENCE_CONFLICT" {
		t.Fatal("stale packet accepted", err)
	}
	// The correct, current packet still reviews cleanly on the identical path.
	current := ReviewDecision{ExpectedVersion: 1, EvidenceID: fresh.ID, EvidenceHash: fresh.Hash, Decision: "APPROVED", Rationale: "Checked exact enrolled response."}
	if err := f.s.Review(context.Background(), actor(domain.Reviewer), i.ID, current, "test"); err != nil {
		t.Fatal(err)
	}
}

func TestLLMStoredRunNeverAddsMissingOrRunStatus(t *testing.T) {
	f := setupPayments(t)
	i := f.held(t)
	p03Engine(t, f)
	llmMode, fake := llmFake(t)
	*llmMode = "ungrounded"
	f.s.LLM = fake
	packet := answeredEvidence(t, f, i)
	for _, m := range packet.Missing {
		if strings.Contains(strings.ToLower(m), "llm") || strings.Contains(strings.ToLower(m), "ai reading") || strings.Contains(strings.ToLower(m), "gemini") {
			t.Fatal("LLM state leaked into required checks", packet.Missing)
		}
	}
	var runStatus string
	var llmRaw []byte
	if err := f.s.DB.Pool.QueryRow(context.Background(), `SELECT status,llm FROM investigation_runs WHERE intent_id=$1 AND intent_version=$2`, i.ID, i.Version).Scan(&runStatus, &llmRaw); err != nil {
		t.Fatal(err)
	}
	if runStatus != "OK" {
		t.Fatal("LLM outcome changed the run status", runStatus)
	}
	var stored llm.Signals
	if json.Unmarshal(llmRaw, &stored) != nil || stored.Status != llm.StatusOK {
		t.Fatal("llm column not persisted", runStatus, string(llmRaw))
	}
}
