package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/logger"
	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

func testNudgeRequest() *classifierv1.ComposeNudgeRequest {
	return &classifierv1.ComposeNudgeRequest{
		Record: &commonv1.Record{
			Id:          "rec-1",
			BatchId:     "batch-1",
			AmountPaise: 499900,
			Currency:    "INR",
		},
		Bucket:        commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
		ActionType:    commonv1.ActionType_ACTION_TYPE_RETRY,
		Locale:        "en-IN-hinglish",
		ContactNumber: 1,
		MaxChars:      160,
	}
}

func TestProviderComposesAWellFormedNudge(t *testing.T) {
	bothVendors(t, envelope("Aapka "+amountPlaceholder+" ka payment fail ho gaya, hum dobara try kar rahe hain."), func(t *testing.T, p *Provider) {
		resp, err := p.ComposeNudge(context.Background(), testNudgeRequest())
		if err != nil {
			t.Fatalf("ComposeNudge: %v", err)
		}
		if resp.GetMessage() == "" {
			t.Error("Message is empty")
		}
		// Source and Hops belong to the chain, not the rung, same rule as
		// Classify (SPEC.md 4.6).
		if resp.GetSource() != commonv1.Source_SOURCE_UNSPECIFIED || len(resp.GetHops()) != 0 {
			t.Error("a rung must not set Source or Hops; the chain owns both")
		}
	})
}

func TestProviderRejectsEveryUntrustworthyNudgeResponse(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"body is not json": func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, "upstream proxy error, plain text")
		},
		"empty envelope": func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{}`)
		},
		"generation truncated": func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "chat/completions") {
				io.WriteString(w, `{"choices":[{"finish_reason":"length","message":{"content":"Aapka "}}]}`)
				return
			}
			io.WriteString(w, `{"candidates":[{"finishReason":"MAX_TOKENS","content":{"parts":[{"text":"Aapka "}]}}]}`)
		},
		"empty answer": envelope(""),
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			bothVendors(t, h, func(t *testing.T, p *Provider) {
				if _, err := p.ComposeNudge(context.Background(), testNudgeRequest()); err == nil {
					t.Errorf("%s: want error so the chain falls through, got nil", name)
				}
			})
		})
	}
}

func TestProviderSurfacesRateLimitingOnComposeNudge(t *testing.T) {
	h := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "42")
		w.WriteHeader(http.StatusTooManyRequests)
	}
	bothVendors(t, h, func(t *testing.T, p *Provider) {
		_, err := p.ComposeNudge(context.Background(), testNudgeRequest())
		var rl *RateLimitedError
		if !errors.As(err, &rl) {
			t.Fatalf("err = %v, want a *RateLimitedError", err)
		}
		if rl.RetryAfter != 42*time.Second {
			t.Errorf("RetryAfter = %s, want 42s from the header", rl.RetryAfter)
		}
	})
}

// TestNudgeRequestCarriesNoResponseSchema is the wire-shape regression this
// unit needs: unlike Classify, ComposeNudge must NOT constrain the model to
// JSON output, since the answer is prose, not a structured record. A vendor
// that copy-pasted its classify request builder would silently send the
// wrong content type and get JSON back instead of Hinglish text.
func TestNudgeRequestCarriesNoResponseSchema(t *testing.T) {
	var capturedBody []byte
	h := func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		envelope("Aapka "+amountPlaceholder+" ka payment fail ho gaya.")(w, r)
	}
	bothVendors(t, h, func(t *testing.T, p *Provider) {
		capturedBody = nil
		if _, err := p.ComposeNudge(context.Background(), testNudgeRequest()); err != nil {
			t.Fatalf("ComposeNudge: %v", err)
		}
		var asMap map[string]json.RawMessage
		if err := json.Unmarshal(capturedBody, &asMap); err != nil {
			t.Fatalf("request body is not JSON: %v", err)
		}
		for _, forbidden := range []string{"response_format", "responseSchema", "generationConfig"} {
			if raw, ok := asMap[forbidden]; ok && strings.Contains(string(raw), "schema") {
				t.Errorf("request body contains a JSON-schema constraint under %q: %s", forbidden, raw)
			}
		}
	})
}

// TestProviderComposeNudgeStripsSurroundingQuotesAndControlCharacters is the
// cleanup a model's raw text needs before it can be trusted: the system
// prompt says "no quotes around it", but models do not reliably obey that,
// and a literal newline in an SMS body is a rendering bug waiting to
// happen.
func TestProviderComposeNudgeStripsSurroundingQuotesAndControlCharacters(t *testing.T) {
	bothVendors(t, envelope("\"Aapka "+amountPlaceholder+" ka payment\tfail\nho gaya.\""), func(t *testing.T, p *Provider) {
		resp, err := p.ComposeNudge(context.Background(), testNudgeRequest())
		if err != nil {
			t.Fatalf("ComposeNudge: %v", err)
		}
		msg := resp.GetMessage()
		if strings.HasPrefix(msg, `"`) || strings.HasSuffix(msg, `"`) {
			t.Errorf("Message = %q, want surrounding quotes stripped", msg)
		}
		if strings.ContainsAny(msg, "\t\n") {
			t.Errorf("Message = %q, want control characters cleaned", msg)
		}
	})
}

func TestNewGroqComposeNudgeAndClassifyShareOneProvider(t *testing.T) {
	// ComposeNudge and Classify are two methods on the same *Provider,
	// deliberately: ARCHITECTURE.md section 5b requires nudge composition to
	// reuse "every piece of LLM plumbing" the Classifier already owns, not a
	// second, independent client construction.
	p, err := NewGroq(Config{APIKey: "k", BaseURL: "http://example.invalid", Model: "m"}, logger.Discard())
	if err != nil {
		t.Fatalf("NewGroq: %v", err)
	}
	var _ interface {
		Classify(context.Context, *classifierv1.ClassifyRequest) (*classifierv1.ClassifyResponse, error)
		ComposeNudge(context.Context, *classifierv1.ComposeNudgeRequest) (*classifierv1.ComposeNudgeResponse, error)
	} = p
}
