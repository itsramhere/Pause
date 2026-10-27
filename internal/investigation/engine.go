// Package investigation owns bounded private inference and public-only retrieval.
// It has no authentication, enrollment, history indexing or payment capability.
package investigation

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ramst/pause/internal/config"
	"github.com/ramst/pause/internal/domain"
)

const TemplateVersion = "public-evidence-template-v1"
const IndexVersion = "pause-playbooks-v1-minilm1110"
const FusionVersion = "go-rrf60-v1"

type Playbook struct {
	ID        string   `json:"playbook_id"`
	Revision  int      `json:"revision"`
	Title     string   `json:"title"`
	Summary   string   `json:"original_summary"`
	Tags      []string `json:"signal_tags"`
	Phrases   []string `json:"example_phrases"`
	URL       string   `json:"source_url"`
	Publisher string   `json:"source_publisher"`
	Date      *string  `json:"source_date"`
	Reviewed  string   `json:"reviewed_at"`
	Usage     string   `json:"license_or_usage_note"`
	Language  string   `json:"language"`
	Active    bool     `json:"active"`
}
type Candidate struct {
	ID          string `json:"id"`
	KeywordRank int    `json:"keyword_rank"`
	VectorRank  int    `json:"vector_rank"`
}
type Receipt struct {
	ConcreteIndex string      `json:"concrete_index"`
	InputDigest   string      `json:"input_digest"`
	Candidates    []Candidate `json:"candidates"`
	DurationMS    int64       `json:"duration_ms"`
	CorpusHash    string      `json:"corpus_hash"`
}
type Engine struct {
	Config     config.Config
	Classifier domain.ClassifierManifest
	Encoder    domain.EncoderManifest
	Corpus     []Playbook
	CorpusHash string
	HTTP       *http.Client
}

func New(c config.Config) (*Engine, error) {
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if c.ElasticCA != "" {
		raw, err := os.ReadFile(c.ElasticCA)
		if err != nil || !roots.AppendCertsFromPEM(raw) {
			return nil, errors.New("invalid Elastic CA")
		}
	}
	e := &Engine{Config: c, HTTP: &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, MaxConnsPerHost: 4, ResponseHeaderTimeout: 5 * time.Second}}}
	for _, x := range []struct {
		path string
		dst  any
	}{{c.ClassifierManifest, &e.Classifier}, {c.EncoderManifest, &e.Encoder}, {"search/playbooks/corpus-v1.json", &e.Corpus}} {
		raw, err := os.ReadFile(x.path)
		if err != nil {
			return nil, errors.New("approved investigation files unavailable")
		}
		if json.Unmarshal(raw, x.dst) != nil {
			return nil, errors.New("invalid investigation file")
		}
		if strings.Contains(x.path, "corpus") {
			e.CorpusHash = domain.Hash(raw)
		}
	}
	if e.Classifier.Validate() != nil || e.Encoder.Validate() != nil {
		return nil, errors.New("invalid investigation release")
	}
	for _, p := range e.Corpus {
		if p.ID == "" || p.Revision < 1 || !strings.HasPrefix(p.URL, "https://consumer.ftc.gov/") || p.Publisher != "Federal Trade Commission" || p.Language != "en" || !p.Active {
			return nil, errors.New("invalid public source")
		}
	}
	return e, nil
}

type dependencyStatus int

func (d dependencyStatus) Error() string { return fmt.Sprintf("dependency status %d", int(d)) }
func (e *Engine) request(ctx context.Context, method, url, token string, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	res, err := e.HTTP.Do(req)
	if err != nil {
		return errors.New("dependency unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return dependencyStatus(res.StatusCode)
	}
	if out == nil {
		return nil
	}
	dec := json.NewDecoder(io.LimitReader(res.Body, 2<<20))
	return dec.Decode(out)
}
func (e *Engine) Classify(ctx context.Context, in domain.InferenceInput) (domain.Classification, error) {
	var out domain.Classification
	err := e.request(ctx, "POST", e.Config.MLURL+"/classify", "Bearer "+e.Config.MLToken, in, &out)
	if err == nil {
		err = out.Validate(in, e.Classifier)
	}
	return out, err
}
func (e *Engine) Ready(ctx context.Context) error {
	var result struct {
		Status     string `json:"status"`
		Schema     string `json:"schema_version"`
		Classifier string `json:"classifier_artifact_hash"`
		Encoder    string `json:"encoder_artifact_hash"`
	}
	if err := e.request(ctx, "GET", e.Config.MLURL+"/readyz", "Bearer "+e.Config.MLToken, nil, &result); err != nil {
		return err
	}
	if result.Status != "OK" || result.Schema != domain.ContractVersion || result.Classifier != e.Classifier.ArtifactHash || result.Encoder != e.Encoder.ArtifactHash {
		return errors.New("approved inference not ready")
	}
	return nil
}
func (e *Engine) Embed(ctx context.Context, in domain.EmbedInput) (domain.Embedding, error) {
	var out domain.Embedding
	err := e.request(ctx, "POST", e.Config.MLURL+"/embed", "Bearer "+e.Config.MLToken, in, &out)
	if err == nil {
		err = out.Validate(in, e.Encoder)
	}
	return out, err
}

type elasticHit struct {
	Index  string `json:"_index"`
	ID     string `json:"_id"`
	Source struct {
		ID       string `json:"playbook_id"`
		Revision int    `json:"revision"`
		Encoder  string `json:"encoder_version"`
	} `json:"_source"`
}
type elasticResult struct {
	TimedOut bool `json:"timed_out"`
	Shards   struct {
		Failed int `json:"failed"`
	} `json:"_shards"`
	Hits struct {
		Hits []elasticHit `json:"hits"`
	} `json:"hits"`
}

func (e *Engine) public(ctx context.Context, q domain.SearchQuery) (domain.SearchResult, Receipt, error) {
	started := time.Now()
	r := domain.SearchResult{SchemaVersion: domain.ContractVersion, Scope: domain.Playbooks, Status: domain.LookupUnavailable, EncoderVersion: e.Encoder.EncoderVersion, IndexVersion: IndexVersion, MappingVersion: "playbooks-v1", FusionVersion: FusionVersion, Hits: []domain.SearchHit{}}
	receipt := Receipt{InputDigest: domain.Hash([]byte(q.Text)), Candidates: []Candidate{}, CorpusHash: e.CorpusHash, ConcreteIndex: IndexVersion}
	if q.Scope != domain.Playbooks || q.Validate() != nil {
		return r, receipt, errors.New("public search scope required")
	}
	if err := e.verifyIndex(ctx); err != nil {
		return r, receipt, err
	}
	filters := []any{map[string]any{"term": map[string]any{"language": "en"}}, map[string]any{"term": map[string]any{"active": true}}, map[string]any{"term": map[string]any{"encoder_version": q.EncoderVersion}}}
	fields := []string{"playbook_id", "revision", "encoder_version"}
	keyword := map[string]any{"size": 20, "_source": fields, "query": map[string]any{"bool": map[string]any{"filter": filters, "must": map[string]any{"multi_match": map[string]any{"query": q.Text, "fields": []string{"title^2", "original_summary", "example_phrases", "signal_tags"}}}}}}
	vector := map[string]any{"size": 20, "_source": fields, "knn": map[string]any{"field": "embedding", "query_vector": q.Vector, "k": 20, "num_candidates": 100, "filter": map[string]any{"bool": map[string]any{"filter": filters}}}}
	url := e.Config.ElasticURL + "/" + e.Config.PlaybookAlias + "/_search"
	token := ""
	if e.Config.ElasticKey != "" {
		token = "ApiKey " + e.Config.ElasticKey
	}
	var a, b elasticResult
	if err := e.request(ctx, "POST", url, token, keyword, &a); err != nil {
		return r, receipt, err
	}
	if err := e.request(ctx, "POST", url, token, vector, &b); err != nil {
		return r, receipt, err
	}
	if a.TimedOut || b.TimedOut || a.Shards.Failed > 0 || b.Shards.Failed > 0 {
		return r, receipt, errors.New("partial search failure")
	}
	scores := map[string]float64{}
	candidates := map[string]*Candidate{}
	versions := map[string]int{}
	for branch, hits := range [][]elasticHit{a.Hits.Hits, b.Hits.Hits} {
		for rank, h := range hits {
			if h.Index != IndexVersion || h.ID != h.Source.ID || h.Source.Encoder != q.EncoderVersion {
				return r, receipt, errors.New("search source mismatch")
			}
			found := false
			for _, p := range e.Corpus {
				if p.ID == h.ID && p.Revision == h.Source.Revision {
					found = true
					break
				}
			}
			if !found {
				return r, receipt, errors.New("unapproved playbook revision")
			}
			if candidates[h.ID] == nil {
				candidates[h.ID] = &Candidate{ID: h.ID}
			}
			if branch == 0 {
				candidates[h.ID].KeywordRank = rank + 1
			} else {
				candidates[h.ID].VectorRank = rank + 1
			}
			scores[h.ID] += 1 / float64(60+rank+1)
			versions[h.ID] = h.Source.Revision
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
			r.Hits = append(r.Hits, domain.SearchHit{SourceID: id, SourceVersion: versions[id], Score: scores[id]})
		}
	}
	r.Status = domain.LookupOK
	receipt.DurationMS = time.Since(started).Milliseconds()
	return r, receipt, r.Validate(q)
}
func (e *Engine) Retrieve(ctx context.Context, text string) (domain.SearchResult, Receipt, error) {
	in := domain.EmbedInput{Text: text, InputDigest: domain.Hash([]byte(text)), EncoderVersion: e.Encoder.EncoderVersion}
	emb, err := e.Embed(ctx, in)
	if err != nil {
		return domain.SearchResult{Status: domain.LookupUnavailable}, Receipt{}, err
	}
	return e.public(ctx, domain.SearchQuery{Scope: domain.Playbooks, Text: text, Vector: emb.Vector, EncoderVersion: e.Encoder.EncoderVersion, Limit: 3})
}
func (e *Engine) Sources(r domain.SearchResult) []Playbook {
	out := []Playbook{}
	for _, h := range r.Hits {
		for _, p := range e.Corpus {
			if p.ID == h.SourceID && p.Revision == h.SourceVersion {
				out = append(out, p)
			}
		}
	}
	return out
}

// Revalidate resolves only the reviewed concrete public index. A cached hit is
// not permission to display a deleted, withdrawn or changed source. Callers run
// this bounded dependency work before taking household/payment decision locks.
func (e *Engine) Revalidate(ctx context.Context, r domain.SearchResult) ([]Playbook, error) {
	if r.Validate(domain.SearchQuery{Scope: domain.Playbooks, EncoderVersion: e.Encoder.EncoderVersion, Limit: 20}) != nil || r.Status != domain.LookupOK || r.IndexVersion != IndexVersion || r.MappingVersion != "playbooks-v1" || r.FusionVersion != FusionVersion {
		return nil, errors.New("invalid public references")
	}
	if err := e.verifyIndex(ctx); err != nil {
		return nil, err
	}
	return e.validateDocuments(ctx, r.Hits)
}

func (e *Engine) validateDocuments(ctx context.Context, hits []domain.SearchHit) ([]Playbook, error) {
	out := e.Sources(domain.SearchResult{Hits: hits})
	if len(out) != len(hits) {
		return nil, errors.New("unapproved public references")
	}
	if len(hits) == 0 {
		return out, nil
	}
	ids := make([]string, len(hits))
	for n, h := range hits {
		ids[n] = h.SourceID
	}
	token := ""
	if e.Config.ElasticKey != "" {
		token = "ApiKey " + e.Config.ElasticKey
	}
	var response struct {
		Docs []struct {
			Index  string          `json:"_index"`
			ID     string          `json:"_id"`
			Found  bool            `json:"found"`
			Source json.RawMessage `json:"_source"`
		} `json:"docs"`
	}
	if err := e.request(ctx, "POST", e.Config.ElasticURL+"/"+IndexVersion+"/_mget?_source_excludes=embedding", token, map[string]any{"ids": ids}, &response); err != nil {
		return nil, err
	}
	if len(response.Docs) != len(out) {
		return nil, errors.New("incomplete public hydration")
	}
	for n, doc := range response.Docs {
		var source Playbook
		var space struct {
			Encoder string `json:"encoder_version"`
		}
		if !doc.Found || doc.Index != IndexVersion || doc.ID != ids[n] || json.Unmarshal(doc.Source, &source) != nil || json.Unmarshal(doc.Source, &space) != nil || space.Encoder != e.Encoder.EncoderVersion || !source.Active || source.Language != "en" || !bytes.Equal(canonicalJSON(source), canonicalJSON(out[n])) {
			return nil, errors.New("public source withdrawn or changed")
		}
	}
	return out, nil
}
func (e *Engine) Ingest(ctx context.Context) error {
	token := ""
	if e.Config.ElasticKey != "" {
		token = "ApiKey " + e.Config.ElasticKey
	}
	// Versioned index is created once. A mapping conflict is an error, never an
	// overwrite or broad deletion. Re-ingestion replaces approved IDs only.
	raw, err := os.ReadFile("search/mappings/playbooks-v1.json")
	if err != nil {
		return err
	}
	var mapping map[string]any
	if json.Unmarshal(raw, &mapping) != nil {
		return errors.New("invalid mapping")
	}
	mapping["mappings"].(map[string]any)["_meta"] = map[string]any{"mapping_version": "playbooks-v1", "encoder_version": e.Encoder.EncoderVersion, "corpus_hash": e.CorpusHash}
	var existing map[string]any
	if err = e.request(ctx, "GET", e.Config.ElasticURL+"/"+IndexVersion, token, nil, &existing); err != nil {
		if status, ok := err.(dependencyStatus); !ok || status != 404 {
			return err
		}
		if err = e.request(ctx, "PUT", e.Config.ElasticURL+"/"+IndexVersion, token, mapping, nil); err != nil {
			return err
		}
	} else {
		// Existing versioned index must have the exact approved schema/space.
		var indexMap map[string]any
		if err = e.request(ctx, "GET", e.Config.ElasticURL+"/"+IndexVersion+"/_mapping", token, nil, &indexMap); err != nil {
			return err
		}
		expected := mapping["mappings"]
		actual, ok := indexMap[IndexVersion].(map[string]any)
		if !ok || !mappingContains(actual["mappings"], expected) {
			return errors.New("existing index mapping mismatch; use new reviewed index version")
		}
	}
	for _, p := range e.Corpus {
		emb, err := e.Embed(ctx, domain.EmbedInput{Text: p.Title + " " + p.Summary + " " + strings.Join(p.Phrases, " "), EncoderVersion: e.Encoder.EncoderVersion, InputDigest: domain.Hash([]byte(p.Title + " " + p.Summary + " " + strings.Join(p.Phrases, " ")))})
		if err != nil {
			return err
		}
		var doc map[string]any
		raw, _ := json.Marshal(p)
		_ = json.Unmarshal(raw, &doc)
		doc["embedding"] = emb.Vector
		doc["encoder_version"] = e.Encoder.EncoderVersion
		if err = e.request(ctx, "PUT", e.Config.ElasticURL+"/"+IndexVersion+"/_doc/"+p.ID+"?refresh=wait_for", token, doc, nil); err != nil {
			return err
		}
	}
	// A failed first ingest must remain unavailable, including when an alias was
	// pre-created by an administrator. Retry fills the same approved IDs; only a
	// complete, exact corpus may publish readiness and switch the alias.
	hits := make([]domain.SearchHit, len(e.Corpus))
	for n, p := range e.Corpus {
		hits[n] = domain.SearchHit{SourceID: p.ID, SourceVersion: p.Revision}
	}
	if _, err = e.validateDocuments(ctx, hits); err != nil {
		return err
	}
	if err = e.verifyCount(ctx); err != nil {
		return err
	}
	meta := mapping["mappings"].(map[string]any)["_meta"].(map[string]any)
	meta["ingest_complete"] = "true"
	if err = e.request(ctx, "PUT", e.Config.ElasticURL+"/"+IndexVersion+"/_mapping", token, map[string]any{"_meta": meta}, nil); err != nil {
		return err
	}
	// An alias switch is explicit offline setup and affects only the public alias.
	actions := map[string]any{"actions": []any{map[string]any{"remove": map[string]any{"index": "pause-playbooks-*", "alias": e.Config.PlaybookAlias, "must_exist": false}}, map[string]any{"add": map[string]any{"index": IndexVersion, "alias": e.Config.PlaybookAlias}}}}
	return e.request(ctx, "POST", e.Config.ElasticURL+"/_aliases", token, actions, nil)
}
func canonicalJSON(v any) []byte { raw, _ := json.Marshal(v); return raw }
func mappingContains(actual, expected any) bool {
	if wanted, ok := expected.(map[string]any); ok {
		got, ok := actual.(map[string]any)
		if !ok {
			return false
		}
		for k, v := range wanted {
			if !mappingContains(got[k], v) {
				return false
			}
		}
		return true
	}
	return bytes.Equal(canonicalJSON(actual), canonicalJSON(expected))
}
func (e *Engine) verifyIndex(ctx context.Context) error {
	token := ""
	if e.Config.ElasticKey != "" {
		token = "ApiKey " + e.Config.ElasticKey
	}
	var mapping map[string]struct {
		Mappings struct {
			Meta       map[string]string          `json:"_meta"`
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"mappings"`
	}
	if err := e.request(ctx, "GET", e.Config.ElasticURL+"/"+e.Config.PlaybookAlias+"/_mapping", token, nil, &mapping); err != nil {
		return err
	}
	if len(mapping) != 1 {
		return errors.New("ambiguous public alias")
	}
	entry, ok := mapping[IndexVersion]
	if !ok || entry.Mappings.Meta["mapping_version"] != "playbooks-v1" || entry.Mappings.Meta["encoder_version"] != e.Encoder.EncoderVersion || entry.Mappings.Meta["corpus_hash"] != e.CorpusHash || entry.Mappings.Meta["ingest_complete"] != "true" {
		return errors.New("public mapping/encoder/corpus mismatch")
	}
	var vector struct {
		Dims       int    `json:"dims"`
		Similarity string `json:"similarity"`
	}
	if json.Unmarshal(entry.Mappings.Properties["embedding"], &vector) != nil || vector.Dims != e.Encoder.Dimension || vector.Similarity != "cosine" {
		return errors.New("public vector space mismatch")
	}
	return e.verifyCount(ctx)
}

func (e *Engine) verifyCount(ctx context.Context) error {
	token := ""
	if e.Config.ElasticKey != "" {
		token = "ApiKey " + e.Config.ElasticKey
	}
	var result struct {
		Count  int `json:"count"`
		Shards struct {
			Failed int `json:"failed"`
		} `json:"_shards"`
	}
	if err := e.request(ctx, "GET", e.Config.ElasticURL+"/"+IndexVersion+"/_count", token, nil, &result); err != nil {
		return err
	}
	if result.Shards.Failed != 0 || result.Count != len(e.Corpus) {
		return errors.New("public corpus incomplete or unexpected documents")
	}
	return nil
}
