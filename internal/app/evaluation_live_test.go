package app

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/ramst/pause/internal/domain"
	"github.com/ramst/pause/internal/investigation"
	"github.com/ramst/pause/internal/store"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// Frozen tiny private relevance set. Isolated PG schema/Elastic index; no
// household text, gold labels or query text enters a public index or training.
func TestP06FrozenPrivateRelevance(t *testing.T) {
	if os.Getenv("P06_EVAL") != "1" {
		t.Skip("P06_EVAL=1 opens separately frozen private relevance cases")
	}
	f := setupPayments(t)
	ctx := context.Background()
	e := p04LiveEngine(t, f)
	index := "pause-request-history-v1-eval-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	liveCreateHistory(t, e, index)
	if _, err := f.s.DB.Pool.Exec(ctx, `INSERT INTO history_indexes(name,encoder_version,state,created_at) VALUES($1,$2,'ACTIVE',$3)`, index, e.Encoder.EncoderVersion, f.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if err := e.SwitchHistory(ctx, index); err != nil {
		t.Fatal(err)
	}
	yes := true
	now := f.clock.now
	ids := map[string]string{}
	for n, x := range []struct{ id, purpose, text string }{{"ride", "Clinic taxi purchase", "The taxi ride to the clinic costs forty dollars. Please check with me through our usual call before paying."}, {"medicine", "Prescription medicine purchase", "The pharmacy prescription refill is ready. Could you buy the medicine after our regular call?"}} {
		f.clock.now = now.Add(-time.Duration(3-n) * 24 * time.Hour)
		i, _, err := f.s.Create(ctx, actor(domain.Senior), Intake{Amount: "40.00", Currency: "USD", MerchantID: f.s.Config.MerchantID, SubjectContactID: actorContact(), Purpose: x.purpose, Message: x.text, HistoryConsent: &yes}, uuid.NewString(), "frozen-private-fixture")
		if err != nil {
			t.Fatal(err)
		}
		ids[x.id] = i.ID
	}
	f.clock.now = now
	current := p04Request(t, f, "900.00", true, false)
	for n := 0; n < 10; n++ {
		worked, err := f.s.WorkHistoryOnce(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			break
		}
	}
	p, h, err := f.s.historyForAnalysis(ctx, actor(domain.Reviewer), current.ID)
	if err != nil {
		t.Fatal(err)
	}
	var queries []struct {
		ID       string   `json:"id"`
		Text     string   `json:"text"`
		Relevant []string `json:"relevant"`
	}
	raw, err := os.ReadFile("../../search/evaluation/private-queries-v1.json")
	if err != nil || json.Unmarshal(raw, &queries) != nil {
		t.Fatal(err)
	}
	meta, err := os.ReadFile("../../search/evaluation/private-manifest.json")
	var frozen struct {
		Count int    `json:"count"`
		Hash  string `json:"sha256"`
	}
	if err != nil || json.Unmarshal(meta, &frozen) != nil || frozen.Count != len(queries) || frozen.Hash != domain.Hash(raw) {
		t.Fatal("private relevance freeze mismatch")
	}
	rows := []any{}
	available := 0
	for _, q := range queries {
		start := time.Now()
		r, receipt, err := e.RetrieveHistory(ctx, historyQuery(p, h, e.Encoder.EncoderVersion, q.Text), historyPermissions(h))
		branches := map[string][]string{"lexical": {}, "vector": {}, "fused": {}}
		if err == nil {
			available++
			for _, branch := range []string{"lexical", "vector"} {
				cs := append([]investigation.Candidate{}, receipt.Candidates...)
				rank := func(c investigation.Candidate) int {
					if branch == "lexical" {
						return c.KeywordRank
					}
					return c.VectorRank
				}
				sort.Slice(cs, func(i, j int) bool { return rank(cs[i]) < rank(cs[j]) })
				for _, c := range cs {
					if rank(c) > 0 && len(branches[branch]) < 3 {
						branches[branch] = append(branches[branch], c.ID)
					}
				}
			}
			for _, hit := range r.Hits {
				branches["fused"] = append(branches["fused"], hit.SourceID)
			}
		}
		metrics := map[string]any{}
		for name, hits := range branches {
			relevant := 0
			symbolic := []string{}
			for _, hit := range hits {
				known := false
				for key, id := range ids {
					if hit == id {
						known = true
						symbolic = append(symbolic, key)
						for _, gold := range q.Relevant {
							if key == gold {
								relevant++
							}
						}
					}
				}
				if !known {
					t.Fatal("scope escaped")
				}
			}
			recall := 0.0
			if len(q.Relevant) > 0 {
				recall = float64(relevant) / float64(len(q.Relevant))
			}
			metrics[name] = map[string]any{"top3_ids": symbolic, "relevant_count": relevant, "precision_at_3": float64(relevant) / 3, "recall_at_3": recall, "no_match_case": len(q.Relevant) == 0, "correct_no_match": err == nil && len(q.Relevant) == 0 && len(hits) == 0}
		}
		rows = append(rows, map[string]any{"query_id": q.ID, "available": err == nil, "duration_ms": time.Since(start).Milliseconds(), "branches": metrics})
	}
	// Canonical permission must suppress an old successful result immediately,
	// before the worker physically removes the source's text/vector.
	r, _, err := e.RetrieveHistory(ctx, historyQuery(p, h, e.Encoder.EncoderVersion, queries[0].Text), historyPermissions(h))
	if err != nil {
		t.Fatal(err)
	}
	no := false
	ride, err := f.s.Read(ctx, actor(domain.Senior), ids["ride"])
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.ChangeConsent(ctx, actor(domain.Senior), ride.ID, ConsentChange{ExpectedVersion: ride.Consent.Version, History: &no}, "eval-withdrawal"); err != nil {
		t.Fatal(err)
	}
	_, fresh, err := f.s.historyForAnalysis(ctx, actor(domain.Reviewer), current.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.s.DB.WithTx(ctx, func(tx pgx.Tx) error { return f.s.hydrateHistoryPacket(ctx, tx, &fresh, r) }); err != nil {
		t.Fatal(err)
	}
	for _, m := range fresh.Matches {
		if m.IntentID == ride.ID {
			t.Fatal("revoked stale hit hydrated")
		}
	}
	report := map[string]any{"schema_version": "pause-final-private-relevance-v1", "denominator": len(queries), "available": available, "queries_sha256": domain.Hash(raw), "encoder": e.Encoder, "mapping": investigation.HistoryMapping, "fusion": investigation.FusionVersion, "template": domain.HistoryTemplate, "rows": rows, "scope_violations": 0, "revoked_stale_hit_suppressed": true, "scope": "Ten authored synthetic queries, two authored prior sources; not a real-world relevance estimate. Query text/gold never indexed; separate from three P04 development probes. All unavailable attempts remain in denominator."}
	data, _ := json.MarshalIndent(report, "", "  ")
	path := os.Getenv("P06_PRIVATE_REPORT")
	if path == "" {
		path = "../../reports/private-retrieval-final.json"
	}
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if _, err = out.Write(append(data, '\n')); err != nil {
		t.Fatal(err)
	}
}

func actorContact() string { return store.SeedID("contact-subject-1") }
