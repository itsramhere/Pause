package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ramst/pause/internal/bootstrap"
	"github.com/ramst/pause/internal/domain"
	"github.com/ramst/pause/internal/llm"
)

var errLLMNotConfigured = errors.New("LLM_NOT_CONFIGURED")
var errFrozenInput = errors.New("FROZEN_INPUT_INVALID")

type llmOptions struct {
	Split       string
	Limit       int
	ConfirmTest bool
	RPS         float64
}

func (o llmOptions) validate() error {
	if (o.Split != "validation" && o.Split != "test") || o.Limit < 0 || o.RPS <= 0 || math.IsNaN(o.RPS) || math.IsInf(o.RPS, 0) || float64(time.Second)/o.RPS >= float64(math.MaxInt64) {
		return errors.New("LLM_OPTIONS_INVALID")
	}
	if o.Split == "test" && (!o.ConfirmTest || o.Limit != 0) {
		return errors.New("TEST_SPLIT_GUARD")
	}
	return nil
}

type labelSet [4]bool

type signalSample struct {
	ID, Text string
	Truth    labelSet
}

type signalData struct {
	Rows                      []signalSample
	DatasetHash, ManifestHash string
	Partial                   bool
}

func loadSignalData(root string, o llmOptions) (signalData, error) {
	if err := o.validate(); err != nil {
		return signalData{}, err
	}
	raw, err := os.ReadFile(filepath.Join(root, "datasets", o.Split+".jsonl"))
	if err != nil {
		return signalData{}, errFrozenInput
	}
	meta, err := os.ReadFile(filepath.Join(root, "datasets", "splits.manifest.json"))
	if err != nil {
		return signalData{}, errFrozenInput
	}
	return verifySignalData(raw, meta, o.Split, o.Limit)
}

func verifySignalData(raw, meta []byte, split string, limit int) (signalData, error) {
	var manifest struct {
		Hashes map[string]string `json:"hashes"`
		Counts map[string]int    `json:"counts"`
	}
	data := signalData{DatasetHash: domain.Hash(raw), ManifestHash: domain.Hash(meta)}
	if json.Unmarshal(meta, &manifest) != nil || manifest.Hashes[split] != data.DatasetHash || manifest.Counts[split] <= 0 {
		return signalData{}, errFrozenInput
	}
	seen := map[string]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var row struct {
			ID     string `json:"message_id"`
			Text   string `json:"text"`
			Labels []*int `json:"labels"`
		}
		if json.Unmarshal(scanner.Bytes(), &row) != nil || row.ID == "" || seen[row.ID] || len(row.Labels) != 4 {
			return signalData{}, errFrozenInput
		}
		seen[row.ID] = true
		sample := signalSample{ID: row.ID, Text: row.Text}
		for i, n := range row.Labels {
			if n == nil || (*n != 0 && *n != 1) {
				return signalData{}, errFrozenInput
			}
			sample.Truth[i] = *n == 1
		}
		data.Rows = append(data.Rows, sample)
	}
	// Even a limited run verifies the entire frozen input before slicing it.
	if scanner.Err() != nil || len(data.Rows) != manifest.Counts[split] {
		return signalData{}, errFrozenInput
	}
	if limit > 0 {
		data.Partial = true
		if limit < len(data.Rows) {
			data.Rows = data.Rows[:limit]
		}
	}
	return data, nil
}

func keywordSignals(text string) (out labelSet) {
	terms := [4][]string{
		{"immediately", "urgent", "now", "minutes", "at once"},
		{"secret", "tell anyone", "keep quiet", "tell nobody", "discussing"},
		{"new number", "temporary account", "unknown phone", "officer", "lawyer", "doctor", "changed number"},
		{"release", "unlock", "winnings", "up front", "clearance fee"},
	}
	text = strings.ToLower(text)
	for i, words := range terms {
		for _, word := range words {
			out[i] = out[i] || strings.Contains(text, word)
		}
	}
	return out
}

func flagsFromLabels(labels []string) (out labelSet) {
	for i, label := range domain.Labels {
		for _, found := range labels {
			out[i] = out[i] || found == label
		}
	}
	return out
}

func (s labelSet) labels() []string {
	out := []string{}
	for i, label := range domain.Labels {
		if s[i] {
			out = append(out, label)
		}
	}
	return out
}

func unionSignals(a, b labelSet) (out labelSet) {
	for i := range out {
		out[i] = a[i] || b[i]
	}
	return out
}

func loadFrozenTFIDF(raw []byte, samples []signalSample) (map[string]labelSet, error) {
	var report struct {
		Rows []struct {
			ID     string   `json:"message_id"`
			Status string   `json:"status"`
			Labels []string `json:"triggered_labels"`
		} `json:"rows"`
	}
	if json.Unmarshal(raw, &report) != nil {
		return nil, errFrozenInput
	}
	out := map[string]labelSet{}
	for _, row := range report.Rows {
		if _, exists := out[row.ID]; exists || row.ID == "" {
			return nil, errFrozenInput
		}
		out[row.ID] = labelSet{}
		if row.Status == "OK" || row.Status == "ABSTAIN" {
			for _, label := range row.Labels {
				if len(flagsFromLabels([]string{label}).labels()) == 0 {
					return nil, errFrozenInput
				}
			}
			out[row.ID] = flagsFromLabels(row.Labels)
		}
	}
	for _, sample := range samples {
		if _, ok := out[sample.ID]; !ok {
			return nil, errFrozenInput
		}
	}
	return out, nil
}

type signalMetric struct {
	TP        int     `json:"tp"`
	FP        int     `json:"fp"`
	FN        int     `json:"fn"`
	Precision float64 `json:"precision"`
	Recall    float64 `json:"recall"`
	F1        float64 `json:"f1"`
	Support   int     `json:"support"`
}

func metric(tp, fp, fn int) signalMetric {
	m := signalMetric{TP: tp, FP: fp, FN: fn, Support: tp + fn}
	if tp+fp > 0 {
		m.Precision = float64(tp) / float64(tp+fp)
	}
	if tp+fn > 0 {
		m.Recall = float64(tp) / float64(tp+fn)
	}
	if 2*tp+fp+fn > 0 {
		m.F1 = float64(2*tp) / float64(2*tp+fp+fn)
	}
	return m
}

type signalMetrics struct {
	PerLabel            map[string]signalMetric `json:"per_label"`
	Micro               signalMetric            `json:"micro"`
	BenignFalseTriggers int                     `json:"benign_false_triggers"`
}

func scoreSignals(truth, predictions []labelSet) signalMetrics {
	out := signalMetrics{PerLabel: map[string]signalMetric{}}
	tp, fp, fn := 0, 0, 0
	for i, label := range domain.Labels {
		a, b, c := 0, 0, 0
		for row, gold := range truth {
			pred := predictions[row][i]
			if gold[i] && pred {
				a++
			} else if pred {
				b++
			} else if gold[i] {
				c++
			}
		}
		out.PerLabel[label] = metric(a, b, c)
		tp, fp, fn = tp+a, fp+b, fn+c
	}
	out.Micro = metric(tp, fp, fn)
	for i, gold := range truth {
		if gold == (labelSet{}) && predictions[i] != (labelSet{}) {
			out.BenignFalseTriggers++
		}
	}
	return out
}

func signalLatency(latencies []int64) map[string]int64 {
	out := map[string]int64{"p50": 0, "p95": 0}
	if len(latencies) > 0 {
		lat := append([]int64{}, latencies...)
		sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
		out["p50"], out["p95"] = lat[len(lat)/2], lat[(len(lat)*95-1)/100]
	}
	return out
}

type signalRow struct {
	ID          string              `json:"message_id"`
	Truth       []string            `json:"truth"`
	Predictions map[string][]string `json:"predictions"`
	Status      llm.Status          `json:"llm_status"`
	Excerpts    map[string]string   `json:"llm_excerpts"`
	Attempts    int                 `json:"attempts"`
}

type signalReport struct {
	SchemaVersion string                   `json:"schema_version"`
	Split         string                   `json:"split"`
	Denominator   int                      `json:"denominator"`
	Partial       bool                     `json:"partial"`
	DatasetHash   string                   `json:"dataset_sha256"`
	ManifestHash  string                   `json:"split_manifest_sha256"`
	Model         string                   `json:"model"`
	PromptVersion string                   `json:"prompt_version"`
	PromptHash    string                   `json:"prompt_hash"`
	GitRevision   string                   `json:"git_revision"`
	RecordedAt    time.Time                `json:"recorded_at"`
	Variants      map[string]signalMetrics `json:"variants"`
	Availability  map[llm.Status]int       `json:"llm_availability"`
	Latency       map[string]int64         `json:"llm_latency_ms"`
	Retries       int                      `json:"llm_retries"`
	Rows          []signalRow              `json:"rows"`
	Scope         string                   `json:"scope"`
	BaselinesNote string                   `json:"baselines_note"`
}

type signalExtractor interface {
	ExtractSignals(context.Context, string) (llm.Signals, error)
}

type evalTiming struct {
	Now  func() time.Time
	Wait func(context.Context, time.Duration) error
}

func waitSignals(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func evaluateSignals(ctx context.Context, data signalData, frozen map[string]labelSet, client signalExtractor, o llmOptions, timing evalTiming) (signalReport, error) {
	if err := o.validate(); err != nil {
		return signalReport{}, err
	}
	if len(data.Rows) == 0 || client == nil {
		return signalReport{}, errFrozenInput
	}
	if timing.Now == nil {
		timing.Now = time.Now
	}
	if timing.Wait == nil {
		timing.Wait = waitSignals
	}
	names := []string{"rules", "llm", "rules_or_llm"}
	if o.Split == "test" {
		for _, row := range data.Rows {
			if _, ok := frozen[row.ID]; !ok {
				return signalReport{}, errFrozenInput
			}
		}
		names = append(names, "tfidf", "tfidf_or_llm")
	}
	report := signalReport{SchemaVersion: "pause-llm-signals-v1", Split: o.Split, Denominator: len(data.Rows), Partial: data.Partial, DatasetHash: data.DatasetHash, ManifestHash: data.ManifestHash, GitRevision: "unknown", RecordedAt: timing.Now().UTC(), Variants: map[string]signalMetrics{}, Availability: map[llm.Status]int{llm.StatusOK: 0, llm.StatusUnavailable: 0, llm.StatusInvalidOutput: 0}, Rows: []signalRow{}, BaselinesNote: "Validation micro precision/recall/F1 for rules and linear are frozen in models/validation-report.json (rules and linear). Validation TF-IDF row predictions are unavailable; the linear variant is omitted on validation. Test TF-IDF uses frozen reports/classifier-final.json rows, including ABSTAIN triggered labels."}
	truth := []labelSet{}
	predictions := map[string][]labelSet{}
	latencies := []int64{}
	interval := time.Duration(float64(time.Second) / o.RPS)
	var next time.Time
	first := true
	for _, sample := range data.Rows {
		var signals llm.Signals
		attempts := 0
		for {
			if err := timing.Wait(ctx, next.Sub(timing.Now())); err != nil {
				return signalReport{}, err
			}
			next = timing.Now().Add(interval)
			callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			result, err := client.ExtractSignals(callCtx, sample.Text)
			cancel()
			signals = result
			attempts++
			if first {
				report.Model, report.PromptVersion, report.PromptHash = signals.Model, signals.PromptVersion, signals.PromptHash
				first = false
			}
			if ctx.Err() != nil {
				return signalReport{}, ctx.Err()
			}
			var failure *llm.Error
			if attempts >= 4 || !errors.As(err, &failure) || (failure.HTTP != 429 && !failure.Temporary) {
				break
			}
			if err := timing.Wait(ctx, time.Duration(1<<attempts)*time.Second); err != nil {
				return signalReport{}, err
			}
		}
		flags := []string{}
		excerpts := map[string]string{}
		if signals.Status == llm.StatusOK {
			for _, flag := range signals.Labels {
				flags = append(flags, flag.Label)
				excerpts[flag.Label] = flag.Excerpt
			}
		}
		llmPred, rules := flagsFromLabels(flags), keywordSignals(sample.Text)
		rowPred := map[string]labelSet{"rules": rules, "llm": llmPred, "rules_or_llm": unionSignals(rules, llmPred)}
		if o.Split == "test" {
			rowPred["tfidf"] = frozen[sample.ID]
			rowPred["tfidf_or_llm"] = unionSignals(frozen[sample.ID], llmPred)
		}
		row := signalRow{ID: sample.ID, Truth: sample.Truth.labels(), Predictions: map[string][]string{}, Status: signals.Status, Excerpts: excerpts, Attempts: attempts}
		for _, name := range names {
			predictions[name] = append(predictions[name], rowPred[name])
			row.Predictions[name] = rowPred[name].labels()
		}
		report.Rows = append(report.Rows, row)
		report.Availability[signals.Status]++
		report.Retries += attempts - 1
		latencies = append(latencies, signals.DurationMS)
		truth = append(truth, sample.Truth)
	}
	for _, name := range names {
		report.Variants[name] = scoreSignals(truth, predictions[name])
	}
	report.Latency = signalLatency(latencies)
	report.Scope = fmt.Sprintf("Prompt developed against the validation split only. The test split is run once with the final prompt (%s) and reported as-is, whether or not it beats the baselines. Synthetic single-author data; not a fraud or real-world accuracy estimate. LLM output is advisory and never gates release.", report.PromptVersion)
	return report, nil
}

func writeSignalReport(output string, report signalReport) error {
	f, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return errors.New("REPORT_CREATE_FAILED")
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	err = enc.Encode(report)
	closed := f.Close()
	if err != nil || closed != nil {
		return errors.New("REPORT_WRITE_FAILED")
	}
	return nil
}

func runLLM(ctx context.Context, o llmOptions, output string) error {
	if err := o.validate(); err != nil {
		return err
	}
	c, err := bootstrap.LoadConfig()
	if c.MistralAPIKey == "" {
		return errLLMNotConfigured
	}
	if err != nil {
		return errors.New("CONFIGURATION_INVALID")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		return errors.New("REPORT_CREATE_FAILED")
	}
	data, err := loadSignalData(".", o)
	if err != nil {
		return err
	}
	var frozen map[string]labelSet
	if o.Split == "test" {
		raw, err := os.ReadFile("reports/classifier-final.json")
		if err != nil {
			return errFrozenInput
		}
		frozen, err = loadFrozenTFIDF(raw, data.Rows)
		if err != nil {
			return err
		}
	}
	report, err := evaluateSignals(ctx, data, frozen, llm.New(c.MistralAPIKey, c.MistralModel, c.MistralBaseURL), o, evalTiming{})
	if err != nil {
		return err
	}
	if revision, err := exec.CommandContext(ctx, "git", "rev-parse", "HEAD").Output(); err == nil {
		report.GitRevision = strings.TrimSpace(string(revision))
	}
	if err := writeSignalReport(output, report); err != nil {
		return err
	}
	bootstrap.Logger().Info("evaluation_complete", "denominator", report.Denominator, "available", report.Availability[llm.StatusOK])
	return nil
}
