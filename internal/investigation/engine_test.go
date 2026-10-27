package investigation

import (
	"context"
	"encoding/json"
	"github.com/ramst/pause/internal/config"
	"github.com/ramst/pause/internal/domain"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPublicBranchesScopeFusionAndFailureDistinctions(t *testing.T) {
	for _, mode := range []string{"hits", "empty", "vector-timeout", "foreign-index", "wrong-revision", "wrong-space", "unapproved-id", "partial-shard", "alias-mismatch"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			e := &Engine{Encoder: domain.EncoderManifest{EncoderVersion: "encoder-test", Dimension: 384}, CorpusHash: "corpus-test", Corpus: []Playbook{{ID: "approved", Revision: 1}}, HTTP: &http.Client{}}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(r.URL.Path, "/_mapping") {
					index := IndexVersion
					if mode == "alias-mismatch" {
						index = "private-history-index"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{index: map[string]any{"mappings": map[string]any{"_meta": map[string]any{"mapping_version": "playbooks-v1", "encoder_version": "encoder-test", "corpus_hash": "corpus-test", "ingest_complete": "true"}, "properties": map[string]any{"embedding": map[string]any{"dims": 384, "similarity": "cosine"}}}}})
					return
				}
				if strings.HasSuffix(r.URL.Path, "/_count") {
					_ = json.NewEncoder(w).Encode(map[string]any{"count": 1})
					return
				}
				calls++
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("bad query")
				}
				raw, _ := json.Marshal(body)
				for _, filter := range []string{`"language":"en"`, `"active":true`, `"encoder_version":"encoder-test"`} {
					if !strings.Contains(string(raw), filter) {
						t.Error("branch missing mandatory filter", filter)
					}
				}
				if calls == 1 && body["query"] == nil {
					t.Error("keyword query missing")
				}
				if calls == 2 && body["knn"] == nil {
					t.Error("vector query missing")
				}
				index, id, revision, encoder := IndexVersion, "approved", 1, "encoder-test"
				if mode == "foreign-index" {
					index = "private-history-index"
				}
				if mode == "wrong-revision" {
					revision = 2
				}
				if mode == "wrong-space" {
					encoder = "other-space"
				}
				if mode == "unapproved-id" {
					id = "attacker"
				}
				hits := []any{}
				if mode != "empty" {
					hits = append(hits, map[string]any{"_index": index, "_id": id, "_source": map[string]any{"playbook_id": id, "revision": revision, "encoder_version": encoder}})
				}
				failed := 0
				if mode == "partial-shard" {
					failed = 1
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"timed_out": mode == "vector-timeout" && calls == 2, "_shards": map[string]any{"failed": failed}, "hits": map[string]any{"hits": hits}})
			}))
			defer server.Close()
			e.Config = config.Config{ElasticURL: server.URL, PlaybookAlias: "pause-playbooks"}
			result, receipt, err := e.public(context.Background(), domain.SearchQuery{Scope: domain.Playbooks, EncoderVersion: "encoder-test", Text: "synthetic development probe", Vector: make([]float64, 384), Limit: 3})
			valid := mode == "hits" || mode == "empty"
			if valid && (err != nil || result.Status != domain.LookupOK) {
				t.Fatal(result, err)
			}
			if !valid && (err == nil || result.Status != domain.LookupUnavailable) {
				t.Fatal("invalid result accepted", result, err)
			}
			if mode == "empty" && len(result.Hits) != 0 {
				t.Fatal("empty lookup invented hits")
			}
			if mode == "hits" && (len(result.Hits) != 1 || receipt.Candidates[0].KeywordRank != 1 || receipt.Candidates[0].VectorRank != 1) {
				t.Fatal("rank receipt missing", receipt)
			}
		})
	}
}

func TestPartialIngestRetryPublishesOnlyCompleteReviewedCorpus(t *testing.T) {
	t.Chdir("../..")
	e, err := New(config.Config{ClassifierManifest: "models/classifier.manifest.json", EncoderManifest: "models/encoder.manifest.json"})
	if err != nil {
		t.Fatal(err)
	}
	e.Corpus = e.Corpus[:2]
	var mapping map[string]any
	documents := map[string]map[string]any{}
	failSecond := true
	aliasSwitches := 0
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		switch {
		case path == "/embed":
			var in domain.EmbedInput
			_ = json.NewDecoder(r.Body).Decode(&in)
			v := make([]float64, 384)
			v[0] = 1
			_ = json.NewEncoder(w).Encode(domain.Embedding{SchemaVersion: domain.ContractVersion, InputDigest: in.InputDigest, EncoderVersion: e.Encoder.EncoderVersion, ArtifactHash: e.Encoder.ArtifactHash, PreprocessingVersion: e.Encoder.PreprocessingVersion, Normalized: true, Vector: v})
		case path == "/"+IndexVersion && r.Method == "GET":
			if mapping == nil {
				w.WriteHeader(404)
			} else {
				_ = json.NewEncoder(w).Encode(map[string]any{IndexVersion: mapping})
			}
		case path == "/"+IndexVersion && r.Method == "PUT":
			_ = json.NewDecoder(r.Body).Decode(&mapping)
			_ = json.NewEncoder(w).Encode(map[string]any{"acknowledged": true})
		case strings.Contains(path, "/_doc/"):
			id := path[strings.LastIndex(path, "/")+1:]
			writes++
			if failSecond && id == e.Corpus[1].ID {
				w.WriteHeader(503)
				return
			}
			var doc map[string]any
			_ = json.NewDecoder(r.Body).Decode(&doc)
			documents[id] = doc
			_ = json.NewEncoder(w).Encode(map[string]any{"result": "created"})
		case strings.HasSuffix(path, "/_mget"):
			if r.URL.Query().Get("_source_excludes") != "embedding" {
				t.Error("mget must exclude vectors via supported query parameter")
			}
			var request struct {
				IDs []string `json:"ids"`
			}
			_ = json.NewDecoder(r.Body).Decode(&request)
			docs := []any{}
			for _, id := range request.IDs {
				doc, found := documents[id]
				docs = append(docs, map[string]any{"_index": IndexVersion, "_id": id, "found": found, "_source": doc})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"docs": docs})
		case strings.HasSuffix(path, "/_count"):
			_ = json.NewEncoder(w).Encode(map[string]any{"count": len(documents)})
		case strings.HasSuffix(path, "/_mapping") && r.Method == "PUT":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			mapping["mappings"].(map[string]any)["_meta"] = body["_meta"]
			_ = json.NewEncoder(w).Encode(map[string]any{"acknowledged": true})
		case strings.HasSuffix(path, "/_mapping"):
			_ = json.NewEncoder(w).Encode(map[string]any{IndexVersion: mapping})
		case path == "/_aliases":
			aliasSwitches++
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			raw, _ := json.Marshal(body)
			if strings.Contains(string(raw), "history") {
				t.Error("public ingest changed private alias")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"acknowledged": true})
		default:
			t.Error("unexpected setup route", r.Method, path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	e.Config.MLURL = server.URL
	e.Config.ElasticURL = server.URL
	e.Config.PlaybookAlias = "pause-playbooks"
	if err := e.Ingest(context.Background()); err == nil {
		t.Fatal("partial ingest succeeded")
	}
	if aliasSwitches != 0 || len(documents) != 1 || e.verifyIndex(context.Background()) == nil {
		t.Fatal("partial index published", aliasSwitches, len(documents))
	}
	failSecond = false
	if err := e.Ingest(context.Background()); err != nil {
		t.Fatal("retry failed", err)
	}
	if aliasSwitches != 1 || len(documents) != 2 || e.verifyIndex(context.Background()) != nil {
		t.Fatal("complete index not ready")
	}
	// Extra documents cannot silently join the supposedly immutable corpus.
	documents["unreviewed"] = map[string]any{}
	if e.Ingest(context.Background()) == nil || aliasSwitches != 1 {
		t.Fatal("extra document approved")
	}
	delete(documents, "unreviewed")
	before := writes
	mapping["mappings"].(map[string]any)["_meta"].(map[string]any)["corpus_hash"] = "other-corpus"
	if e.Ingest(context.Background()) == nil || writes != before || aliasSwitches != 1 {
		t.Fatal("mixed corpus overwritten or published")
	}
}

func TestInferenceTimeoutIsExplicit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(100 * time.Millisecond):
		}
	}))
	defer server.Close()
	e := &Engine{HTTP: &http.Client{Timeout: 30 * time.Millisecond}, Config: config.Config{MLURL: server.URL}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := e.Classify(ctx, domain.InferenceInput{}); err == nil {
		t.Fatal("timeout became valid negative classification")
	}
}
func TestApprovedEngineRejectsRedirectsAndTamperedInference(t *testing.T) {
	t.Chdir("../..")
	e, err := New(config.Config{ClassifierManifest: "models/classifier.manifest.json", EncoderManifest: "models/encoder.manifest.json"})
	if err != nil {
		t.Fatal(err)
	}
	leaked := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true; w.WriteHeader(200) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer redirect.Close()
	if err = e.request(context.Background(), "POST", redirect.URL, "Bearer synthetic-test-only", map[string]string{"text": "synthetic probe"}, nil); err == nil || leaked {
		t.Fatal("dependency redirect followed")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(domain.Classification{Status: domain.InferenceOK})
	}))
	defer server.Close()
	e.Config.MLURL = server.URL
	_, err = e.Classify(context.Background(), domain.InferenceInput{IntentID: "2257436e-4351-4f8d-9886-e8eb670ca720", IntentVersion: 1, ContentDigest: strings.Repeat("a", 64), Text: "Synthetic payment request.", Language: "en", ModelVersion: e.Classifier.ModelVersion})
	if err == nil {
		t.Fatal("tampered inference accepted")
	}
}
