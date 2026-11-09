// Package llm owns the bounded Mistral signal extractor. Its output is
// advisory only: callers may surface it or raise reviewer attention, but no
// release, eligibility or money path may consult it, and its unavailability
// must never block anything. Every model response is untrusted data that is
// re-validated in Go against an exact schema before it is recorded.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ramst/pause/internal/domain"
)

const (
	requestTimeout      = 10 * time.Second
	maxResponseBytes    = 1 << 20
	maxOutputTokens     = 1024
	randomSeed          = 7
	chatCompletionsPath = "/v1/chat/completions"
)

// Error carries only a bounded code. Error() never includes a response body,
// the API key or any URL, mirroring the PayPal client discipline.
type Error struct {
	Code      string
	HTTP      int
	Temporary bool
}

func (e *Error) Error() string { return e.Code }

// Client is a hardened Mistral chat-completions client. No retries: a failed
// call is recorded as UNAVAILABLE and the pipeline continues without it.
type Client struct {
	base, key, model string
	http             *http.Client
}

func New(apiKey, model, baseURL string) *Client {
	if baseURL == "" {
		baseURL = "https://api.mistral.ai"
	}
	return &Client{base: strings.TrimSuffix(baseURL, "/"), key: apiKey, model: model, http: &http.Client{Timeout: requestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

type chatRequest struct {
	Model          string        `json:"model"`
	Messages       []chatMessage `json:"messages"`
	Temperature    float64       `json:"temperature"`
	RandomSeed     int           `json:"random_seed"`
	MaxTokens      int           `json:"max_tokens"`
	ResponseFormat responseSpec  `json:"response_format"`
}
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type responseSpec struct {
	Type       string      `json:"type"`
	JSONSchema *schemaSpec `json:"json_schema,omitempty"`
}
type schemaSpec struct {
	Name   string         `json:"name"`
	Strict bool           `json:"strict"`
	Schema map[string]any `json:"schema"`
}
type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// structuredOutput reports whether the configured model family accepts
// response_format json_schema. Older open-weights names fall back to
// json_object. Go-side validation is the authority either way, so a stale
// list only loses schema enforcement, never correctness.
func structuredOutput(model string) bool {
	for _, legacy := range []string{"open-mistral-", "open-mixtral-", "mistral-7b", "mistral-tiny", "mixtral-"} {
		if strings.HasPrefix(model, legacy) {
			return false
		}
	}
	return true
}

func responseSchema() map[string]any {
	props := map[string]any{}
	for _, label := range domain.Labels {
		props[label] = map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"present": map[string]any{"type": "boolean"}, "excerpt": map[string]any{"type": "string"}}, "required": []string{"present", "excerpt"}}
	}
	return map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"labels": map[string]any{"type": "object", "additionalProperties": false, "properties": props, "required": domain.Labels}}, "required": []string{"labels"}}
}

// ExtractSignals redacts the text, asks the configured model to flag pressure
// labels, and validates the response in Go. The returned Signals always
// carries a terminal status matching the error; the error is a mirror whose
// Error() is only a code.
func (c *Client) ExtractSignals(ctx context.Context, text string) (signals Signals, err error) {
	started := time.Now()
	sent := domain.RedactPrivateAddresses(text)
	prompt := systemPrompt()
	signals = Signals{Status: StatusUnavailable, Model: c.model, PromptVersion: PromptVersion, PromptHash: domain.Hash([]byte(prompt)), Labels: []LabelFlag{}}
	defer func() { signals.DurationMS = time.Since(started).Milliseconds() }()
	spec := responseSpec{Type: "json_object"}
	if structuredOutput(c.model) {
		spec = responseSpec{Type: "json_schema", JSONSchema: &schemaSpec{Name: "pause_signals", Strict: true, Schema: responseSchema()}}
	}
	req, e := http.NewRequestWithContext(ctx, "POST", c.base+chatCompletionsPath, mustJSON(chatRequest{Model: c.model, Messages: []chatMessage{{Role: "system", Content: prompt}, {Role: "user", Content: messageBlock(sent)}}, Temperature: 0, RandomSeed: randomSeed, MaxTokens: maxOutputTokens, ResponseFormat: spec}))
	if e != nil {
		return signals, &Error{Code: "LLM_TRANSPORT", Temporary: true}
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")
	resp, e := c.http.Do(req)
	if e != nil {
		return signals, &Error{Code: "LLM_TRANSPORT", Temporary: true}
	}
	defer resp.Body.Close()
	body, e := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if e != nil || len(body) > maxResponseBytes {
		return signals, &Error{Code: "LLM_UNREADABLE", HTTP: resp.StatusCode, Temporary: true}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return signals, &Error{Code: "LLM_HTTP_ERROR", HTTP: resp.StatusCode, Temporary: resp.StatusCode == 429 || resp.StatusCode >= 500}
	}
	var envelope chatResponse
	if json.Unmarshal(body, &envelope) != nil || len(envelope.Choices) != 1 {
		return signals, &Error{Code: "LLM_INVALID_OUTPUT"}
	}
	return Validate([]byte(envelope.Choices[0].Message.Content), sent, signals)
}

func mustJSON(v any) io.Reader {
	b, err := json.Marshal(v)
	if err != nil {
		return strings.NewReader("{}")
	}
	return bytes.NewReader(b)
}

// messageBlock delimits the untrusted text. The system prompt instructs the
// model to treat everything between the markers as data; grounded excerpt
// validation bounds the damage of any boundary confusion.
func messageBlock(sent string) string {
	return fmt.Sprintf("%s\n%s\n%s", MessageStart, sent, MessageEnd)
}
