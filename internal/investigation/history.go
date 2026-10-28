package investigation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ramst/pause/internal/domain"
	"github.com/ramst/pause/search"
)

const HistoryIndex = "pause-request-history-v1-minilm1110"
const HistoryMapping = "history-v1"

type PrivateDocument struct {
	Household  string    `json:"household_id"`
	Contact    string    `json:"subject_contact_id"`
	Intent     string    `json:"intent_id"`
	Version    int       `json:"intent_version"`
	Digest     string    `json:"content_digest"`
	Revision   int       `json:"projection_revision"`
	Consent    int       `json:"consent_version"`
	Permission string    `json:"permission_key"`
	Time       time.Time `json:"event_time"`
	Expires    time.Time `json:"expires_at"`
	Purpose    string    `json:"purpose"`
	Text       string    `json:"redacted_text"`
	Amount     int64     `json:"amount_minor"`
	Source     string    `json:"source"`
	Language   string    `json:"language"`
	Deleted    bool      `json:"deleted"`
	Encoder    string    `json:"encoder_version"`
	Vector     []float64 `json:"embedding,omitempty"`
}

func PermissionKey(id string, version, consent, revision int) string {
	return fmt.Sprintf("%s:%d:%d:%d", id, version, consent, revision)
}
func (e *Engine) elasticToken() string {
	if e.Config.ElasticKey != "" {
		return "ApiKey " + e.Config.ElasticKey
	}
	return ""
}

var privateIndexName = regexp.MustCompile(`^pause-request-history-v[0-9]+-[a-z0-9-]+$`)

func (e *Engine) CreateHistoryIndex(ctx context.Context, name string) error {
	if !privateIndexName.MatchString(name) {
		return errors.New("invalid private index name")
	}
	raw, err := search.Assets.ReadFile("mappings/history-v1.json")
	if err != nil {
		return err
	}
	var mapping map[string]any
	if json.Unmarshal(raw, &mapping) != nil {
		return errors.New("invalid private mapping")
	}
	m := mapping["mappings"].(map[string]any)
	m["_meta"] = map[string]any{"mapping_version": HistoryMapping, "encoder_version": e.Encoder.EncoderVersion, "encoder_hash": e.Encoder.ArtifactHash}
	m["properties"].(map[string]any)["embedding"].(map[string]any)["dims"] = e.Encoder.Dimension
	var existing any
	err = e.request(ctx, "GET", e.Config.ElasticURL+"/"+name, e.elasticToken(), nil, &existing)
	if err != nil {
		if status, ok := err.(dependencyStatus); !ok || status != 404 {
			return err
		}
		if err = e.request(ctx, "PUT", e.Config.ElasticURL+"/"+name, e.elasticToken(), mapping, nil); err != nil {
			return err
		}
	}
	_, err = e.HistoryIndex(ctx, name)
	return err
}

func (e *Engine) HistoryIndex(ctx context.Context, target string) (string, error) {
	var mapping map[string]struct {
		Mappings struct {
			Dynamic    string                     `json:"dynamic"`
			Meta       map[string]string          `json:"_meta"`
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"mappings"`
	}
	if target != e.Config.HistoryAlias && !privateIndexName.MatchString(target) {
		return "", errors.New("invalid history target")
	}
	if err := e.request(ctx, "GET", e.Config.ElasticURL+"/"+target+"/_mapping", e.elasticToken(), nil, &mapping); err != nil {
		return "", err
	}
	if len(mapping) != 1 {
		return "", errors.New("ambiguous private index")
	}
	for name, m := range mapping {
		var v struct {
			Dims       int    `json:"dims"`
			Similarity string `json:"similarity"`
		}
		if !privateIndexName.MatchString(name) || m.Mappings.Dynamic != "strict" || m.Mappings.Meta["mapping_version"] != HistoryMapping || m.Mappings.Meta["encoder_version"] != e.Encoder.EncoderVersion || m.Mappings.Meta["encoder_hash"] != e.Encoder.ArtifactHash || json.Unmarshal(m.Mappings.Properties["embedding"], &v) != nil || v.Dims != e.Encoder.Dimension || v.Similarity != "cosine" {
			return "", errors.New("private mapping or encoder mismatch")
		}
		for _, field := range []string{"household_id", "subject_contact_id", "permission_key", "intent_id", "deleted", "expires_at", "event_time", "language", "encoder_version"} {
			if len(m.Mappings.Properties[field]) == 0 {
				return "", errors.New("private scope mapping missing")
			}
		}
		var expected struct {
			Mappings struct {
				Properties map[string]map[string]any `json:"properties"`
			} `json:"mappings"`
		}
		raw, _ := search.Assets.ReadFile("mappings/history-v1.json")
		if json.Unmarshal(raw, &expected) != nil {
			return "", errors.New("private mapping unavailable")
		}
		expected.Mappings.Properties["embedding"]["dims"] = float64(e.Encoder.Dimension)
		for field, want := range expected.Mappings.Properties {
			var got map[string]any
			if json.Unmarshal(m.Mappings.Properties[field], &got) != nil || !mappingContains(got, want) {
				return "", errors.New("private field mapping mismatch")
			}
		}
		return name, nil
	}
	return "", errors.New("private index unavailable")
}
func (e *Engine) SwitchHistory(ctx context.Context, name string) error {
	if _, err := e.HistoryIndex(ctx, name); err != nil {
		return err
	}
	return e.request(ctx, "POST", e.Config.ElasticURL+"/_aliases", e.elasticToken(), map[string]any{"actions": []any{map[string]any{"remove": map[string]any{"index": "pause-request-history-v*", "alias": e.Config.HistoryAlias, "must_exist": false}}, map[string]any{"add": map[string]any{"index": name, "alias": e.Config.HistoryAlias}}}}, nil)
}

// A durable sparse tombstone replaces text and vector. Unlike HTTP DELETE's
// short-lived version cache it continues to reject delayed lower revisions.
func (e *Engine) WriteHistory(ctx context.Context, index string, d PrivateDocument) error {
	if !privateIndexName.MatchString(index) || d.Intent == "" || d.Revision < 1 {
		return errors.New("invalid projection")
	}
	var body any = d
	if d.Deleted {
		body = map[string]any{"intent_id": d.Intent, "projection_revision": d.Revision, "deleted": true}
	} else {
		if len(d.Vector) != e.Encoder.Dimension || d.Encoder != e.Encoder.EncoderVersion {
			return errors.New("private vector mismatch")
		}
		sum := 0.0
		for _, v := range d.Vector {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return errors.New("invalid vector")
			}
			sum += v * v
		}
		if math.Abs(sum-1) > .001 {
			return errors.New("invalid vector norm")
		}
	}
	// Even revisions are upserts; the following odd revision is a tombstone for
	// the same canonical revision (e.g. expiry racing a prepared embedding).
	version := d.Revision * 2
	if d.Deleted {
		version++
	}
	err := e.request(ctx, "PUT", fmt.Sprintf("%s/%s/_doc/%s?version=%d&version_type=external_gte&refresh=wait_for", e.Config.ElasticURL, index, d.Intent, version), e.elasticToken(), body, nil)
	if status, ok := err.(dependencyStatus); ok && status == 409 {
		return nil
	} // newer canonical projection already won
	return err
}
func (e *Engine) RetrieveHistory(ctx context.Context, q domain.SearchQuery, permissions []string) (domain.SearchResult, Receipt, error) {
	started := time.Now()
	r := domain.SearchResult{SchemaVersion: domain.ContractVersion, Scope: domain.History, Status: domain.LookupUnavailable, EncoderVersion: e.Encoder.EncoderVersion, MappingVersion: HistoryMapping, FusionVersion: FusionVersion, Hits: []domain.SearchHit{}}
	receipt := Receipt{InputDigest: domain.Hash([]byte(q.Text)), Candidates: []Candidate{}}
	if q.Validate() != nil || q.Scope != domain.History || q.EncoderVersion != e.Encoder.EncoderVersion {
		return r, receipt, errors.New("invalid private query")
	}
	index, err := e.HistoryIndex(ctx, e.Config.HistoryAlias)
	if err != nil {
		return r, receipt, err
	}
	r.IndexVersion = index
	receipt.ConcreteIndex = index
	in := domain.EmbedInput{Text: q.Text, InputDigest: receipt.InputDigest, EncoderVersion: q.EncoderVersion}
	emb, err := e.Embed(ctx, in)
	if err != nil {
		return r, receipt, err
	}
	consents := []int{}
	permitted := map[string]bool{}
	for _, key := range permissions {
		parts := strings.Split(key, ":")
		if len(parts) != 4 {
			return r, receipt, errors.New("invalid permission key")
		}
		v, err := strconv.Atoi(parts[2])
		if err != nil || v < 1 {
			return r, receipt, errors.New("invalid permission revision")
		}
		consents = append(consents, v)
		permitted[key] = true
	}
	filters := []any{map[string]any{"term": map[string]any{"household_id": q.HouseholdID}}, map[string]any{"term": map[string]any{"subject_contact_id": q.SubjectContactID}}, map[string]any{"term": map[string]any{"language": "en"}}, map[string]any{"term": map[string]any{"encoder_version": q.EncoderVersion}}, map[string]any{"term": map[string]any{"deleted": false}}, map[string]any{"terms": map[string]any{"consent_version": consents}}, map[string]any{"terms": map[string]any{"permission_key": permissions}}, map[string]any{"range": map[string]any{"event_time": map[string]any{"gte": q.Cutoff, "lte": q.Now}}}, map[string]any{"range": map[string]any{"expires_at": map[string]any{"gt": q.Now}}}}
	scoped := map[string]any{"filter": filters, "must_not": []any{map[string]any{"term": map[string]any{"intent_id": q.ExcludeIntentID}}}}
	fields := []string{"intent_id", "intent_version", "projection_revision", "consent_version", "encoder_version"}
	lexicalScope := map[string]any{}
	for k, v := range scoped {
		lexicalScope[k] = v
	}
	lexicalScope["must"] = map[string]any{"multi_match": map[string]any{"query": q.Text, "fields": []string{"purpose^2", "redacted_text"}, "minimum_should_match": "2<50%"}}
	branches := []any{map[string]any{"size": 20, "_source": fields, "query": map[string]any{"bool": lexicalScope}}, map[string]any{"size": 20, "_source": fields, "knn": map[string]any{"field": "embedding", "query_vector": emb.Vector, "k": 20, "num_candidates": 100, "similarity": 0.35, "filter": map[string]any{"bool": scoped}}}}
	type hit struct {
		Index  string          `json:"_index"`
		ID     string          `json:"_id"`
		Source PrivateDocument `json:"_source"`
	}
	scores := map[string]float64{}
	docs := map[string]PrivateDocument{}
	candidates := map[string]*Candidate{}
	for branch, body := range branches {
		var response struct {
			TimedOut bool `json:"timed_out"`
			Shards   struct {
				Failed int `json:"failed"`
			} `json:"_shards"`
			Hits struct {
				Hits []hit `json:"hits"`
			} `json:"hits"`
		}
		if err = e.request(ctx, "POST", e.Config.ElasticURL+"/"+index+"/_search", e.elasticToken(), body, &response); err != nil {
			return r, receipt, err
		}
		if response.TimedOut || response.Shards.Failed > 0 {
			return r, receipt, errors.New("partial private search")
		}
		seen := map[string]bool{}
		for rank, h := range response.Hits.Hits {
			if h.Index != index || h.ID != h.Source.Intent || h.Source.Encoder != q.EncoderVersion || seen[h.ID] || !permitted[PermissionKey(h.ID, h.Source.Version, h.Source.Consent, h.Source.Revision)] {
				return r, receipt, errors.New("private source mismatch")
			}
			seen[h.ID] = true
			if old, ok := docs[h.ID]; ok && (old.Revision != h.Source.Revision || old.Version != h.Source.Version || old.Consent != h.Source.Consent) {
				return r, receipt, errors.New("private projection changed during query")
			}
			docs[h.ID] = h.Source
			scores[h.ID] += 1 / float64(61+rank)
			if candidates[h.ID] == nil {
				candidates[h.ID] = &Candidate{ID: h.ID}
			}
			if branch == 0 {
				candidates[h.ID].KeywordRank = rank + 1
			} else {
				candidates[h.ID].VectorRank = rank + 1
			}
		}
	}
	ids := []string{}
	for id := range scores {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if scores[ids[i]] == scores[ids[j]] {
			return ids[i] < ids[j]
		}
		return scores[ids[i]] > scores[ids[j]]
	})
	for n, id := range ids {
		receipt.Candidates = append(receipt.Candidates, *candidates[id])
		if n < q.Limit {
			d := docs[id]
			r.Hits = append(r.Hits, domain.SearchHit{SourceID: id, SourceVersion: d.Version, ProjectionRevision: d.Revision, ConsentVersion: d.Consent, Score: scores[id]})
		}
	}
	r.Status = domain.LookupOK
	receipt.DurationMS = time.Since(started).Milliseconds()
	return r, receipt, r.Validate(q)
}
