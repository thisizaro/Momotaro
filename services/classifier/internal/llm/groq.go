package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// DefaultGroqBaseURL is Groq's OpenAI-compatible surface. Overridable via
// GROQ_BASE_URL, which is what lets every test point at an httptest.Server.
const DefaultGroqBaseURL = "https://api.groq.com/openai/v1"

// DefaultGroqModel is the primary. gpt-oss-20b is one of only three models on
// Groq supporting strict structured output (with gpt-oss-120b and Qwen 3.8
// 27B); everything else, including llama-3.1-8b-instant, is best effort. That
// narrowness is the reason this model was chosen and not a larger one, and it
// is why changing this value is a decision rather than a tweak
// (docs/DECISIONS.md 2026-08-28).
const DefaultGroqModel = "openai/gpt-oss-20b"

// groqReasoningEffort is deliberately "low".
//
// GPT-OSS are reasoning models: at high effort, gpt-oss-20b measures roughly
// 3.05s time-to-first-token on Groq, which on its own exceeds both LLM_TIMEOUT
// and PRD.md section 10's 3s p95 target for the LLM path, before any network
// time. A seven-way classification over two fields does not need deliberation,
// so low is both the fast answer and the right one.
const groqReasoningEffort = "low"

type groqVendor struct {
	cfg Config
}

func (g *groqVendor) name() string { return GroqName }

type groqRequest struct {
	Model           string        `json:"model"`
	ReasoningEffort string        `json:"reasoning_effort,omitempty"`
	Temperature     float64       `json:"temperature"`
	Messages        []groqMessage `json:"messages"`
	ResponseFormat  groqRespFmt   `json:"response_format"`
}

type groqMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type groqRespFmt struct {
	Type       string         `json:"type"`
	JSONSchema groqJSONSchema `json:"json_schema"`
}

type groqJSONSchema struct {
	Name   string         `json:"name"`
	Strict bool           `json:"strict"`
	Schema map[string]any `json:"schema"`
}

func (g *groqVendor) request(ctx context.Context, p prompt) (*http.Request, error) {
	payload := groqRequest{
		Model:           g.cfg.Model,
		ReasoningEffort: groqReasoningEffort,
		// Deterministic as the API allows. Two identical records should
		// classify identically: SPEC.md section 7 requires it of the rules
		// engine and Phase 2's re-run safety test rests on it.
		Temperature: 0,
		Messages: []groqMessage{
			{Role: "system", Content: p.system},
			{Role: "user", Content: p.user},
		},
		ResponseFormat: groqRespFmt{
			Type: "json_schema",
			JSONSchema: groqJSONSchema{
				Name: "payment_failure_classification",
				// Constrained decoding: the model is restricted at the token
				// level and cannot emit an out-of-vocabulary value. This is
				// the strongest of the two gates; validate.go is still the
				// one this repo owns.
				Strict: true,
				Schema: outputSchema(dialectStrictJSONSchema),
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	url := strings.TrimSuffix(g.cfg.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.cfg.APIKey)
	return req, nil
}

// groqNudgeRequest is deliberately its own type rather than groqRequest with
// ResponseFormat left unset: groqRespFmt is a plain struct field, and Go's
// `omitempty` does not omit a struct value regardless of its contents, so
// reusing groqRequest would still serialize an empty (and invalid)
// "response_format" object. A dedicated type has no such field to omit.
type groqNudgeRequest struct {
	Model       string        `json:"model"`
	Temperature float64       `json:"temperature"`
	Messages    []groqMessage `json:"messages"`
}

// nudgeRequest is the ComposeNudge equivalent of request: same prompt pair,
// same endpoint and auth, but with no JSON-schema constraint, since a
// nudge's answer is prose, not a structured record. Temperature stays 0 for
// the same reproducibility reason Classify uses it.
func (g *groqVendor) nudgeRequest(ctx context.Context, p prompt) (*http.Request, error) {
	payload := groqNudgeRequest{
		Model:       g.cfg.Model,
		Temperature: 0,
		Messages: []groqMessage{
			{Role: "system", Content: p.system},
			{Role: "user", Content: p.user},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	url := strings.TrimSuffix(g.cfg.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.cfg.APIKey)
	return req, nil
}

type groqResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

func (g *groqVendor) answer(body []byte) (string, error) {
	var r groqResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return "", fmt.Errorf("decode response envelope: %w", err)
	}
	if len(r.Choices) == 0 {
		return "", fmt.Errorf("response contained no choices")
	}
	// A truncated generation is not a partial answer, it is broken JSON that
	// happens to start plausibly. Name it here rather than letting parseAnswer
	// report a confusing syntax error.
	if fr := r.Choices[0].FinishReason; fr != "" && fr != "stop" {
		return "", fmt.Errorf("generation did not complete: finish_reason=%q", fr)
	}
	content := r.Choices[0].Message.Content
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("response contained an empty message")
	}
	return content, nil
}
