// Frozen public relevance evaluation. Uses real approved inference and Elastic.
// No training, indexing, approval, database writes or threshold selection.
package main

import (
	"encoding/json"
	"flag"
	"github.com/ramst/pause/internal/bootstrap"
	"github.com/ramst/pause/internal/domain"
	"github.com/ramst/pause/internal/investigation"
	"os"
	"sort"
	"time"
)

type query struct {
	ID       string   `json:"query_id"`
	Text     string   `json:"text"`
	Relevant []string `json:"relevant_ids"`
	Language string   `json:"language"`
}

func main() {
	output := flag.String("output", "reports/retrieval-final.json", "new sanitized report path")
	mode := flag.String("mode", "public", "public or workflow")
	flag.Parse()
	ctx, stop := bootstrap.Context()
	defer stop()
	s, err := bootstrap.Load(ctx)
	if err != nil {
		fail()
	}
	defer s.DB.Pool.Close()
	if s.Investigation == nil {
		fail()
	}
	if *mode == "workflow" {
		if workflow(ctx, s, *output) != nil {
			fail()
		}
		return
	}
	if *mode != "public" {
		fail()
	}
	raw, err := os.ReadFile("search/evaluation/public-queries-v1.sealed.json")
	if err != nil {
		fail()
	}
	meta, err := os.ReadFile("search/evaluation/manifest.json")
	if err != nil {
		fail()
	}
	var m struct {
		Hash  string `json:"sha256"`
		Count int    `json:"count"`
	}
	if json.Unmarshal(meta, &m) != nil || domain.Hash(raw) != m.Hash {
		fail()
	}
	var queries []query
	if json.Unmarshal(raw, &queries) != nil || len(queries) != m.Count {
		fail()
	}
	rows := []any{}
	lat := []int64{}
	available := 0
	for _, q := range queries {
		started := time.Now()
		r, receipt, err := s.Investigation.Retrieve(ctx, q.Text)
		ms := time.Since(started).Milliseconds()
		lat = append(lat, ms)
		branches := map[string][]string{"lexical": {}, "vector": {}, "fused": {}}
		if err == nil {
			available++
			for _, branch := range []string{"lexical", "vector"} {
				candidates := append([]investigation.Candidate{}, receipt.Candidates...)
				rank := func(c investigation.Candidate) int {
					if branch == "lexical" {
						return c.KeywordRank
					}
					return c.VectorRank
				}
				sort.Slice(candidates, func(i, j int) bool { return rank(candidates[i]) < rank(candidates[j]) })
				for _, c := range candidates {
					if rank(c) > 0 && len(branches[branch]) < 3 {
						branches[branch] = append(branches[branch], c.ID)
					}
				}
			}
			for _, h := range r.Hits {
				branches["fused"] = append(branches["fused"], h.SourceID)
			}
		}
		scores := map[string]any{}
		for name, ids := range branches {
			hits := 0
			for _, id := range ids {
				for _, gold := range q.Relevant {
					if id == gold {
						hits++
					}
				}
			}
			recall := 0.0
			if len(q.Relevant) > 0 {
				recall = float64(hits) / float64(len(q.Relevant))
			}
			scores[name] = map[string]any{"top3_ids": ids, "relevant_count": hits, "precision_at_3": float64(hits) / 3, "recall_at_3": recall, "no_match_case": len(q.Relevant) == 0, "correct_no_match": err == nil && len(q.Relevant) == 0 && len(ids) == 0}
		}
		status := "OK"
		if err != nil {
			status = "UNAVAILABLE"
		}
		rows = append(rows, map[string]any{"query_id": q.ID, "status": status, "duration_ms": ms, "gold_count": len(q.Relevant), "branches": scores})
	}
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	report := map[string]any{"schema_version": "pause-final-public-retrieval-v1", "denominator": len(queries), "available": available, "unavailable": len(queries) - available, "query_sha256": m.Hash, "classifier": s.Investigation.Classifier, "encoder": s.Investigation.Encoder, "corpus_sha256": s.Investigation.CorpusHash, "mapping": "playbooks-v1", "fusion": investigation.FusionVersion, "template": investigation.TemplateVersion, "latency_ms": map[string]int64{"p50": lat[len(lat)/2], "p95": lat[(len(lat)*95-1)/100]}, "rows": rows, "scope": "Previously sealed synthetic public relevance set only; source IDs, no text/vectors/secrets. No runtime no-match threshold exists; poor no-match is a result, never safety assurance.", "private_evaluation": "Separate integration report; do not conflate public relevance with scope/privacy or workflow correctness"}
	f, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		fail()
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if enc.Encode(report) != nil {
		fail()
	}
	bootstrap.Logger().Info("evaluation_complete", "denominator", len(queries), "available", available)
}
func fail() {
	bootstrap.Logger().Error("evaluation_failed", "reason_code", "CONFIGURATION_OR_FROZEN_INPUT_ERROR")
	os.Exit(1)
}
