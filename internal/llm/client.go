// Package llm owns the bounded Gemini signal extractor. Its output is
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
	requestTimeout   = 10 * time.Second
	maxResponseBytes = 1 << 20
	maxOutputTokens  = 1024
	randomSeed       = 7
	interactionsPath = "/v1/interactions"
)

// Error carries only a bounded code. Error() never includes a response body,
// the API key or any URL, mirroring the PayPal client discipline.
type Error struct {
	Code      string
	HTTP      int
	Temporary bool
}

func (e *Error) Error() string { return e.Code }

// Client is a hardened Gemini Interactions client. No retries: a failed
// call is recorded as UNAVAILABLE and the pipeline continues without it.
type Client struct {
	base, key, model, thinkingLevel string
	http                            *http.Client
}

func New(apiKey, model, baseURL, thinkingLevel string) *Client {
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com"
	}
	if thinkingLevel == "" {
		thinkingLevel = "minimal"
	}
	return &Client{thinkingLevel: thinkingLevel, base: strings.TrimSuffix(baseURL, "/"), key: apiKey, model: model, http: &http.Client{Timeout: requestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

type interactionRequest struct {
	Model             string                  `json:"model"`
	SystemInstruction string                  `json:"system_instruction"`
	Input             string                  `json:"input"`
	ResponseFormat    interactionResponseSpec `json:"response_format"`
	GenerationConfig  generationConfig        `json:"generation_config"`
	Store             bool                    `json:"store"`
}
type interactionResponseSpec struct {
	Type     string         `json:"type"`
	MIMEType string         `json:"mime_type"`
	Schema   map[string]any `json:"schema"`
}
type generationConfig struct {
	Seed            int    `json:"seed"`
	MaxOutputTokens int    `json:"max_output_tokens"`
	ThinkingLevel   string `json:"thinking_level"`
}
type interactionResponse struct {
	Status string `json:"status"`
	Steps  []struct {
		Type    string          `json:"type"`
		Content json.RawMessage `json:"content"`
	} `json:"steps"`
}
type interactionContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
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
	answer, e := c.generate(ctx, prompt, messageBlock(sent), responseSchema())
	if e != nil {
		if failure, ok := e.(*Error); ok && failure.Code == "LLM_INVALID_OUTPUT" {
			signals.Status = StatusInvalidOutput
		}
		return signals, e
	}
	return Validate([]byte(answer), sent, signals)
}

// Model identifies the configured model for derived-display cache keys.
func (c *Client) Model() string { return c.model }

// generate shares the bounded Interactions transport without changing signal requests.
func (c *Client) generate(ctx context.Context, system, input string, schema map[string]any) (string, error) {
	req, e := http.NewRequestWithContext(ctx, "POST", c.base+interactionsPath, mustJSON(interactionRequest{
		Model: c.model, SystemInstruction: system, Input: input,
		ResponseFormat:   interactionResponseSpec{Type: "text", MIMEType: "application/json", Schema: schema},
		GenerationConfig: generationConfig{Seed: randomSeed, MaxOutputTokens: maxOutputTokens, ThinkingLevel: c.thinkingLevel},
		Store:            false,
	}))
	if e != nil {
		return "", &Error{Code: "LLM_TRANSPORT", Temporary: true}
	}
	req.Header.Set("x-goog-api-key", c.key)
	req.Header.Set("Content-Type", "application/json")
	resp, e := c.http.Do(req)
	if e != nil {
		return "", &Error{Code: "LLM_TRANSPORT", Temporary: true}
	}
	defer resp.Body.Close()
	body, e := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if e != nil || len(body) > maxResponseBytes {
		return "", &Error{Code: "LLM_UNREADABLE", HTTP: resp.StatusCode, Temporary: true}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &Error{Code: "LLM_HTTP_ERROR", HTTP: resp.StatusCode, Temporary: resp.StatusCode == 429 || resp.StatusCode >= 500}
	}
	var envelope interactionResponse
	if json.Unmarshal(body, &envelope) != nil {
		return "", &Error{Code: "LLM_INVALID_OUTPUT"}
	}
	if envelope.Status == "incomplete" {
		return "", &Error{Code: "LLM_INVALID_OUTPUT"}
	}
	if envelope.Status != "completed" {
		return "", &Error{Code: "LLM_UNAVAILABLE"}
	}
	var answer string
	textParts := 0
	for _, step := range envelope.Steps {
		if step.Type != "model_output" {
			continue
		}
		var content []interactionContent
		if json.Unmarshal(step.Content, &content) != nil {
			return "", &Error{Code: "LLM_INVALID_OUTPUT"}
		}
		for _, part := range content {
			if part.Type == "text" {
				textParts++
				answer = part.Text
			}
		}
	}
	if textParts != 1 {
		return "", &Error{Code: "LLM_INVALID_OUTPUT"}
	}
	return answer, nil
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
