package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// DefaultGeminiBaseURL is the native generateContent surface, NOT the
// OpenAI-compatibility endpoint at /v1beta/openai.
//
// That endpoint exists and would have let this rung share Groq's client
// exactly, which is precisely why the choice is worth writing down: its tool
// calling does not follow the OpenAI schema, and structured output is the one
// capability a fallback rung must not get wrong. A rung that silently degrades
// to freeform prose when the primary is down is worse than no rung at all,
// because it fails at the moment it is needed (docs/DECISIONS.md 2026-08-28).
const DefaultGeminiBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// DefaultGeminiModel is the failover. Flash-tier: this is a small structured
// classification, and the free tier's request-per-day cap is the binding
// constraint, not model capability.
const DefaultGeminiModel = "gemini-2.5-flash"

type geminiVendor struct {
	cfg Config
}

func (g *geminiVendor) name() string { return GeminiName }

type geminiRequest struct {
	Contents          []geminiContent `json:"contents"`
	SystemInstruction *geminiContent  `json:"systemInstruction,omitempty"`
	GenerationConfig  geminiGenConfig `json:"generationConfig"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiGenConfig struct {
	Temperature      float64        `json:"temperature"`
	ResponseMIMEType string         `json:"responseMimeType"`
	ResponseSchema   map[string]any `json:"responseSchema"`
}

func (g *geminiVendor) request(ctx context.Context, p prompt) (*http.Request, error) {
	payload := geminiRequest{
		Contents: []geminiContent{{
			Role:  "user",
			Parts: []geminiPart{{Text: p.user}},
		}},
		SystemInstruction: &geminiContent{
			Parts: []geminiPart{{Text: p.system}},
		},
		GenerationConfig: geminiGenConfig{
			Temperature:      0,
			ResponseMIMEType: "application/json",
			// dialectGemini, not the strict one: Gemini supports only a subset
			// of JSON Schema and rejects additionalProperties, which Groq's
			// strict mode requires. One schema for both would fail on one.
			ResponseSchema: outputSchema(dialectGemini),
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// The key goes in a query parameter, which is Gemini's documented scheme.
	// url.Values escapes it, so a key containing a reserved character cannot
	// silently produce a malformed URL.
	endpoint := fmt.Sprintf("%s/models/%s:generateContent?%s",
		strings.TrimSuffix(g.cfg.BaseURL, "/"),
		url.PathEscape(g.cfg.Model),
		url.Values{"key": {g.cfg.APIKey}}.Encode(),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

// geminiNudgeRequest is its own type rather than geminiRequest with
// GenerationConfig's schema fields left unset, for the same reason
// groqNudgeRequest exists: a non-pointer struct field is never omitted by
// `omitempty` regardless of its contents, so reusing geminiRequest would
// still ask for an (invalid, empty-schema) structured JSON response.
type geminiNudgeRequest struct {
	Contents          []geminiContent      `json:"contents"`
	SystemInstruction *geminiContent       `json:"systemInstruction,omitempty"`
	GenerationConfig  geminiNudgeGenConfig `json:"generationConfig"`
}

type geminiNudgeGenConfig struct {
	Temperature float64 `json:"temperature"`
}

// nudgeRequest is the ComposeNudge equivalent of request: same prompt pair,
// same endpoint, but with no response schema, since a nudge's answer is
// prose, not a structured record.
func (g *geminiVendor) nudgeRequest(ctx context.Context, p prompt) (*http.Request, error) {
	payload := geminiNudgeRequest{
		Contents: []geminiContent{{
			Role:  "user",
			Parts: []geminiPart{{Text: p.user}},
		}},
		SystemInstruction: &geminiContent{
			Parts: []geminiPart{{Text: p.system}},
		},
		GenerationConfig: geminiNudgeGenConfig{Temperature: 0},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/models/%s:generateContent?%s",
		strings.TrimSuffix(g.cfg.BaseURL, "/"),
		url.PathEscape(g.cfg.Model),
		url.Values{"key": {g.cfg.APIKey}}.Encode(),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []geminiPart `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback,omitempty"`
}

func (g *geminiVendor) answer(body []byte) (string, error) {
	var r geminiResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return "", fmt.Errorf("decode response envelope: %w", err)
	}
	// A safety block returns HTTP 200 with no candidates, so it has to be
	// detected here rather than by the status check. Worth its own message:
	// "the safety filter refused" and "the model returned nothing" look
	// identical downstream but mean very different things, and a payment
	// failure code tripping a safety filter is something an operator should
	// see rather than have folded into a generic error.
	if r.PromptFeedback != nil && r.PromptFeedback.BlockReason != "" {
		return "", fmt.Errorf("prompt blocked by safety filter: %s", r.PromptFeedback.BlockReason)
	}
	if len(r.Candidates) == 0 {
		return "", fmt.Errorf("response contained no candidates")
	}
	c := r.Candidates[0]
	if c.FinishReason != "" && c.FinishReason != "STOP" {
		return "", fmt.Errorf("generation did not complete: finishReason=%q", c.FinishReason)
	}
	var sb strings.Builder
	for _, part := range c.Content.Parts {
		sb.WriteString(part.Text)
	}
	if strings.TrimSpace(sb.String()) == "" {
		return "", fmt.Errorf("response contained no text")
	}
	return sb.String(), nil
}
