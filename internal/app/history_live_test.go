package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/ramst/pause/internal/domain"
	"github.com/ramst/pause/internal/investigation"
)

type historyRoundTrip func(*http.Request) (*http.Response, error)

func (fn historyRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func p04LiveEngine(t *testing.T, f *paymentFixture) *investigation.Engine {
	t.Helper()
	if os.Getenv("P04_LIVE_ELASTIC") != "1" {
		t.Skip("P04_LIVE_ELASTIC=1 runs actual local Elastic and approved ML; no mock substitutes")
	}
	env, _ := godotenv.Read("../../.env.p03.local")
	value := func(key string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return env[key]
	}
	var err error
	e := &investigation.Engine{HTTP: &http.Client{Timeout: 10 * time.Second}}
	e.Config.ElasticURL = value("ELASTICSEARCH_URL")
	e.Config.ElasticKey = value("ELASTICSEARCH_API_KEY")
	e.Config.MLURL = value("ML_SERVICE_URL")
	e.Config.MLToken = value("ML_SERVICE_TOKEN")
	e.Config.PlaybookAlias = "pause-playbooks"
	e.Config.HistoryAlias = "pause-request-history-live-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	for path, dst := range map[string]any{"../../models/classifier.manifest.json": &e.Classifier, "../../models/encoder.manifest.json": &e.Encoder, "../../search/playbooks/corpus-v1.json": &e.Corpus} {
		raw, err := os.ReadFile(path)
		if err != nil || json.Unmarshal(raw, dst) != nil {
			t.Fatal("approved release unavailable")
		}
		if strings.Contains(path, "corpus") {
			e.CorpusHash = domain.Hash(raw)
		}
	}
	f.s.Investigation = e
	if err = e.Ready(context.Background()); err != nil {
		t.Fatal("actual inference not ready", err)
	}
	if err = f.s.ApproveReleases(context.Background()); err != nil {
		t.Fatal(err)
	}
	return e
}

// CreateHistoryIndex reads the reviewed mapping relative to the repo. This test
// temporarily supplies that path without changing process-wide cwd.
func liveCreateHistory(t *testing.T, e *investigation.Engine, name string) {
	t.Helper()
	raw, err := os.ReadFile("../../search/mappings/history-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	body["mappings"].(map[string]any)["_meta"] = map[string]any{"mapping_version": investigation.HistoryMapping, "encoder_version": e.Encoder.EncoderVersion, "encoder_hash": e.Encoder.ArtifactHash}
	liveHTTP(t, e, "PUT", "/"+name, body, nil)
	t.Cleanup(func() { liveHTTP(t, e, "DELETE", "/"+name, nil, nil) })
}
func liveHTTP(t *testing.T, e *investigation.Engine, method, path string, body any, out any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(string(mustJSON(body)))
	}
	req, err := http.NewRequest(method, e.Config.ElasticURL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.Config.ElasticKey != "" {
		req.Header.Set("Authorization", "ApiKey "+e.Config.ElasticKey)
	}
	res, err := e.HTTP.Do(req)
	if err != nil {
		t.Fatal("Elastic unavailable", err)
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		t.Fatalf("Elastic %s %s status %d", method, path, res.StatusCode)
	}
	if out != nil {
		if err = json.NewDecoder(res.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
}

func TestP04LivePrivateRetrievalRevocationReindexAndReview(t *testing.T) {
	if os.Getenv("P04_LIVE_ELASTIC") != "1" {
		t.Skip("requires P04_LIVE_ELASTIC=1")
	}
	f := setupPayments(t)
	ctx := context.Background()
	e := p04LiveEngine(t, f)
	index := "pause-request-history-v1-live-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	liveCreateHistory(t, e, index)
	if _, err := f.s.DB.Pool.Exec(ctx, `INSERT INTO history_indexes(name,encoder_version,state,created_at) VALUES($1,$2,'ACTIVE',$3)`, index, e.Encoder.EncoderVersion, f.clock.Now()); err != nil {
		t.Fatal(err)
	}
	if err := e.SwitchHistory(ctx, index); err != nil {
		t.Fatal(err)
	}
	now := f.clock.now
	f.clock.now = now.Add(-2 * 24 * time.Hour)
	prior := p04Request(t, f, "40.00", true, false)
	f.clock.now = now
	current := p04Request(t, f, "900.00", true, true)
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
	var queries []struct{ ID, Text, Expect string }
	raw, err := os.ReadFile("../../tests/fixtures/p04-private-queries.json")
	if err != nil || json.Unmarshal(raw, &queries) != nil {
		t.Fatal(err)
	}
	retrievalEvidence := []map[string]any{}
	for _, q := range queries {
		result, receipt, err := e.RetrieveHistory(ctx, historyQuery(p, h, e.Encoder.EncoderVersion, q.Text), historyPermissions(h))
		if err != nil {
			t.Fatal(err)
		}
		if q.Expect == "permitted_prior_context" && (len(result.Hits) != 1 || result.Hits[0].SourceID != prior.ID) {
			t.Fatal("paraphrase omitted permitted source", result)
		}
		if q.Expect == "successful_empty" && len(result.Hits) != 0 {
			t.Fatal("frozen development no-match returned context", result)
		}
		t.Logf("query=%s status=%s hits=%d branches=%v", q.ID, result.Status, len(result.Hits), receipt.Candidates)
		retrievalEvidence = append(retrievalEvidence, map[string]any{"query_id": q.ID, "status": result.Status, "hit_count": len(result.Hits), "candidates": receipt.Candidates, "index": result.IndexVersion, "fusion": result.FusionVersion, "encoder": result.EncoderVersion})
	}
	// Exact same vector/text cannot bypass branch scoping. Retention, language,
	// contact, household and consent mutations are all tested before top-k fusion.
	doc, err := f.s.projectionDocument(ctx, prior.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"household", "contact", "consent", "expired", "language", "current"} {
		d := doc
		d.Intent = uuid.NewString()
		d.Permission = investigation.PermissionKey(d.Intent, d.Version, d.Consent, d.Revision)
		switch kind {
		case "household":
			d.Household = uuid.NewString()
		case "contact":
			d.Contact = uuid.NewString()
		case "consent":
			d.Consent++
		case "expired":
			d.Expires = now.Add(-time.Second)
		case "language":
			d.Language = "fr"
		case "current":
			d.Intent = current.ID
			d.Permission = investigation.PermissionKey(d.Intent, d.Version, d.Consent, d.Revision)
		}
		if kind == "current" {
			continue
		} // actual indexed current is already an exact candidate
		if err = e.WriteHistory(ctx, index, d); err != nil {
			t.Fatal(err)
		}
		// Include that document's permission deliberately, isolating the other
		// mandatory filters. Consent mismatch keeps the original permitted key.
		permissions := historyPermissions(h)
		if kind != "consent" {
			permissions = append(permissions, d.Permission)
		}
		result, _, err := e.RetrieveHistory(ctx, historyQuery(p, h, e.Encoder.EncoderVersion, queries[0].Text), permissions)
		if err != nil {
			t.Fatal(err)
		}
		for _, hit := range result.Hits {
			if hit.SourceID != prior.ID {
				t.Fatal("scope escaped before hydration", kind, result)
			}
		}
	}
	// Run the actual trained model/public and private Elastic checks, then reject
	// stale review after a new related canonical request.
	task, answer := taskAnswer(t, f, current)
	if err = f.s.RespondVerification(ctx, actor(domain.Subject), task.ID, answer, "live-test"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Analyze(ctx, actor(domain.Reviewer), current.ID, 1); err != nil {
		t.Fatal(err)
	}
	packet, err := f.s.Evidence(ctx, actor(domain.Reviewer), current.ID)
	if err != nil || len(packet.Missing) > 0 || len(packet.History.Matches) != 1 || !patternAttention(packet.History) {
		t.Fatal("real evidence", err, packet.Missing, packet.History)
	}
	var persisted []byte
	if err = f.s.DB.Pool.QueryRow(ctx, `SELECT evidence FROM evidence_snapshots WHERE id=$1`, packet.ID).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(persisted), packet.History.Matches[0].Excerpt) {
		t.Fatal("persisted private excerpt")
	}
	f.clock.now = f.clock.now.Add(time.Second)
	_ = p04Request(t, f, "180.00", true, false)
	decision := ReviewDecision{ExpectedVersion: 1, EvidenceID: packet.ID, EvidenceHash: packet.Hash, Decision: "APPROVED", Rationale: "Read exact confirmation and escalating requests."}
	if err = f.s.Review(ctx, actor(domain.Reviewer), current.ID, decision, "stale-test"); err == nil {
		t.Fatal("stale review captured")
	}
	if f.p.calls["CAPTURE"] != 0 {
		t.Fatal("stale capture dispatched")
	}
	// Revocation wins both stale upsert orderings and duplicate delivery. Text and
	// vector are replaced by a durable sparse tombstone in the actual index.
	no := false
	started := time.Now()
	prepared := make(chan struct{})
	release := make(chan struct{})
	result := make(chan error, 1)
	var once sync.Once
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	delayed := *e
	delayed.HTTP = &http.Client{Timeout: 30 * time.Second, Transport: historyRoundTrip(func(req *http.Request) (*http.Response, error) {
		if req.Method == "PUT" && strings.HasSuffix(req.URL.Path, "/_doc/"+prior.ID) {
			once.Do(func() { close(prepared); <-release })
		}
		return http.DefaultTransport.RoundTrip(req)
	})}
	racingIndex := strings.Replace(index, "v1-", "v4-", 1)
	t.Cleanup(func() { liveHTTP(t, e, "DELETE", "/"+racingIndex, nil, nil) })
	reindexService := *f.s
	reindexService.Investigation = &delayed
	go func() { result <- reindexService.ReindexHistory(ctx, racingIndex) }()
	select {
	case <-prepared:
	case err := <-result:
		t.Fatal("reindex failed before race", err)
	case <-time.After(30 * time.Second):
		t.Fatal("reindex race preparation timed out")
	}
	if _, err = f.s.ChangeConsent(ctx, actor(domain.Senior), prior.ID, ConsentChange{ExpectedVersion: 1, History: &no}, "withdraw-test"); err != nil {
		unblock()
		t.Fatal(err)
	}
	if len(p04History(t, f, current.ID).Rows) != 2 {
		t.Fatal("withdrawn canonical source visible")
	}
	// A failed delete remains pending while PG already denies access. Restore
	// the dependency and retry the durable job without changing permission.
	liveURL := e.Config.ElasticURL
	e.Config.ElasticURL = "http://127.0.0.1:1"
	worked, outageErr := f.s.WorkHistoryOnce(ctx)
	e.Config.ElasticURL = liveURL
	if !worked || outageErr == nil || p04History(t, f, current.ID).PendingDeletes == 0 {
		unblock()
		t.Fatal("cleanup failure was not observable")
	}
	f.clock.now = f.clock.now.Add(2 * time.Second)
	for n := 0; n < 10; n++ {
		worked, err := f.s.WorkHistoryOnce(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			break
		}
	}
	unblock()
	if err = <-result; err != nil {
		t.Fatal(err)
	}
	var raced struct {
		Source map[string]json.RawMessage `json:"_source"`
	}
	liveHTTP(t, e, "GET", "/"+racingIndex+"/_doc/"+prior.ID, nil, &raced)
	if string(raced.Source["deleted"]) != "true" || raced.Source["redacted_text"] != nil || raced.Source["embedding"] != nil {
		t.Fatal("racing reindex retained revoked text/vector")
	}
	if err = e.WriteHistory(ctx, index, doc); err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Source map[string]json.RawMessage `json:"_source"`
	}
	liveHTTP(t, e, "GET", "/"+index+"/_doc/"+prior.ID, nil, &stored)
	if string(stored.Source["deleted"]) != "true" || stored.Source["redacted_text"] != nil || stored.Source["embedding"] != nil {
		t.Fatal("revoked content resurrected")
	}
	if time.Since(started) > 5*time.Minute {
		t.Fatal("healthy cleanup missed five-minute objective")
	}
	cleanupMS := time.Since(started).Milliseconds()
	t.Logf("actual text/vector cleanup_ms=%d", cleanupMS)
	// Building and rollback targets must inherit tombstones and compatible spaces.
	next := strings.Replace(index, "v1-", "v2-", 1)
	t.Cleanup(func() { liveHTTP(t, e, "DELETE", "/"+next, nil, nil) })
	if err = f.s.ReindexHistory(ctx, next); err != nil {
		t.Fatal(err)
	}
	if err = e.WriteHistory(ctx, next, doc); err != nil {
		t.Fatal(err)
	}
	liveHTTP(t, e, "GET", "/"+next+"/_doc/"+prior.ID, nil, &stored)
	if stored.Source["redacted_text"] != nil || string(stored.Source["deleted"]) != "true" {
		t.Fatal("reindex resurrected revoked content")
	}
	if err = f.s.ReindexHistory(ctx, index); err != nil {
		t.Fatal(err)
	}
	wrong := strings.Replace(index, "v1-", "v3-", 1)
	liveCreateHistory(t, e, wrong)
	liveHTTP(t, e, "PUT", "/"+wrong+"/_mapping", map[string]any{"_meta": map[string]any{"mapping_version": investigation.HistoryMapping, "encoder_version": "wrong-space", "encoder_hash": e.Encoder.ArtifactHash}}, nil)
	if err = e.SwitchHistory(ctx, wrong); err == nil {
		t.Fatal("wrong encoder mapping switched alias")
	}
	d := doc
	d.Vector = []float64{1}
	if err = e.WriteHistory(ctx, index, d); err == nil {
		t.Fatal("wrong dimension accepted")
	}
	// Retry the packet against the current complete PG set and explicitly resolve
	// the pattern with confirmation plus rationale. Provider is a unit adapter;
	// this is not claimed as new live PayPal evidence.
	if _, err = f.s.Analyze(ctx, actor(domain.Reviewer), current.ID, 1); err != nil {
		t.Fatal(err)
	}
	packet, err = f.s.Evidence(ctx, actor(domain.Reviewer), current.ID)
	if err != nil || len(packet.Missing) > 0 {
		t.Fatal(err, packet.Missing)
	}
	decision.EvidenceID = packet.ID
	decision.EvidenceHash = packet.Hash
	if err = f.s.Review(ctx, actor(domain.Reviewer), current.ID, decision, "fresh-pattern-review"); err != nil {
		t.Fatal(err)
	}
	f.work(t)
	if f.p.calls["CAPTURE"] != 1 || f.status(t, current.ID) != domain.Captured {
		t.Fatal("pattern resolution failed", f.p.calls, f.status(t, current.ID))
	}
	var reason string
	if err = f.s.DB.Pool.QueryRow(ctx, `SELECT reason_code FROM reviews WHERE intent_id=$1`, current.ID).Scan(&reason); err != nil || reason != "PATTERN_REVIEWED_WITH_RATIONALE" {
		t.Fatal("pattern rationale not recorded", reason, err)
	}
	// Retention has the same immediate PG boundary and physical cleanup as
	// revocation. Advance only the synthetic clock; do not wait an hour.
	f.s.Config.MessageRetention = time.Hour
	expiring := p04Request(t, f, "10.00", true, false)
	if err = f.s.syncHistory(ctx, expiring.ID, nil); err != nil {
		t.Fatal(err)
	}
	expiringDocument, err := f.s.projectionDocument(ctx, expiring.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.clock.now = f.clock.now.Add(time.Hour + time.Second)
	if err = f.s.CleanupMessages(ctx); err != nil {
		t.Fatal(err)
	}
	for _, row := range p04History(t, f, current.ID).Rows {
		if row.IntentID == expiring.ID {
			t.Fatal("expired canonical history visible")
		}
	}
	for n := 0; n < 20; n++ {
		worked, err := f.s.WorkHistoryOnce(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			break
		}
	}
	if err = e.WriteHistory(ctx, index, expiringDocument); err != nil {
		t.Fatal(err)
	}
	liveHTTP(t, e, "GET", "/"+index+"/_doc/"+expiring.ID, nil, &stored)
	if string(stored.Source["deleted"]) != "true" || stored.Source["redacted_text"] != nil || stored.Source["embedding"] != nil {
		t.Fatal("expired snippet/vector restored")
	}
	var cluster struct {
		Version struct {
			Number string `json:"number"`
		} `json:"version"`
	}
	liveHTTP(t, e, "GET", "/", nil, &cluster)
	receipt := map[string]any{"recorded_at_utc": time.Now().UTC(), "scope": "Actual local Elastic and approved inference; PostgreSQL disposable schema; payment adapter is test-only, not new PayPal evidence", "elastic_version": cluster.Version.Number, "classifier_artifact_hash": e.Classifier.ArtifactHash, "encoder_artifact_hash": e.Encoder.ArtifactHash, "queries": retrievalEvidence, "cleanup_ms": cleanupMS, "revocation_replay_rejected": true, "in_flight_upsert_revocation_race_passed": true, "in_flight_reindex_revocation_race_passed": true, "cleanup_failure_pending_then_retried": true, "retention_text_and_vector_cleanup_passed": true, "physical_text_and_vector_absent": true, "fresh_reindex_and_rollback_passed": true, "wrong_encoder_alias_switch_rejected": true, "wrong_dimension_rejected": true, "stale_review_rejected": true, "fresh_pattern_rationale_resolved": true, "worker_outbox_dispatch_passed": true}
	if err = os.MkdirAll("../../data", 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile("../../data/p04-live-receipt.json", append(mustJSON(receipt), '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
