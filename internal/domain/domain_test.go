package domain

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

func TestExactMoney(t *testing.T) {
	for text, want := range map[string]int64{"40.00": 4000, "180": 18000, "900.01": 90001, "0.01": 1, "5000.00": 500000, "00040.0": 4000} {
		got, e := ParseUSD(text, "USD", 500000)
		if e != nil || got != want {
			t.Errorf("%s = %d, %v; want %d", text, got, e, want)
		}
	}
	for _, text := range []string{"0", "0.00", "-1", "+1", "5000.01", "1.001", "1e2", "1E2", "9999999999999999999999999999999999", "92233720368547758.08", ".01", "1.", " 40", "40 ", "NaN", "Inf", "1,000", "１２", ""} {
		if _, e := ParseUSD(text, "USD", 500000); e == nil {
			t.Errorf("accepted %q", text)
		}
	}
	if _, e := ParseUSD("40", "EUR", 500000); e == nil {
		t.Fatal("accepted unsupported currency")
	}
	if _, e := ParseUSD("40", "USD", 3999); e == nil {
		t.Fatal("ignored configured cap")
	}
}
func fixtureBinding() Binding {
	return Binding{IntentID: "3f6e546c-78d5-46c3-a73f-a47fb5aa571d", Version: 1, AmountMinor: 4000, Currency: "USD", PayeeID: "SYNTHETIC_MERCHANT", SubjectContactID: "dbd0c2a2-eb02-48c4-8be0-5fbf5459ffbe", Purpose: "Transport for José"}
}
func TestImmutableCanonicalBinding(t *testing.T) {
	b := fixtureBinding()
	digest, e := b.Digest()
	if e != nil {
		t.Fatal(e)
	}
	n := b
	n.IntentID = strings.ToUpper(n.IntentID)
	n.SubjectContactID = strings.ToUpper(n.SubjectContactID)
	n.Purpose = "  Transport\tfor  Jose\u0301 \n"
	got, e := n.Digest()
	if e != nil || got != digest {
		t.Fatalf("normalization mismatch: %s != %s (%v)", got, digest, e)
	}
	cases := []Binding{b, b, b, b, b, b}
	cases[0].AmountMinor++
	cases[1].PayeeID += "x"
	cases[2].SubjectContactID = "ab83e2a0-a28a-4e0f-b710-47c5c3ee39b4"
	cases[3].Purpose += " today"
	cases[4].Version++
	cases[5].IntentID = "ab83e2a0-a28a-4e0f-b710-47c5c3ee39b4"
	for _, changed := range cases {
		got, e := changed.Digest()
		if e != nil || got == digest {
			t.Fatal("binding field did not affect digest", e)
		}
	}
	canonical, _ := b.Canonical()
	raw, _ := json.Marshal(canonical)
	want := `{"schema":"pause-binding-v1","intent_id":"3f6e546c-78d5-46c3-a73f-a47fb5aa571d","version":1,"amount_minor":4000,"currency":"USD","payee_id":"SYNTHETIC_MERCHANT","subject_contact_id":"dbd0c2a2-eb02-48c4-8be0-5fbf5459ffbe","purpose":"Transport for José"}`
	if string(raw) != want {
		t.Fatalf("binding wire format changed: %s", raw)
	}
	if _, e = NormalizePurpose("ab\x00c"); e == nil {
		t.Fatal("accepted control character")
	}
	rawFixture, e := os.ReadFile("../../tests/fixtures/binding-v1.json")
	if e != nil {
		t.Fatal(e)
	}
	var golden struct {
		Binding Binding `json:"binding"`
		SHA256  string  `json:"sha256"`
	}
	if e = json.Unmarshal(rawFixture, &golden); e != nil {
		t.Fatal(e)
	}
	fixtureDigest, e := golden.Binding.Digest()
	if e != nil || fixtureDigest != golden.SHA256 || fixtureDigest != digest {
		t.Fatal("canonical binding interoperability fixture changed", fixtureDigest, e)
	}
}
func TestLifecycleTransitions(t *testing.T) {
	for _, pair := range [][2]PaymentStatus{{Draft, AwaitingPayer}, {Draft, Abandoned}, {Held, VoidPending}, {Held, CapturePending}, {CapturePending, Reconciling}, {Reconciling, Captured}} {
		if !CanTransition(pair[0], pair[1]) {
			t.Fatal("rejected valid transition", pair)
		}
	}
	for _, pair := range [][2]PaymentStatus{{Draft, Captured}, {Draft, Held}, {Captured, Voided}, {Voided, Captured}, {CapturePending, VoidPending}, {Abandoned, Held}, {Held, Captured}, {"unknown", Held}, {Draft, Draft}} {
		if CanTransition(pair[0], pair[1]) {
			t.Fatal("accepted invalid transition", pair)
		}
	}
}
func classifierFixture() (ClassifierManifest, InferenceInput, Classification) {
	hash := strings.Repeat("a", 64)
	m := ClassifierManifest{SchemaVersion: ContractVersion, Status: "RELEASED", ModelVersion: "fixture-linear-v1", ArtifactHash: hash, LabelSchemaVersion: "pause-signals-v1", PreprocessingVersion: "fixture-pre-v1", Labels: append([]string{}, Labels...), Thresholds: map[string]float64{}, DatasetHash: hash, SplitHash: hash, DependencyLockHash: hash, CodeRevision: "synthetic-fixture", Language: "en"}
	scores := map[string]float64{}
	for _, l := range Labels {
		m.Thresholds[l] = 0.5
		scores[l] = 0.2
	}
	in := InferenceInput{IntentID: fixtureBinding().IntentID, IntentVersion: 1, ContentDigest: hash, Text: "Synthetic fixture only", Language: "en", ModelVersion: m.ModelVersion}
	c := Classification{SchemaVersion: ContractVersion, IntentID: in.IntentID, IntentVersion: 1, ContentDigest: hash, ModelVersion: m.ModelVersion, ArtifactHash: hash, LabelSchemaVersion: m.LabelSchemaVersion, PreprocessingVersion: m.PreprocessingVersion, LabelScores: scores, Status: InferenceOK, TriggeredLabels: []string{}}
	return m, in, c
}
func TestClassifierContractFixtures(t *testing.T) {
	m, in, c := classifierFixture()
	if e := c.Validate(in, m); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*Classification){func(c *Classification) { c.ArtifactHash = "wrong" }, func(c *Classification) { c.IntentVersion++ }, func(c *Classification) { c.SchemaVersion = "v9" }, func(c *Classification) { c.PreprocessingVersion = "wrong" }, func(c *Classification) { c.LabelScores[Labels[0]] = math.NaN() }, func(c *Classification) { c.LabelScores[Labels[0]] = math.Inf(1) }, func(c *Classification) { c.LabelScores[Labels[0]] = 1.01 }, func(c *Classification) { delete(c.LabelScores, Labels[0]); c.LabelScores["fraud_probability"] = 0.5 }, func(c *Classification) { c.TriggeredLabels = []string{"fake_identity"} }, func(c *Classification) { c.Status = "SAFE" }} {
		m, in, c := classifierFixture()
		mutate(&c)
		if c.Validate(in, m) == nil {
			t.Fatal("accepted invalid classifier output")
		}
	}
	c.Status = Unavailable
	c.LabelScores = nil
	if e := c.Validate(in, m); e != nil {
		t.Fatal("unavailable contract rejected", e)
	}
}
func TestEncoderAndSearchContracts(t *testing.T) {
	hash := strings.Repeat("b", 64)
	m := EncoderManifest{SchemaVersion: ContractVersion, Status: "RELEASED", EncoderVersion: "fixture-encoder-v1", ArtifactHash: hash, UpstreamRevision: "pinned-fixture", License: "synthetic", PreprocessingVersion: "fixture-v1", Dimension: 2, Normalized: true, Similarity: "cosine"}
	in := EmbedInput{Text: "synthetic", InputDigest: hash, EncoderVersion: m.EncoderVersion}
	e := Embedding{SchemaVersion: ContractVersion, InputDigest: hash, EncoderVersion: m.EncoderVersion, ArtifactHash: hash, PreprocessingVersion: m.PreprocessingVersion, Normalized: true, Vector: []float64{1, 0}}
	if err := e.Validate(in, m); err != nil {
		t.Fatal(err)
	}
	for _, vector := range [][]float64{{1}, {1, math.NaN()}, {1, math.Inf(-1)}, {0, 0}, {2, 0}} {
		bad := e
		bad.Vector = vector
		if bad.Validate(in, m) == nil {
			t.Fatal("accepted invalid vector", vector)
		}
	}
	bad := e
	bad.ArtifactHash = strings.Repeat("c", 64)
	if bad.Validate(in, m) == nil {
		t.Fatal("accepted wrong encoder artifact")
	}
	q := SearchQuery{Scope: History, HouseholdID: fixtureBinding().IntentID, SubjectContactID: fixtureBinding().SubjectContactID, ExcludeIntentID: fixtureBinding().IntentID, ConsentVersion: 2, Cutoff: time.Now().Add(-time.Hour), Now: time.Now(), EncoderVersion: m.EncoderVersion, Limit: 3}
	r := SearchResult{SchemaVersion: ContractVersion, Scope: History, Status: LookupOK, EncoderVersion: m.EncoderVersion, IndexVersion: "fixture-index-v1", MappingVersion: "fixture-map-v1", FusionVersion: "rrf-fixture-v1", Hits: []SearchHit{{SourceID: "previous", SourceVersion: 1, ProjectionRevision: 3, ConsentVersion: 1, Score: 0.5}}}
	if err := r.Validate(q); err != nil {
		t.Fatal(err)
	}
	r.Hits[0].Score = math.NaN()
	if r.Validate(q) == nil {
		t.Fatal("accepted nonfinite rank")
	}
	r.Hits = nil
	r.Status = LookupUnavailable
	if r.Validate(q) != nil {
		t.Fatal("unavailable search is valid status")
	}
	q.Scope = "arbitrary-client-index"
	if q.Validate() == nil {
		t.Fatal("accepted arbitrary index")
	}
	q.Scope = History
	q.HouseholdID = ""
	if q.Validate() == nil {
		t.Fatal("unscoped private search accepted")
	}
}
func TestUnreleasedManifestsFailClosed(t *testing.T) {
	raw, e := os.ReadFile("../../models/classifier.manifest.json")
	if e != nil {
		t.Fatal(e)
	}
	var classifier ClassifierManifest
	if e = json.Unmarshal(raw, &classifier); e != nil {
		t.Fatal(e)
	}
	if classifier.Validate() != nil {
		t.Fatal("approved classifier manifest invalid")
	}
	classifier.Status = "UNRELEASED"
	if classifier.Validate() == nil {
		t.Fatal("unreleased classifier accepted")
	}
	classifier.Status = "RELEASED"
	classifier.ArtifactHash = "tampered"
	if classifier.Validate() == nil {
		t.Fatal("invalid classifier hash accepted")
	}
	raw, e = os.ReadFile("../../models/encoder.manifest.json")
	if e != nil {
		t.Fatal(e)
	}
	var encoder EncoderManifest
	if e = json.Unmarshal(raw, &encoder); e != nil {
		t.Fatal(e)
	}
	if encoder.Validate() != nil {
		t.Fatal("approved encoder manifest invalid")
	}
	encoder.Status = "UNRELEASED"
	if encoder.Validate() == nil {
		t.Fatal("unreleased encoder accepted")
	}
}
