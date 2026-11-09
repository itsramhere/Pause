package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ramst/pause/internal/app"
	"github.com/ramst/pause/internal/domain"
	"github.com/ramst/pause/internal/investigation"
	"github.com/ramst/pause/internal/llm"
)

func near(t *testing.T, got, want float64) {
	t.Helper()
	if math.IsNaN(got) || math.Abs(got-want) > 1e-9 {
		t.Fatalf("got %.16g, want %.16g", got, want)
	}
}

func TestSignalMetrics(t *testing.T) {
	truth := []labelSet{{true, true}, {true, false, true}, {}, {false, true}}
	pred := []labelSet{{true, false, true}, {false, false, true}, {true, true}, {false, true}}
	got := scoreSignals(truth, pred)
	want := []signalMetric{
		{TP: 1, FP: 1, FN: 1, Precision: .5, Recall: .5, F1: .5, Support: 2},
		{TP: 1, FP: 1, FN: 1, Precision: .5, Recall: .5, F1: .5, Support: 2},
		{TP: 1, FP: 1, Precision: .5, Recall: 1, F1: 2.0 / 3, Support: 1},
		{},
	}
	for i, label := range domain.Labels {
		if got.PerLabel[label] != want[i] {
			t.Fatalf("%s: %+v", label, got.PerLabel[label])
		}
	}
	near(t, got.PerLabel[domain.Labels[0]].Precision, .5)
	near(t, got.PerLabel[domain.Labels[1]].Recall, .5)
	near(t, got.PerLabel[domain.Labels[2]].F1, 2.0/3)
	if got.Micro.TP != 3 || got.Micro.FP != 3 || got.Micro.FN != 2 || got.Micro.Support != 5 || got.BenignFalseTriggers != 1 {
		t.Fatalf("wrong counts: %+v", got)
	}
	near(t, got.Micro.Precision, .5)
	near(t, got.Micro.Recall, .6)
	near(t, got.Micro.F1, 6.0/11)
	for _, m := range []signalMetric{metric(0, 0, 0), metric(0, 0, 2), metric(0, 2, 0), scoreSignals(nil, nil).Micro} {
		if m.Precision != 0 || m.Recall != 0 || m.F1 != 0 {
			t.Fatalf("zero_division: %+v", m)
		}
	}
}

func readEvalFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestRulesValidationGolden(t *testing.T) {
	data, err := loadSignalData(filepath.Join("..", ".."), llmOptions{Split: "validation", RPS: 1})
	if err != nil {
		t.Fatal(err)
	}
	var golden struct {
		Rules signalMetric `json:"rules"`
	}
	if err := json.Unmarshal(readEvalFile(t, "../../models/validation-report.json"), &golden); err != nil {
		t.Fatal(err)
	}
	truth, pred := []labelSet{}, []labelSet{}
	for _, row := range data.Rows {
		truth = append(truth, row.Truth)
		pred = append(pred, keywordSignals(row.Text))
	}
	got := scoreSignals(truth, pred).Micro
	near(t, got.Precision, golden.Rules.Precision)
	near(t, got.Recall, golden.Rules.Recall)
	near(t, got.F1, golden.Rules.F1)
	t.Logf("rules validation: precision=%.16g recall=%.16g f1=%.16g", got.Precision, got.Recall, got.F1)
}

func TestTFIDFTestGolden(t *testing.T) {
	data, err := loadSignalData(filepath.Join("..", ".."), llmOptions{Split: "test", ConfirmTest: true, RPS: 1})
	if err != nil {
		t.Fatal(err)
	}
	raw := readEvalFile(t, "../../reports/classifier-final.json")
	frozen, err := loadFrozenTFIDF(raw, data.Rows)
	if err != nil {
		t.Fatal(err)
	}
	var golden struct {
		PerLabel map[string]signalMetric `json:"per_label"`
	}
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	truth, pred := []labelSet{}, []labelSet{}
	for _, row := range data.Rows {
		truth = append(truth, row.Truth)
		pred = append(pred, frozen[row.ID])
	}
	got := scoreSignals(truth, pred)
	for _, label := range domain.Labels {
		a, b := got.PerLabel[label], golden.PerLabel[label]
		near(t, a.Precision, b.Precision)
		near(t, a.Recall, b.Recall)
		near(t, a.F1, b.F1)
		if a.Support != b.Support {
			t.Fatalf("%s support %d != %d", label, a.Support, b.Support)
		}
		t.Logf("%s: precision=%.16g recall=%.16g f1=%.16g support=%d match", label, a.Precision, a.Recall, a.F1, a.Support)
	}
}

func fixtureSplit(t *testing.T, root, split string, raw []byte, count int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "datasets"), 0755); err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(map[string]any{"hashes": map[string]string{split: domain.Hash(raw)}, "counts": map[string]int{split: count}})
	if err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string][]byte{split + ".jsonl": raw, "splits.manifest.json": manifest} {
		if err := os.WriteFile(filepath.Join(root, "datasets", path), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

const tinySplit = `{"message_id":"one","text":"Pay now, keep quiet.","labels":[1,1,0,0]}
{"message_id":"two","text":"A routine purchase.","labels":[0,0,0,0]}
`

func TestFrozenSplitVerification(t *testing.T) {
	root := t.TempDir()
	opts := llmOptions{Split: "validation", RPS: 1}
	fixtureSplit(t, root, "validation", []byte(tinySplit), 2)
	data, err := loadSignalData(root, opts)
	if err != nil || len(data.Rows) != 2 || data.DatasetHash != domain.Hash([]byte(tinySplit)) {
		t.Fatal("valid fixture failed", err)
	}
	opts.Limit = 1
	data, err = loadSignalData(root, opts)
	if err != nil || len(data.Rows) != 1 || !data.Partial {
		t.Fatal("limited fixture failed", err)
	}
	if err := os.WriteFile(filepath.Join(root, "datasets", "validation.jsonl"), []byte(tinySplit+" "), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSignalData(root, opts); err == nil {
		t.Fatal("tampered bytes accepted")
	}
	fixtureSplit(t, root, "validation", []byte(tinySplit), 3)
	if _, err := loadSignalData(root, opts); err == nil {
		t.Fatal("wrong count accepted, even with limit")
	}
	for _, labels := range []string{"[1,0,0]", "[1,0,0,2]", "[1,0,null,0]", "[1,0,0,-1]", "[1,0,0,true]"} {
		raw := []byte(`{"message_id":"bad","text":"x","labels":` + labels + "}\n")
		fixtureSplit(t, root, "validation", raw, 1)
		if _, err := loadSignalData(root, opts); err == nil {
			t.Fatalf("invalid labels accepted: %s", labels)
		}
	}
	fixtureSplit(t, root, "validation", []byte(tinySplit+tinySplit), 4)
	if _, err := loadSignalData(root, opts); err == nil {
		t.Fatal("duplicate IDs accepted")
	}
}

func TestFrozenTFIDFStatusesAndCoverage(t *testing.T) {
	raw := []byte(`{"rows":[
{"message_id":"ok","status":"OK","triggered_labels":["secrecy_pressure"]},
{"message_id":"abstain","status":"ABSTAIN","triggered_labels":["urgent_payment_pressure"]},
{"message_id":"unavailable","status":"UNAVAILABLE","triggered_labels":["urgent_payment_pressure"]},
{"message_id":"unsupported","status":"UNSUPPORTED","triggered_labels":["urgent_payment_pressure"]}]}`)
	got, err := loadFrozenTFIDF(raw, []signalSample{{ID: "ok"}, {ID: "abstain"}, {ID: "unavailable"}, {ID: "unsupported"}})
	if err != nil || !got["ok"][1] || !got["abstain"][0] || got["unavailable"] != (labelSet{}) || got["unsupported"] != (labelSet{}) {
		t.Fatal("wrong status semantics", got, err)
	}
	if _, err := loadFrozenTFIDF(raw, []signalSample{{ID: "missing"}}); err == nil {
		t.Fatal("missing row accepted")
	}
	for _, invalid := range []string{`{`, `{"rows":[{"message_id":"x"},{"message_id":"x"}]}`, `{"rows":[{"message_id":"x","status":"OK","triggered_labels":["unknown"]}]}`} {
		if _, err := loadFrozenTFIDF([]byte(invalid), nil); err == nil {
			t.Fatal("invalid frozen report accepted")
		}
	}
}

func fakeEvalMistral(t *testing.T, mode string) (*llm.Client, *int) {
	t.Helper()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer offline-secret-key" {
			t.Error("unexpected Mistral request")
		}
		if mode == "500" || mode == "429-ok" && calls == 1 {
			code := 500
			if mode == "429-ok" {
				code = 429
			}
			w.WriteHeader(code)
			_, _ = w.Write([]byte("private raw output offline-secret-key"))
			return
		}
		var request struct {
			Messages []struct{ Content string }
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || len(request.Messages) != 2 {
			t.Error("invalid request")
			w.WriteHeader(400)
			return
		}
		labels := map[string]any{}
		for _, label := range domain.Labels {
			labels[label] = map[string]any{"present": false, "excerpt": ""}
		}
		if strings.Contains(request.Messages[1].Content, "Pay now") {
			excerpt := "Pay now"
			if mode == "ungrounded" {
				excerpt = "private raw output offline-secret-key"
			}
			labels[domain.Labels[0]] = map[string]any{"present": true, "excerpt": excerpt}
		}
		answer, _ := json.Marshal(map[string]any{"labels": labels})
		if mode == "malformed" {
			answer = []byte(`{"labels":private raw output offline-secret-key`)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(answer)}}}})
	}))
	t.Cleanup(server.Close)
	return llm.New("offline-secret-key", "offline-model", server.URL), &calls
}

func fakeEvalTime() (evalTiming, *[]time.Duration) {
	now := time.Date(2026, 11, 9, 1, 2, 3, 0, time.UTC)
	waits := []time.Duration{}
	return evalTiming{Now: func() time.Time { return now }, Wait: func(ctx context.Context, d time.Duration) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if d > 0 {
			waits = append(waits, d)
			now = now.Add(d)
		}
		return nil
	}}, &waits
}

func TestEvaluateSignalsMistralFake(t *testing.T) {
	for _, mode := range []string{"valid", "ungrounded", "malformed", "429-ok", "500"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			fixtureSplit(t, root, "validation", []byte(tinySplit), 2)
			opts := llmOptions{Split: "validation", Limit: 2, RPS: 1}
			data, err := loadSignalData(root, opts)
			if err != nil {
				t.Fatal(err)
			}
			client, calls := fakeEvalMistral(t, mode)
			timing, waits := fakeEvalTime()
			report, err := evaluateSignals(context.Background(), data, nil, client, opts, timing)
			if err != nil {
				t.Fatal(err)
			}
			wantStatus, attempts, retries, wantCalls := llm.StatusOK, 1, 0, 2
			if mode == "malformed" {
				wantStatus = llm.StatusInvalidOutput
			}
			if mode == "429-ok" {
				attempts, retries, wantCalls = 2, 1, 3
			}
			if mode == "500" {
				wantStatus, attempts, retries, wantCalls = llm.StatusUnavailable, 4, 6, 8
			}
			if *calls != wantCalls || report.Rows[0].Attempts != attempts || report.Rows[1].Attempts != 1 && mode != "500" || report.Retries != retries {
				t.Fatalf("attempt accounting: calls=%d rows=%+v retries=%d", *calls, report.Rows, report.Retries)
			}
			if mode == "500" && report.Rows[1].Attempts != 4 {
				t.Fatal("second outage row did not retry")
			}
			if report.Availability[wantStatus] != 2 || len(report.Availability) != 3 {
				t.Fatalf("availability: %+v", report.Availability)
			}
			if mode == "429-ok" && !reflect.DeepEqual(*waits, []time.Duration{2 * time.Second, time.Second}) {
				t.Fatalf("retry/pacing waits: %v", *waits)
			}
			if mode == "500" && !reflect.DeepEqual(*waits, []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}) {
				t.Fatalf("exponential backoff: %v", *waits)
			}
			if report.Rows[0].Status != wantStatus || report.Rows[1].Status != wantStatus {
				t.Fatal("wrong final statuses")
			}
			wantFlags := []string{}
			if mode == "valid" || mode == "429-ok" {
				wantFlags = []string{domain.Labels[0]}
				if report.Rows[0].Excerpts[domain.Labels[0]] != "Pay now" {
					t.Fatal("grounded excerpt missing")
				}
			}
			if !reflect.DeepEqual(report.Rows[0].Predictions["llm"], wantFlags) || len(report.Rows[1].Predictions["llm"]) != 0 || len(report.Rows[0].Predictions["rules_or_llm"]) != 2 {
				t.Fatalf("wrong predictions: %+v", report.Rows)
			}
			if len(wantFlags) == 0 && len(report.Rows[0].Excerpts) != 0 {
				t.Fatal("ungrounded or unavailable excerpts retained")
			}
			if report.SchemaVersion != "pause-llm-signals-v1" || report.Denominator != 2 || !report.Partial || report.Split != "validation" || len(report.Variants) != 3 || len(report.Rows) != 2 || report.Model != "offline-model" || report.PromptVersion != llm.PromptVersion || len(report.PromptHash) != 64 || report.DatasetHash != data.DatasetHash || report.ManifestHash != data.ManifestHash || report.GitRevision != "unknown" || report.RecordedAt.Location() != time.UTC {
				t.Fatalf("report metadata: %+v", report)
			}
			if !strings.Contains(report.Scope, llm.PromptVersion) || !strings.Contains(report.BaselinesNote, "models/validation-report.json") {
				t.Fatal("missing scope or baseline provenance")
			}
			if len(report.Latency) != 2 || report.Latency["p50"] < 0 || report.Latency["p95"] < 0 {
				t.Fatal("invalid latency")
			}
			output := filepath.Join(root, "sanitized.json")
			if err := writeSignalReport(output, report); err != nil {
				t.Fatal(err)
			}
			raw := readEvalFile(t, output)
			for _, private := range []string{"offline-secret-key", "private raw output", "UNTRUSTED_MESSAGE", "A routine purchase.", "Pay now, keep quiet."} {
				if strings.Contains(string(raw), private) {
					t.Fatal("private/request data leaked into report")
				}
			}
			if !strings.Contains(string(raw), "\n  \"schema_version\"") {
				t.Fatal("report lacks two-space indentation")
			}
			var decoded signalReport
			if json.Unmarshal(raw, &decoded) != nil || !reflect.DeepEqual(decoded, report) {
				t.Fatal("report failed JSON round trip")
			}
			if writeSignalReport(output, report) == nil || !reflect.DeepEqual(raw, readEvalFile(t, output)) {
				t.Fatal("existing report was overwritten")
			}
		})
	}
}

func TestEvaluateSignalsTestUnion(t *testing.T) {
	root := t.TempDir()
	fixtureSplit(t, root, "test", []byte(tinySplit), 2)
	opts := llmOptions{Split: "test", ConfirmTest: true, RPS: 2}
	data, err := loadSignalData(root, opts)
	if err != nil {
		t.Fatal(err)
	}
	client, calls := fakeEvalMistral(t, "valid")
	timing, waits := fakeEvalTime()
	if _, err := evaluateSignals(context.Background(), data, nil, client, opts, timing); err == nil || *calls != 0 {
		t.Fatal("missing frozen predictions did not fail before requests")
	}
	frozen := map[string]labelSet{"one": {false, false, true}, "two": {false, false, false, true}}
	report, err := evaluateSignals(context.Background(), data, frozen, client, opts, timing)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Variants) != 5 || report.Partial || !reflect.DeepEqual(report.Rows[0].Predictions["tfidf_or_llm"], []string{domain.Labels[0], domain.Labels[2]}) || report.Variants["tfidf"].BenignFalseTriggers != 1 || report.Variants["tfidf_or_llm"].BenignFalseTriggers != 1 || !reflect.DeepEqual(*waits, []time.Duration{500 * time.Millisecond}) {
		t.Fatalf("test union/pacing: %+v waits=%v", report, *waits)
	}
}

func TestSignalLatency(t *testing.T) {
	lat := []int64{7, 0, 6, 1, 5, 2, 4, 3}
	if !reflect.DeepEqual(signalLatency(lat), map[string]int64{"p50": 4, "p95": 7}) || lat[0] != 7 || !reflect.DeepEqual(signalLatency(nil), map[string]int64{"p50": 0, "p95": 0}) {
		t.Fatal("latency index convention or input mutation")
	}
}

func TestLLMGuards(t *testing.T) {
	for _, opts := range []llmOptions{
		{Split: "test", RPS: 1}, {Split: "test", ConfirmTest: true, Limit: 1, RPS: 1},
		{Split: "train", RPS: 1}, {Split: "validation", Limit: -1, RPS: 1},
		{Split: "validation", RPS: 0}, {Split: "validation", RPS: -1},
		{Split: "validation", RPS: math.NaN()}, {Split: "validation", RPS: math.Inf(1)},
	} {
		if opts.validate() == nil {
			t.Fatalf("invalid options accepted: %+v", opts)
		}
	}
	for _, opts := range []llmOptions{{Split: "validation", RPS: 1}, {Split: "validation", Limit: 1, RPS: 1}, {Split: "test", ConfirmTest: true, RPS: 1}} {
		if err := opts.validate(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWorkflowLLMLabels(t *testing.T) {
	base := make([]string, 1, 8)
	base[0] = domain.Labels[0]
	signals := llm.Signals{Status: llm.StatusOK, Labels: []llm.LabelFlag{{Label: domain.Labels[1]}, {Label: domain.Labels[0]}, {Label: domain.Labels[1]}}}
	got, added := workflowLLMLabels(base, signals)
	if added != 1 || !reflect.DeepEqual(got, []string{domain.Labels[0], domain.Labels[1]}) || base[:cap(base)][1] != "" {
		t.Fatal("merge duplicated labels or mutated baseline", got, added)
	}
	for _, status := range []llm.Status{llm.StatusUnavailable, llm.StatusInvalidOutput, llm.StatusDisabled} {
		signals.Status = status
		got, added := workflowLLMLabels(base, signals)
		if added != 0 || !reflect.DeepEqual(got, base) {
			t.Fatal("non-OK signals affected workflow")
		}
	}
}

type evalTransport func(*http.Request) (*http.Response, error)

func (f evalTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWorkflowLLMReport(t *testing.T) {
	t.Chdir("../..")
	root := t.TempDir()
	e := &investigation.Engine{}
	if err := json.Unmarshal(readEvalFile(t, "models/classifier.manifest.json"), &e.Classifier); err != nil {
		t.Fatal(err)
	}
	// In-memory dependency responses: no ML service, Elastic or DB is used.
	e.HTTP = &http.Client{Transport: evalTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/classify" {
			return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
		}
		var in domain.InferenceInput
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			t.Fatal("invalid classification input")
		}
		scores := map[string]float64{}
		for _, label := range domain.Labels {
			scores[label] = 0
		}
		scores[domain.Labels[0]] = 1
		out := domain.Classification{SchemaVersion: domain.ContractVersion, IntentID: in.IntentID, IntentVersion: in.IntentVersion, ContentDigest: in.ContentDigest, ModelVersion: e.Classifier.ModelVersion, ArtifactHash: e.Classifier.ArtifactHash, LabelSchemaVersion: e.Classifier.LabelSchemaVersion, PreprocessingVersion: e.Classifier.PreprocessingVersion, LabelScores: scores, TriggeredLabels: []string{domain.Labels[0]}, Status: domain.InferenceOK}
		raw, _ := json.Marshal(out)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw))), Header: http.Header{}}, nil
	})}
	s := &app.Service{Investigation: e}
	type workflowRow struct {
		Variants map[string]json.RawMessage `json:"variants"`
		Status   llm.Status                 `json:"llm_status"`
	}
	type workflowReport struct {
		Rows          []workflowRow `json:"rows"`
		LLMVariant    string        `json:"llm_variant"`
		PromptVersion string        `json:"prompt_version"`
	}
	run := func(name string) workflowReport {
		t.Helper()
		path := filepath.Join(root, name+".json")
		if err := workflow(context.Background(), s, path); err != nil {
			t.Fatal(err)
		}
		var report workflowReport
		if err := json.Unmarshal(readEvalFile(t, path), &report); err != nil {
			t.Fatal(err)
		}
		return report
	}
	baseline := run("disabled")
	if baseline.LLMVariant != "not configured" || baseline.PromptVersion != "" || len(baseline.Rows) != 40 {
		t.Fatal("disabled workflow metadata")
	}
	mode, calls := "OK", 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if mode == "UNAVAILABLE" {
			w.WriteHeader(500)
			return
		}
		var request struct {
			Messages []struct{ Content string }
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil || len(request.Messages) != 2 {
			t.Error("invalid Mistral request")
			w.WriteHeader(400)
			return
		}
		text := strings.TrimSuffix(strings.TrimPrefix(request.Messages[1].Content, llm.MessageStart+"\n"), "\n"+llm.MessageEnd)
		excerpt := string([]rune(text)[:5])
		labels := map[string]any{}
		for i, label := range domain.Labels {
			quote := ""
			if i < 2 {
				quote = excerpt
			}
			labels[label] = map[string]any{"present": i < 2, "excerpt": quote}
		}
		answer, _ := json.Marshal(map[string]any{"labels": labels})
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(answer)}}}})
	}))
	defer server.Close()
	s.LLM = llm.New("offline-secret-key", "offline-model", server.URL)
	for _, status := range []string{"OK", "UNAVAILABLE"} {
		mode = status
		report := run(status)
		if report.LLMVariant != "" || report.PromptVersion != llm.PromptVersion || len(report.Rows) != 40 {
			t.Fatal("enabled workflow metadata")
		}
		for i, row := range report.Rows {
			if len(row.Variants) != 4 || string(row.Status) != status || len(baseline.Rows[i].Variants) != 3 || baseline.Rows[i].Status != "" {
				t.Fatal("workflow row shape/status")
			}
			for _, name := range []string{"rules", "rules_classifier", "full"} {
				if string(row.Variants[name]) != string(baseline.Rows[i].Variants[name]) {
					t.Fatalf("existing %s variant changed", name)
				}
			}
			var base, enhanced struct {
				Labels []string `json:"signal_labels"`
				Count  int      `json:"attention_reason_count"`
			}
			if json.Unmarshal(row.Variants["rules_classifier"], &base) != nil || json.Unmarshal(row.Variants["rules_classifier_llm"], &enhanced) != nil {
				t.Fatal("invalid variant")
			}
			added := 0
			if status == "OK" {
				added = 1
			}
			if len(base.Labels) != 1 || len(enhanced.Labels) != 1+added || enhanced.Count != base.Count+added {
				t.Fatalf("workflow count/label union: %+v %+v", base, enhanced)
			}
		}
	}
	if calls != 80 {
		t.Fatalf("unexpected workflow LLM calls/retries: %d", calls)
	}
}

// Run the real mode dispatch in a child test process so fail's os.Exit and
// reason-only logging can be checked without opening product dependencies.
func TestLLMMainProcess(t *testing.T) {
	if os.Getenv("PAUSE_EVAL_CHILD") == "1" {
		flag.CommandLine = flag.NewFlagSet("eval", flag.ExitOnError)
		os.Args = append([]string{"eval"}, strings.Split(os.Getenv("PAUSE_EVAL_ARGS"), " ")...)
		main()
		return
	}
	root := t.TempDir()
	fixtureSplit(t, root, "validation", []byte(tinySplit), 2)
	clientCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientCalls++
		labels := map[string]any{}
		for _, label := range domain.Labels {
			labels[label] = map[string]any{"present": false, "excerpt": ""}
		}
		answer, _ := json.Marshal(map[string]any{"labels": labels})
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": string(answer)}}}})
	}))
	defer server.Close()
	envFile := filepath.Join(root, "eval.env")
	if err := os.WriteFile(envFile, []byte("MISTRAL_MODEL=env-file-model\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"PAUSE_EVAL_CHILD": "1", "ENV_FILE": envFile, "APP_ENV": "test",
		"DATABASE_URL": "postgres://offline:offline@127.0.0.1:1/offline", "MESSAGE_ENCRYPTION_KEY": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=", "PAYPAL_MERCHANT_ID": "offline",
		"APP_BASE_URL": "http://127.0.0.1:5173", "MISTRAL_BASE_URL": server.URL, "MISTRAL_MODEL": "",
		"ML_SERVICE_URL": "", "ELASTICSEARCH_URL": "", "MISTRAL_API_KEY": "offline-secret-key",
		"PAYPAL_BASE_URL": "https://api-m.sandbox.paypal.com", "PAYPAL_CLIENT_ID": "", "PAYPAL_CLIENT_SECRET": "", "PAYPAL_WEBHOOK_ID": "",
	} {
		t.Setenv(name, value)
	}
	// godotenv preserves explicitly set variables, including empty values.
	// Leave this one absent so the test exercises loading it from ENV_FILE.
	if err := os.Unsetenv("MISTRAL_MODEL"); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, args, reason string
		missingKey         bool
	}{
		{"success", "-mode llm -split validation -limit 1 -output new.json", "", false},
		{"existing output", "-mode llm -split validation -limit 1 -output new.json", "CONFIGURATION_OR_FROZEN_INPUT_ERROR", false},
		{"missing key", "-mode llm -output missing.json", "LLM_NOT_CONFIGURED", true},
		{"test unconfirmed", "-mode llm -split test -output guarded.json", "CONFIGURATION_OR_FROZEN_INPUT_ERROR", false},
		{"test limited", "-mode llm -split test -confirm-test -limit 1 -output guarded.json", "CONFIGURATION_OR_FROZEN_INPUT_ERROR", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("PAUSE_EVAL_ARGS", test.args)
			if test.missingKey {
				t.Setenv("MISTRAL_API_KEY", "")
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestLLMMainProcess$")
			cmd.Dir = root
			out, err := cmd.CombinedOutput()
			if test.reason == "" {
				if err != nil {
					t.Fatalf("standalone eval failed: %s", out)
				}
			} else if err == nil || !strings.Contains(string(out), `"reason_code":"`+test.reason+`"`) {
				t.Fatalf("wrong failure: %v %s", err, out)
			}
			for _, private := range []string{"offline-secret-key", server.URL, "postgres://", "Pay now"} {
				if strings.Contains(string(out), private) {
					t.Fatal("logs contain private data")
				}
			}
		})
	}
	if clientCalls != 1 {
		t.Fatalf("guards made unexpected requests: %d", clientCalls)
	}
	var report signalReport
	if json.Unmarshal(readEvalFile(t, filepath.Join(root, "new.json")), &report) != nil || report.Model != "env-file-model" || report.Denominator != 1 || !report.Partial {
		t.Fatal("config-only mode dispatch or env-file loading failed")
	}
	if _, err := os.Stat(filepath.Join(root, "guarded.json")); !os.IsNotExist(err) {
		t.Fatal("guard created output")
	}
}
