package domain

import (
	"context"
	"errors"
	"math"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const ContractVersion = "pause-evidence-v1"

var Labels = []string{"urgent_payment_pressure", "secrecy_pressure", "impersonation_style_request", "advance_fee_or_release_fee"}
var hashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type InferenceStatus string

const (
	InferenceOK InferenceStatus = "OK"
	Abstain     InferenceStatus = "ABSTAIN"
	Unsupported InferenceStatus = "UNSUPPORTED"
	Unavailable InferenceStatus = "UNAVAILABLE"
)

type ClassifierManifest struct {
	SchemaVersion        string             `json:"schema_version"`
	Status               string             `json:"status"`
	ModelVersion         string             `json:"model_version"`
	ArtifactHash         string             `json:"artifact_hash"`
	LabelSchemaVersion   string             `json:"label_schema_version"`
	PreprocessingVersion string             `json:"preprocessing_version"`
	Labels               []string           `json:"labels"`
	Thresholds           map[string]float64 `json:"thresholds"`
	DatasetHash          string             `json:"dataset_hash"`
	SplitHash            string             `json:"split_hash"`
	Seed                 int64              `json:"seed"`
	DependencyLockHash   string             `json:"dependency_lock_hash"`
	CodeRevision         string             `json:"code_revision"`
	Language             string             `json:"language"`
}

func (m ClassifierManifest) Validate() error {
	if m.SchemaVersion != ContractVersion || m.Status != "RELEASED" || m.ModelVersion == "" || !hashPattern.MatchString(m.ArtifactHash) || !hashPattern.MatchString(m.DatasetHash) || !hashPattern.MatchString(m.SplitHash) || !hashPattern.MatchString(m.DependencyLockHash) || m.CodeRevision == "" || m.Language != "en" || m.LabelSchemaVersion != "pause-signals-v1" || m.PreprocessingVersion == "" || len(m.Labels) != len(Labels) || len(m.Thresholds) != len(Labels) {
		return errors.New("invalid classifier manifest")
	}
	for i, l := range Labels {
		v, ok := m.Thresholds[l]
		if m.Labels[i] != l || !ok || !finite(v) || v < 0 || v > 1 {
			return errors.New("invalid classifier labels or thresholds")
		}
	}
	return nil
}

type InferenceInput struct {
	IntentID      string `json:"intent_id"`
	IntentVersion int    `json:"intent_version"`
	ContentDigest string `json:"content_digest"`
	Text          string `json:"text"`
	Language      string `json:"language"`
	ModelVersion  string `json:"model_version"`
}
type Classification struct {
	SchemaVersion        string             `json:"schema_version"`
	IntentID             string             `json:"intent_id"`
	IntentVersion        int                `json:"intent_version"`
	ContentDigest        string             `json:"content_digest"`
	ModelVersion         string             `json:"model_version"`
	ArtifactHash         string             `json:"artifact_hash"`
	LabelSchemaVersion   string             `json:"label_schema_version"`
	PreprocessingVersion string             `json:"preprocessing_version"`
	LabelScores          map[string]float64 `json:"label_scores"`
	TriggeredLabels      []string           `json:"triggered_labels"`
	Status               InferenceStatus    `json:"status"`
	DurationMS           int64              `json:"duration_ms"`
}

func (c Classification) Validate(in InferenceInput, m ClassifierManifest) error {
	if _, e := uuid.Parse(in.IntentID); e != nil || in.IntentVersion < 1 || !utf8.ValidString(in.Text) || len(in.Text) < 1 || len(in.Text) > 8000 {
		return errors.New("invalid classifier input")
	}
	if m.Validate() != nil || c.SchemaVersion != ContractVersion || c.IntentID != in.IntentID || c.IntentVersion != in.IntentVersion || c.ContentDigest != in.ContentDigest || !hashPattern.MatchString(c.ContentDigest) || c.ModelVersion != m.ModelVersion || in.ModelVersion != m.ModelVersion || c.ArtifactHash != m.ArtifactHash || c.LabelSchemaVersion != m.LabelSchemaVersion || c.PreprocessingVersion != m.PreprocessingVersion || c.DurationMS < 0 {
		return errors.New("classification binding or artifact mismatch")
	}
	if c.Status != InferenceOK && c.Status != Abstain && c.Status != Unsupported && c.Status != Unavailable {
		return errors.New("invalid classification status")
	}
	if in.Language != m.Language && c.Status != Unsupported && c.Status != Unavailable {
		return errors.New("unsupported classifier language")
	}
	if c.Status == Unsupported || c.Status == Unavailable {
		if len(c.LabelScores) != 0 || len(c.TriggeredLabels) != 0 {
			return errors.New("unavailable classification has scores")
		}
		return nil
	}
	if len(c.LabelScores) != len(Labels) {
		return errors.New("invalid label schema")
	}
	expected := make(map[string]bool)
	for _, l := range Labels {
		v, ok := c.LabelScores[l]
		if !ok || !finite(v) || v < 0 || v > 1 {
			return errors.New("invalid label score")
		}
		if v >= m.Thresholds[l] {
			expected[l] = true
		}
	}
	if len(c.TriggeredLabels) != len(expected) {
		return errors.New("invalid triggered labels")
	}
	for _, l := range c.TriggeredLabels {
		if !expected[l] {
			return errors.New("invalid triggered labels")
		}
		delete(expected, l)
	}
	return nil
}

type EncoderManifest struct {
	SchemaVersion        string `json:"schema_version"`
	Status               string `json:"status"`
	EncoderVersion       string `json:"encoder_version"`
	ArtifactHash         string `json:"artifact_hash"`
	UpstreamRevision     string `json:"upstream_revision"`
	License              string `json:"license"`
	PreprocessingVersion string `json:"preprocessing_version"`
	Dimension            int    `json:"dimension"`
	Normalized           bool   `json:"normalized"`
	Similarity           string `json:"similarity"`
}

func (m EncoderManifest) Validate() error {
	if m.SchemaVersion != ContractVersion || m.Status != "RELEASED" || m.EncoderVersion == "" || !hashPattern.MatchString(m.ArtifactHash) || m.UpstreamRevision == "" || m.License == "" || m.PreprocessingVersion == "" || m.Dimension < 1 || m.Dimension > 4096 || !m.Normalized || m.Similarity != "cosine" {
		return errors.New("invalid encoder manifest")
	}
	return nil
}

type EmbedInput struct {
	Text           string `json:"text"`
	InputDigest    string `json:"input_digest"`
	EncoderVersion string `json:"encoder_version"`
}
type Embedding struct {
	SchemaVersion        string    `json:"schema_version"`
	InputDigest          string    `json:"input_digest"`
	EncoderVersion       string    `json:"encoder_version"`
	ArtifactHash         string    `json:"artifact_hash"`
	PreprocessingVersion string    `json:"preprocessing_version"`
	Normalized           bool      `json:"normalized"`
	Vector               []float64 `json:"vector"`
	DurationMS           int64     `json:"duration_ms"`
}

func (e Embedding) Validate(in EmbedInput, m EncoderManifest) error {
	if m.Validate() != nil || e.SchemaVersion != ContractVersion || e.InputDigest != in.InputDigest || !hashPattern.MatchString(e.InputDigest) || in.EncoderVersion != m.EncoderVersion || e.EncoderVersion != m.EncoderVersion || e.ArtifactHash != m.ArtifactHash || e.PreprocessingVersion != m.PreprocessingVersion || !e.Normalized || len(e.Vector) != m.Dimension || e.DurationMS < 0 {
		return errors.New("embedding artifact or dimension mismatch")
	}
	sum := 0.0
	for _, v := range e.Vector {
		if !finite(v) {
			return errors.New("nonfinite embedding")
		}
		sum += v * v
	}
	if math.Abs(sum-1) > 0.001 {
		return errors.New("embedding not normalized")
	}
	return nil
}

type SearchScope string

const (
	Playbooks SearchScope = "PLAYBOOKS"
	History   SearchScope = "HISTORY"
)

type LookupStatus string

const (
	LookupOK            LookupStatus = "OK"
	LookupUnavailable   LookupStatus = "UNAVAILABLE"
	LookupNotApplicable LookupStatus = "NOT_APPLICABLE"
)

// No client-selected index or raw DSL exists in the contract. The adapter owns
// alias resolution and applies the scope to every lexical/vector branch.
type SearchQuery struct {
	Scope                                          SearchScope
	HouseholdID, SubjectContactID, ExcludeIntentID string
	ConsentVersion                                 int
	Cutoff, Now                                    time.Time
	EncoderVersion                                 string
	Text                                           string
	Vector                                         []float64
	Limit                                          int
}

func (q SearchQuery) Validate() error {
	if q.Limit < 1 || q.Limit > 20 || q.EncoderVersion == "" {
		return errors.New("invalid search bounds")
	}
	if q.Scope == Playbooks {
		return nil
	}
	if q.Scope != History || q.HouseholdID == "" || q.SubjectContactID == "" || q.ExcludeIntentID == "" || q.ConsentVersion < 1 || q.Cutoff.IsZero() || !q.Now.After(q.Cutoff) {
		return errors.New("invalid search scope")
	}
	for _, id := range []string{q.HouseholdID, q.SubjectContactID, q.ExcludeIntentID} {
		if _, e := uuid.Parse(id); e != nil {
			return errors.New("invalid search scope ID")
		}
	}
	return nil
}

type SearchHit struct {
	SourceID           string  `json:"source_id"`
	SourceVersion      int     `json:"source_version"`
	ProjectionRevision int     `json:"projection_revision"`
	ConsentVersion     int     `json:"consent_version"`
	Score              float64 `json:"score"`
}
type SearchResult struct {
	SchemaVersion  string       `json:"schema_version"`
	Scope          SearchScope  `json:"scope"`
	Status         LookupStatus `json:"status"`
	EncoderVersion string       `json:"encoder_version"`
	IndexVersion   string       `json:"index_version"`
	MappingVersion string       `json:"mapping_version"`
	FusionVersion  string       `json:"fusion_version"`
	Hits           []SearchHit  `json:"hits"`
}

func (r SearchResult) Validate(q SearchQuery) error {
	if q.Validate() != nil || r.SchemaVersion != ContractVersion || r.Scope != q.Scope || r.EncoderVersion != q.EncoderVersion || (r.Status != LookupOK && r.Status != LookupUnavailable && r.Status != LookupNotApplicable) || (r.Status == LookupNotApplicable && q.Scope != History) || len(r.Hits) > q.Limit {
		return errors.New("invalid search result")
	}
	if r.Status == LookupOK && (r.IndexVersion == "" || r.MappingVersion == "" || r.FusionVersion == "") {
		return errors.New("missing search versions")
	}
	if r.Status != LookupOK && len(r.Hits) != 0 {
		return errors.New("unavailable search has hits")
	}
	ids := map[string]bool{}
	for _, h := range r.Hits {
		if h.SourceID == "" || ids[h.SourceID] || h.SourceVersion < 1 || !finite(h.Score) || h.Score < 0 || (q.Scope == History && (h.ProjectionRevision < 1 || h.ConsentVersion < 1 || h.SourceID == q.ExcludeIntentID)) {
			return errors.New("invalid search hit")
		}
		ids[h.SourceID] = true
	}
	return nil
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

type ProviderCommand struct {
	IntentID          string
	Version           int
	Digest, RequestID string
	Binding           Binding
	ProviderObjectID  string
}
type ProviderResult struct {
	OrderID, AuthorizationID, CaptureID, Status string
	Confirmed                                   bool
}
type PaymentProvider interface {
	CreateOrder(context.Context, ProviderCommand) (ProviderResult, error)
	Authorize(context.Context, ProviderCommand) (ProviderResult, error)
	Capture(context.Context, ProviderCommand) (ProviderResult, error)
	Void(context.Context, ProviderCommand) (ProviderResult, error)
	Reconcile(context.Context, ProviderCommand) (ProviderResult, error)
}
type ClassifierClient interface {
	Classify(context.Context, InferenceInput) (Classification, error)
}
type EncoderClient interface {
	Embed(context.Context, EmbedInput) (Embedding, error)
}
type SearchClient interface {
	Search(context.Context, SearchQuery) (SearchResult, error)
}
type TemplateFacts struct {
	Binding         Binding
	EvidenceVersion string
	Sources         []SearchHit
}
type TemplateRenderer interface {
	Render(context.Context, string, TemplateFacts) (string, error)
}
type Job struct {
	ID, Kind, ObjectID, LeaseToken string
	Attempts                       int
	LeaseUntil                     time.Time
}
type JobStore interface {
	Lease(context.Context, []string, time.Duration) (*Job, error)
	Complete(context.Context, Job) error
	Retry(context.Context, Job, string, time.Duration) error
}
