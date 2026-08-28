package llm

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/thisizaro/Momotaro/internal/platform/logger"
	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
)

// Provider names, as they appear in LLM_PROVIDER_CHAIN and in the hop trail.
// Neither may contain ':' or ',': provider.NewChain rejects those, because
// Unit E encodes hops into one delimited column.
const (
	GroqName   = "groq"
	GeminiName = "gemini"
)

// vendor is the only part of a model call that differs between providers: how
// to build the HTTP request, and where the answer sits in the response. The
// prompt, the schema, the parsing and the sanitising are shared, which is why
// adding a third vendor is a file rather than a package.
type vendor interface {
	name() string
	request(ctx context.Context, p prompt) (*http.Request, error)
	// answer extracts the model's raw JSON answer from a successful response.
	answer(body []byte) (string, error)
}

// Config is one rung's settings. BaseURL exists so every test can point at an
// httptest.Server and exercise the real HTTP client, real JSON and real
// context cancellation with no key and no network. A rung that hardcoded its
// endpoint could only be tested by mocking the thing under test.
type Config struct {
	APIKey  string
	BaseURL string
	Model   string
}

// Provider is one model rung of the classifier's chain. It satisfies
// provider.Provider without importing that package: the interface is
// structural (Name, Classify), so the two packages stay independent and
// neither has to know the other's construction order.
type Provider struct {
	v      vendor
	client *client
	log    *slog.Logger
}

// NewGroq builds the primary rung: openai/gpt-oss-20b under strict structured
// output, which is token-level constrained decoding rather than a request that
// the model please comply (docs/DECISIONS.md 2026-08-28).
func NewGroq(cfg Config, log *slog.Logger) (*Provider, error) {
	if err := validateConfig(GroqName, cfg); err != nil {
		return nil, err
	}
	return &Provider{v: &groqVendor{cfg: cfg}, client: newClient(), log: log}, nil
}

// NewGemini builds the failover rung, on Gemini's native generateContent.
// Deliberately not its OpenAI-compatibility endpoint: that exists, but its
// tool calling does not follow the OpenAI schema, and structured output is the
// one capability a fallback rung must not get wrong.
func NewGemini(cfg Config, log *slog.Logger) (*Provider, error) {
	if err := validateConfig(GeminiName, cfg); err != nil {
		return nil, err
	}
	return &Provider{v: &geminiVendor{cfg: cfg}, client: newClient(), log: log}, nil
}

// validateConfig fails at construction, which means at startup, so a rung
// named in LLM_PROVIDER_CHAIN without a key stops the pod rather than failing
// every classification at request time (docs/ENGINEERING.md section 5).
func validateConfig(name string, cfg Config) error {
	if cfg.APIKey == "" {
		return fmt.Errorf("%s: API key is required when %q is in LLM_PROVIDER_CHAIN", name, name)
	}
	if cfg.BaseURL == "" {
		return fmt.Errorf("%s: base URL is required", name)
	}
	if cfg.Model == "" {
		return fmt.Errorf("%s: model is required", name)
	}
	return nil
}

// Name implements provider.Provider.
func (p *Provider) Name() string { return p.v.name() }

// Classify asks the model, and refuses to return anything it cannot verify.
//
// Every error path here becomes a hop in the chain and the next rung runs
// (provider/chain.go), ending at the rules engine, which cannot fail. That is
// the whole reason this rung is allowed to be unreliable.
func (p *Provider) Classify(ctx context.Context, req *classifierv1.ClassifyRequest) (*classifierv1.ClassifyResponse, error) {
	httpReq, err := p.v.request(ctx, buildPrompt(req))
	if err != nil {
		return nil, fmt.Errorf("%s: build request: %w", p.v.name(), err)
	}

	body, err := p.client.do(p.v.name(), httpReq)
	if err != nil {
		return nil, err
	}

	raw, err := p.v.answer(body)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p.v.name(), err)
	}

	resp, err := parseAnswer(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p.v.name(), err)
	}

	logger.ForRecord(p.log, req.GetRecord().GetId(), req.GetRecord().GetBatchId()).Debug("model classified record",
		logger.KeyProvider, p.v.name(),
		logger.KeyBucket, resp.GetBucket().String(),
		logger.KeyAction, resp.GetRecommendedAction().String(),
		"confidence", resp.GetConfidence(),
	)
	// Source and Hops are set by the chain, which is the only thing that knows
	// what was tried before this rung answered (SPEC.md section 4.6).
	return resp, nil
}
