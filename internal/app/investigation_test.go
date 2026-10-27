package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ramst/pause/internal/domain"
	"github.com/ramst/pause/internal/investigation"
	"github.com/ramst/pause/internal/store"
)

func taskAnswer(t *testing.T, f *paymentFixture, i domain.Intent) (domain.Task, Answer) {
	t.Helper()
	tasks, e := f.s.Tasks(context.Background(), actor(domain.Subject))
	if e != nil {
		t.Fatal(e)
	}
	for _, task := range tasks {
		if task.IntentID == i.ID {
			return task, Answer{ExpectedVersion: task.Version, Digest: task.Digest, SnapshotHash: task.SnapshotHash, Response: "CONFIRMED"}
		}
	}
	t.Fatal("addressed task missing")
	return domain.Task{}, Answer{}
}

func TestP03RejectedAnswersConsumeAccountRateLimit(t *testing.T) {
	f := setupPayments(t)
	i := f.held(t)
	task, answer := taskAnswer(t, f, i)
	bad := answer
	bad.Digest = strings.Repeat("b", 64)
	for n := 0; n < 20; n++ {
		if err := f.s.RespondVerification(context.Background(), actor(domain.Subject), task.ID, bad, "test"); err == nil {
			t.Fatal("forged answer accepted")
		}
	}
	if err := f.s.RespondVerification(context.Background(), actor(domain.Subject), task.ID, answer, "test"); err == nil || err.Error() != "ANSWER_THROTTLED" {
		t.Fatal("rejected answers failed to throttle account", err)
	}
	var attempts int
	if err := f.s.DB.Pool.QueryRow(context.Background(), `SELECT attempts FROM verification_answer_buckets WHERE user_id=$1`, actor(domain.Subject).UserID).Scan(&attempts); err != nil || attempts != 21 {
		t.Fatal(attempts, err)
	}
	f.clock.now = f.clock.now.Add(time.Minute)
	if err := f.s.RespondVerification(context.Background(), actor(domain.Subject), task.ID, answer, "test"); err != nil {
		t.Fatal("new window failed", err)
	}
}

func TestP03TaskReplayNeverRenewsOrReopens(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		t.Run(map[bool]string{false: "expired", true: "terminal"}[terminal], func(t *testing.T) {
			f := setupPayments(t)
			i := f.held(t)
			task, _ := taskAnswer(t, f, i)
			if terminal {
				if _, err := f.s.Cancel(context.Background(), actor(domain.Senior), i.ID, i.Version, "test"); err != nil {
					t.Fatal(err)
				}
				f.work(t)
			}
			f.clock.now = f.clock.now.Add(16 * time.Minute)
			if err := f.s.RequestVerification(context.Background(), actor(domain.Senior), i.ID, i.Version, "retry"); err != nil {
				t.Fatal(err)
			}
			again, _ := taskAnswer(t, f, i)
			if again.ID != task.ID || !again.ExpiresAt.Equal(task.ExpiresAt) || again.Status != "EXPIRED" {
				t.Fatal("task replay changed identity/TTL", again)
			}
		})
	}
}

// Real PG authority with deterministic dependency HTTP fixtures. Actual weights
// and actual Elastic are tested separately; these cases isolate boundary faults.
func p03Engine(t *testing.T, f *paymentFixture) *string {
	t.Helper()
	e := &investigation.Engine{HTTP: &http.Client{Timeout: time.Second}, CorpusHash: strings.Repeat("c", 64), Corpus: []investigation.Playbook{{ID: "approved", Revision: 1, Title: "Original FTC summary", Summary: "Original reviewed text.", URL: "https://consumer.ftc.gov/articles/scams", Publisher: "Federal Trade Commission", Language: "en", Active: true}}}
	for path, dst := range map[string]any{"../../models/classifier.manifest.json": &e.Classifier, "../../models/encoder.manifest.json": &e.Encoder} {
		raw, err := os.ReadFile(path)
		if err != nil || json.Unmarshal(raw, dst) != nil {
			t.Fatal("manifest", err)
		}
	}
	mode := "valid"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode == "outage" {
			w.WriteHeader(503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var out any
		switch {
		case r.URL.Path == "/readyz":
			out = map[string]any{"status": "OK", "schema_version": domain.ContractVersion, "classifier_artifact_hash": e.Classifier.ArtifactHash, "encoder_artifact_hash": e.Encoder.ArtifactHash}
			if mode == "wrong-ready-artifact" {
				out = map[string]any{"status": "OK", "schema_version": domain.ContractVersion}
			}
		case r.URL.Path == "/classify":
			var in domain.InferenceInput
			_ = json.NewDecoder(r.Body).Decode(&in)
			scores := map[string]float64{}
			for _, label := range domain.Labels {
				scores[label] = 0
			}
			status := domain.InferenceOK
			if mode == "abstain" {
				status = domain.Abstain
			}
			if mode == "unsupported" {
				status = domain.Unsupported
				scores = nil
			}
			out = domain.Classification{SchemaVersion: domain.ContractVersion, IntentID: in.IntentID, IntentVersion: in.IntentVersion, ContentDigest: in.ContentDigest, ModelVersion: e.Classifier.ModelVersion, ArtifactHash: e.Classifier.ArtifactHash, LabelSchemaVersion: e.Classifier.LabelSchemaVersion, PreprocessingVersion: e.Classifier.PreprocessingVersion, LabelScores: scores, TriggeredLabels: []string{}, Status: status}
		case r.URL.Path == "/embed":
			var in domain.EmbedInput
			_ = json.NewDecoder(r.Body).Decode(&in)
			v := make([]float64, 384)
			v[0] = 1
			out = domain.Embedding{SchemaVersion: domain.ContractVersion, InputDigest: in.InputDigest, EncoderVersion: e.Encoder.EncoderVersion, ArtifactHash: e.Encoder.ArtifactHash, PreprocessingVersion: e.Encoder.PreprocessingVersion, Normalized: true, Vector: v}
		case strings.HasSuffix(r.URL.Path, "/_mapping"):
			out = map[string]any{investigation.IndexVersion: map[string]any{"mappings": map[string]any{"_meta": map[string]any{"mapping_version": "playbooks-v1", "encoder_version": e.Encoder.EncoderVersion, "corpus_hash": e.CorpusHash, "ingest_complete": "true"}, "properties": map[string]any{"embedding": map[string]any{"dims": 384, "similarity": "cosine"}}}}}
		case strings.HasSuffix(r.URL.Path, "/_count"):
			out = map[string]any{"count": 1}
		case strings.HasSuffix(r.URL.Path, "/_mget"):
			p := e.Corpus[0]
			if mode == "inactive" {
				p.Active = false
			}
			if mode == "revision" {
				p.Revision++
			}
			if mode == "changed-content" {
				p.Summary = "Unreviewed replacement"
			}
			var doc map[string]any
			_ = json.Unmarshal(mustJSON(p), &doc)
			doc["encoder_version"] = e.Encoder.EncoderVersion
			out = map[string]any{"docs": []any{map[string]any{"_index": investigation.IndexVersion, "_id": p.ID, "found": mode != "deleted", "_source": doc}}}
		case strings.HasSuffix(r.URL.Path, "/_search"):
			hits := []any{}
			if mode != "empty" {
				hits = append(hits, map[string]any{"_index": investigation.IndexVersion, "_id": "approved", "_source": map[string]any{"playbook_id": "approved", "revision": 1, "encoder_version": e.Encoder.EncoderVersion}})
			}
			out = map[string]any{"hits": map[string]any{"hits": hits}}
		default:
			t.Error("unexpected dependency route", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(server.Close)
	e.Config.MLURL = server.URL
	e.Config.ElasticURL = server.URL
	e.Config.PlaybookAlias = "pause-playbooks"
	f.s.Investigation = e
	if err := f.s.ApproveReleases(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &mode
}

func TestP03LiveReferencesAndModelRecheckedBeforeRenderReviewAndDispatch(t *testing.T) {
	for _, mode := range []string{"deleted", "inactive", "revision", "changed-content", "outage", "wrong-ready-artifact"} {
		t.Run(mode, func(t *testing.T) {
			f := setupPayments(t)
			i := f.held(t)
			change := p03Engine(t, f)
			task, answer := taskAnswer(t, f, i)
			if err := f.s.RespondVerification(context.Background(), actor(domain.Subject), task.ID, answer, "test"); err != nil {
				t.Fatal(err)
			}
			if _, err := f.s.Analyze(context.Background(), actor(domain.Reviewer), i.ID, 1); err != nil {
				t.Fatal(err)
			}
			packet, err := f.s.Evidence(context.Background(), actor(domain.Reviewer), i.ID)
			if err != nil || len(packet.Missing) > 0 || len(packet.Sources) != 1 {
				t.Fatal("valid packet", err, packet.Missing)
			}
			*change = mode
			if mode != "wrong-ready-artifact" {
				stale, err := f.s.Evidence(context.Background(), actor(domain.Reviewer), i.ID)
				if err != nil || len(stale.Sources) != 0 || stale.LookupStatus != domain.LookupUnavailable {
					t.Fatal("stale source rendered", err, stale.Sources)
				}
			}
			decision := ReviewDecision{ExpectedVersion: 1, EvidenceID: packet.ID, EvidenceHash: packet.Hash, Decision: "APPROVED", Rationale: "Checked exact enrolled response."}
			if err := f.s.Review(context.Background(), actor(domain.Reviewer), i.ID, decision, "test"); err == nil {
				t.Fatal("stale dependency approved")
			}
			*change = "valid"
			if err := f.s.Review(context.Background(), actor(domain.Reviewer), i.ID, decision, "test"); err != nil {
				t.Fatal("valid review", err)
			}
			*change = mode
			f.work(t)
			if f.p.calls["CAPTURE"] != 0 || f.status(t, i.ID) != domain.Voided {
				t.Fatal("changed dependency reached capture", f.p.calls, f.status(t, i.ID))
			}
		})
	}
}

func TestP03AdversarialIntakeCannotSupplyAuthority(t *testing.T) {
	var fixtures []struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	}
	raw, err := os.ReadFile("../../tests/fixtures/p03-adversarial.json")
	if err != nil || json.Unmarshal(raw, &fixtures) != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 10 {
		t.Fatal("fixture count", len(fixtures))
	}
	for _, fixture := range fixtures {
		t.Run(fixture.ID, func(t *testing.T) {
			f := setupPayments(t)
			p03Engine(t, f)
			// Same display name deliberately exists on two separately enrolled IDs.
			if _, err := f.s.DB.Pool.Exec(context.Background(), `UPDATE users SET display_name='Alex' WHERE id IN ($1,$2)`, store.SeedID("subject-1"), store.SeedID("subject-2")); err != nil {
				t.Fatal(err)
			}
			no := false
			in := Intake{Amount: "40.01", Currency: "USD", MerchantID: "FIXTURE_MERCHANT", SubjectContactID: store.SeedID("contact-subject-1"), Purpose: "Explicit ordinary supplies", Message: fixture.Message, HistoryConsent: &no}
			i, _, err := f.s.Create(context.Background(), actor(domain.Senior), in, "adversarial-"+fixture.ID, "test")
			if err != nil {
				t.Fatal(err)
			}
			task, answer := taskAnswer(t, f, i)
			if i.Binding.AmountMinor != 4001 || i.Binding.PayeeID != in.MerchantID || i.Binding.Purpose != in.Purpose || task.Binding.SubjectContactID != in.SubjectContactID || task.Binding != i.Binding {
				t.Fatal("message overwrote explicit authority")
			}
			var subject string
			var commands int
			if err := f.s.DB.Pool.QueryRow(context.Background(), `SELECT subject_user_id::text FROM verifications WHERE id=$1`, task.ID).Scan(&subject); err != nil || subject != store.SeedID("subject-1") {
				t.Fatal("attacker selected recipient", subject, err)
			}
			if _, err := f.s.Analyze(context.Background(), actor(domain.Reviewer), i.ID, 1); err != nil {
				t.Fatal(err)
			}
			packet, err := f.s.Evidence(context.Background(), actor(domain.Reviewer), i.ID)
			if err != nil || packet.OriginalMessage != fixture.Message || len(packet.Missing) == 0 {
				t.Fatal("pasted authority accepted", err)
			}
			decision := ReviewDecision{ExpectedVersion: 1, EvidenceID: packet.ID, EvidenceHash: packet.Hash, Decision: "APPROVED", Rationale: "Pasted message claimed approval."}
			if err := f.s.Review(context.Background(), actor(domain.Reviewer), i.ID, decision, "test"); err == nil {
				t.Fatal("text released payment")
			}
			if err := f.s.DB.Pool.QueryRow(context.Background(), `SELECT count(*) FROM payment_operations WHERE intent_id=$1`, i.ID).Scan(&commands); err != nil || commands != 0 {
				t.Fatal("text dispatched financial commands", commands, err)
			}
			if err := f.s.RespondVerification(context.Background(), actor(domain.Reviewer), task.ID, answer, "forged-reviewer"); err == nil {
				t.Fatal("reviewer answered subject task")
			}
			// A real structured correction invalidates the old request's task.
			changed := in
			changed.Purpose = "Changed explicit purchase"
			if _, err := f.s.Correct(context.Background(), actor(domain.Senior), i.ID, Correction{ExpectedVersion: 1, ExpectedConsentVersion: 1, Intake: changed}, "test"); err != nil {
				t.Fatal(err)
			}
			if err := f.s.RespondVerification(context.Background(), actor(domain.Subject), task.ID, answer, "old-answer"); err == nil {
				t.Fatal("old version answer accepted")
			}
			if err := f.s.RequestVerification(context.Background(), actor(domain.Senior), i.ID, 2, "replacement-task"); err != nil {
				t.Fatal(err)
			}
			current := i
			current.Version = 2
			replacement, replacementAnswer := taskAnswer(t, f, current)
			if replacement.Version != 2 || replacement.ID == task.ID || replacement.Binding.Purpose != changed.Purpose {
				t.Fatal("replacement task lost corrected binding")
			}
			if err := f.s.RespondVerification(context.Background(), actor(domain.Subject), replacement.ID, answer, "replayed-old-answer"); err == nil {
				t.Fatal("old answer authorized replacement task")
			}
			if err := f.s.RespondVerification(context.Background(), actor(domain.Subject), replacement.ID, replacementAnswer, "current-answer"); err != nil {
				t.Fatal("current replacement task unreachable", err)
			}
		})
	}
}

func TestP03AbstentionEmptyAndUnsupportedEligibility(t *testing.T) {
	for _, mode := range []string{"valid", "empty", "abstain", "unsupported"} {
		t.Run(mode, func(t *testing.T) {
			f := setupPayments(t)
			i := f.held(t)
			change := p03Engine(t, f)
			*change = mode
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
			decision := ReviewDecision{ExpectedVersion: 1, EvidenceID: packet.ID, EvidenceHash: packet.Hash, Decision: "APPROVED", Rationale: ""}
			if err := f.s.Review(context.Background(), actor(domain.Reviewer), i.ID, decision, "test"); err == nil {
				t.Fatal("missing rationale accepted")
			}
			decision.Rationale = "Explicitly reviewed uncertainty and enrolled account response."
			err = f.s.Review(context.Background(), actor(domain.Reviewer), i.ID, decision, "test")
			if mode == "unsupported" {
				if err == nil {
					t.Fatal("unsupported released")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := f.s.Review(context.Background(), actor(domain.Reviewer), i.ID, decision, "replayed-review"); err == nil || err.Error() != "REVIEW_ALREADY_RECORDED" {
				t.Fatal("review replay not explicit conflict", err)
			}
			f.work(t)
			if f.status(t, i.ID) != domain.Captured || f.p.calls["CAPTURE"] != 1 {
				t.Fatal("eligible capture failed", f.p.calls)
			}
			if mode == "abstain" && !packet.Attention {
				t.Fatal("abstention attention absent")
			}
			if mode == "empty" && (packet.LookupStatus != domain.LookupOK || len(packet.Sources) != 0) {
				t.Fatal("empty lookup conflated with outage")
			}
		})
	}
}
func TestP03IndependentSubjectAuthority(t *testing.T) {
	for _, name := range []string{"confirm-no-capture", "deny-void-without-analysis", "wrong-digest", "wrong-version", "wrong-snapshot", "late-answer", "foreign-subject", "senior-cannot-answer", "duplicate-answer", "deduplicated-task"} {
		t.Run(name, func(t *testing.T) {
			f := setupPayments(t)
			i := f.held(t)
			task, answer := taskAnswer(t, f, i)
			who := actor(domain.Subject)
			ctx := context.Background()
			switch name {
			case "deny-void-without-analysis":
				answer.Response = "DENIED"
				outage := p03Engine(t, f)
				*outage = "outage"
			case "wrong-digest":
				answer.Digest = strings.Repeat("b", 64)
			case "wrong-version":
				answer.ExpectedVersion++
			case "wrong-snapshot":
				answer.SnapshotHash = strings.Repeat("c", 64)
			case "late-answer":
				f.clock.now = f.clock.now.Add(16 * time.Minute)
			case "foreign-subject":
				who.UserID = store.SeedID("subject-2")
				who.HouseholdID = store.SeedID("household-2")
			case "senior-cannot-answer":
				who = actor(domain.Senior)
			case "duplicate-answer":
				if e := f.s.RespondVerification(ctx, who, task.ID, answer, "test"); e != nil {
					t.Fatal(e)
				}
			case "deduplicated-task":
				if e := f.s.RequestVerification(ctx, actor(domain.Senior), i.ID, i.Version, "test"); e != nil {
					t.Fatal(e)
				}
			}
			e := f.s.RespondVerification(ctx, who, task.ID, answer, "test")
			valid := name == "confirm-no-capture" || name == "deny-void-without-analysis" || name == "deduplicated-task"
			if valid && e != nil {
				t.Fatal(e)
			}
			if !valid && e == nil {
				t.Fatal("invalid subject response accepted")
			}
			var captures, voids, tasks int
			if e = f.s.DB.Pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE kind='CAPTURE'),count(*) FILTER(WHERE kind='VOID') FROM payment_operations WHERE intent_id=$1`, i.ID).Scan(&captures, &voids); e != nil {
				t.Fatal(e)
			}
			if captures != 0 {
				t.Fatal("subject answer dispatched capture")
			}
			if name == "deny-void-without-analysis" && voids != 1 {
				t.Fatal("denial did not enqueue guarded void", voids)
			}
			if name == "deny-void-without-analysis" {
				f.work(t)
				if f.status(t, i.ID) != domain.Voided {
					t.Fatal("configured dependency outage blocked denial cleanup")
				}
			}
			if e = f.s.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM verifications WHERE intent_id=$1 AND intent_version=$2`, i.ID, i.Version).Scan(&tasks); e != nil || tasks != 1 {
				t.Fatal("verification dedup invariant", tasks, e)
			}
		})
	}
}

func TestP03MissingEngineFailsClosedAtReservationDispatchAndRecovery(t *testing.T) {
	for _, stage := range []string{"reservation", "dispatch", "recovery"} {
		t.Run(stage, func(t *testing.T) {
			f := setupPayments(t)
			i := f.held(t)
			evidence := f.evidence(t, i)
			if stage != "reservation" {
				if err := f.s.ReviewApprove(context.Background(), actor(domain.Reviewer), i.ID, evidence, "Fixture policy review", "test"); err != nil {
					t.Fatal(err)
				}
			}
			f.s.sourceValidator = nil
			if stage == "reservation" {
				if err := f.s.Review(context.Background(), actor(domain.Reviewer), i.ID, ReviewDecision{ExpectedVersion: 1, EvidenceID: evidence, Decision: "APPROVED", Rationale: "Explicit review"}, "test"); err == nil {
					t.Fatal("missing engine accepted product review")
				}
				if err := f.s.ReviewApprove(context.Background(), actor(domain.Reviewer), i.ID, evidence, "Explicit review", "test"); err == nil {
					t.Fatal("missing engine reserved capture")
				}
				return
			}
			if stage == "recovery" {
				job, err := (&store.Jobs{DB: f.s.DB, Clock: f.clock}).Lease(context.Background(), []string{"PAYMENT_OPERATION"}, time.Minute)
				if err != nil || job == nil {
					t.Fatal(job, err)
				}
				o, err := f.s.loadOperation(context.Background(), job.ObjectID)
				if err != nil {
					t.Fatal(err)
				}
				if err := f.s.captureRecoveryEligible(context.Background(), *job, o); err == nil {
					t.Fatal("missing engine allowed capture recovery")
				}
				return
			}
			f.work(t)
			if f.p.calls["CAPTURE"] != 0 || f.status(t, i.ID) != domain.Voided {
				t.Fatal("missing engine dispatched capture", f.p.calls)
			}
		})
	}
}
func TestP03TaskBindsExactMerchantPurchase(t *testing.T) {
	f := setupPayments(t)
	i := f.held(t)
	task, _ := taskAnswer(t, f, i)
	if task.Binding != i.Binding || task.Digest != i.Digest || task.SnapshotHash == "" || task.CreatedAt.IsZero() {
		t.Fatal("task lost exact binding")
	}
	if !task.ExpiresAt.Before(i.DecisionDeadline) {
		t.Fatal("verification TTL not shorter than application deadline")
	}
	other := actor(domain.Subject)
	other.UserID = store.SeedID("subject-2")
	other.HouseholdID = store.SeedID("household-2")
	tasks, e := f.s.Tasks(context.Background(), other)
	if e != nil {
		t.Fatal(e)
	}
	for _, x := range tasks {
		if x.ID == task.ID {
			t.Fatal("cross-household task disclosure")
		}
	}
}
func TestP03EvidenceHashSurvivesPostgresJSONBAndRejectsTampering(t *testing.T) {
	f := setupPayments(t)
	value := map[string]any{"classification": domain.Classification{SchemaVersion: domain.ContractVersion, LabelScores: map[string]float64{"urgent_payment_pressure": .123456789}, Status: domain.Abstain}, "nested": map[string]any{"z": 1, "a": 2}}
	raw := mustJSON(value)
	expected := evidenceHash(raw)
	var returned []byte
	if e := f.s.DB.Pool.QueryRow(context.Background(), `SELECT $1::jsonb`, raw).Scan(&returned); e != nil {
		t.Fatal(e)
	}
	if expected == "" || evidenceHash(returned) != expected {
		t.Fatal("evidence hash changed across JSONB", string(raw), string(returned))
	}
	var changed []byte
	if e := f.s.DB.Pool.QueryRow(context.Background(), `SELECT jsonb_set($1::jsonb,'{classification,status}','"OK"')`, raw).Scan(&changed); e != nil {
		t.Fatal(e)
	}
	if evidenceHash(changed) == expected {
		t.Fatal("tampered evidence retained hash")
	}
}
