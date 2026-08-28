package provider

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/logger"
	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	"github.com/thisizaro/Momotaro/services/classifier/internal/llm"
)

// Phase 3 Unit C: PLAN.md's "simulate timeout/error per provider, confirm
// the chain falls through correctly and every hop tried is recorded", one
// test per failure mode, each asserting the exact hop result string rather
// than merely that a fallback occurred.
//
// These import services/classifier/internal/llm for its two exported error
// types (RateLimitedError, StatusError) rather than hand-rolling local
// equivalents. No HTTP happens here (SPEC.md section 7: use fake rungs, do
// not call anything real) -- the point is that the *value* fed into
// hopResultForError is the same type the real llm.Provider actually
// produces, not a stand-in that could quietly drift from it.

// assertHop is the one assertion every test below makes: hop i names the
// right provider and carries the right result string.
func assertHop(t *testing.T, resp *classifierv1.ClassifyResponse, i int, wantProvider, wantResult string) {
	t.Helper()
	hops := resp.GetHops()
	if i >= len(hops) {
		t.Fatalf("hops = %+v, want at least %d entries", hops, i+1)
	}
	if hops[i].GetProvider() != wantProvider || hops[i].GetResult() != wantResult {
		t.Errorf("hops[%d] = %s/%s, want %s/%s", i, hops[i].GetProvider(), hops[i].GetResult(), wantProvider, wantResult)
	}
}

func twoRungChain(t *testing.T, failing *fakeRung) (*Chain, *fakeRung) {
	t.Helper()
	rules := &fakeRung{name: RulesName, resp: validResponse()}
	c, err := NewChain([]string{failing.name, RulesName}, map[string]Provider{failing.name: failing, RulesName: rules}, testConfig(), logger.Discard())
	if err != nil {
		t.Fatalf("NewChain: %v", err)
	}
	return c, rules
}

func TestFallbackOnTimeout(t *testing.T) {
	failing := &fakeRung{name: llm.GroqName, err: context.DeadlineExceeded}
	c, rules := twoRungChain(t, failing)

	resp, err := c.Classify(context.Background(), &classifierv1.ClassifyRequest{})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	assertHop(t, resp, 0, llm.GroqName, HopTimeout)
	if rules.calls != 1 {
		t.Error("fallback rung was not tried after a timeout")
	}
}

func TestFallbackOnTransportError(t *testing.T) {
	// What client.do actually wraps a dial failure as (client.go): the raw
	// net error, not a typed error of its own. hopResultForError's default
	// branch is what has to catch this.
	failing := &fakeRung{name: llm.GroqName, err: fmt.Errorf("%s request: dial tcp: connection refused", llm.GroqName)}
	c, rules := twoRungChain(t, failing)

	resp, err := c.Classify(context.Background(), &classifierv1.ClassifyRequest{})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	assertHop(t, resp, 0, llm.GroqName, HopError)
	if rules.calls != 1 {
		t.Error("fallback rung was not tried after a transport error")
	}
}

func TestFallbackOnHTTP5xx(t *testing.T) {
	failing := &fakeRung{name: llm.GroqName, err: &llm.StatusError{Provider: llm.GroqName, Code: 503, Body: "service unavailable"}}
	c, rules := twoRungChain(t, failing)

	resp, err := c.Classify(context.Background(), &classifierv1.ClassifyRequest{})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	// A 5xx is not a rate limit: it must land on HopError, not HopRateLimited,
	// or an outage would be misdiagnosed as self-inflicted throttling.
	assertHop(t, resp, 0, llm.GroqName, HopError)
	if rules.calls != 1 {
		t.Error("fallback rung was not tried after an HTTP 5xx")
	}
}

func TestFallbackOnHTTP429(t *testing.T) {
	failing := &fakeRung{name: llm.GroqName, err: &llm.RateLimitedError{Provider: llm.GroqName, RetryAfter: 30 * time.Second}}
	c, rules := twoRungChain(t, failing)

	resp, err := c.Classify(context.Background(), &classifierv1.ClassifyRequest{})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	// Distinct from HopError on purpose (docs/DECISIONS.md 2026-08-28): "we
	// were throttled" and "the provider is broken" call for different
	// operator responses.
	assertHop(t, resp, 0, llm.GroqName, HopRateLimited)
	if rules.calls != 1 {
		t.Error("fallback rung was not tried after an HTTP 429")
	}
}

func TestFallbackOnInvalidJSON(t *testing.T) {
	// What answer()/parseAnswer actually produce for a truncated or malformed
	// body: a plain wrapped error, never a typed one, because there is
	// nothing structured to type -- the JSON just didn't parse.
	failing := &fakeRung{name: llm.GroqName, err: fmt.Errorf("%s: decode response envelope: invalid character 'x' looking for beginning of value", llm.GroqName)}
	c, rules := twoRungChain(t, failing)

	resp, err := c.Classify(context.Background(), &classifierv1.ClassifyRequest{})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	assertHop(t, resp, 0, llm.GroqName, HopError)
	if rules.calls != 1 {
		t.Error("fallback rung was not tried after invalid JSON")
	}
}

func TestFallbackOnOutOfVocabularyEnum(t *testing.T) {
	// Valid JSON, but a bucket outside RootCauseBucket: validate() catches
	// this and it becomes a rung *failure*, not an answer (SPEC.md section
	// 4.7), specifically HopSchemaInvalid rather than HopError -- the model
	// answered, it just answered something this service will not trust.
	bad := invalidResponses()["bucket outside enum"]
	failing := &fakeRung{name: llm.GroqName, resp: bad}
	c, rules := twoRungChain(t, failing)

	resp, err := c.Classify(context.Background(), &classifierv1.ClassifyRequest{})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	assertHop(t, resp, 0, llm.GroqName, HopSchemaInvalid)
	if rules.calls != 1 {
		t.Error("fallback rung was not tried after an out-of-vocabulary enum")
	}
}

// TestFallbackFromGroqToGeminiPreservesOrderAndSource is the two-LLM-rung
// case: the first fails, the second answers. Hops must appear in attempt
// order and Source must reflect that a model answered, not the fallback
// rules engine three rungs might never reach.
func TestFallbackFromGroqToGeminiPreservesOrderAndSource(t *testing.T) {
	groq := &fakeRung{name: llm.GroqName, err: context.DeadlineExceeded}
	gemini := &fakeRung{name: llm.GeminiName, resp: validResponse()}
	rules := &fakeRung{name: RulesName, resp: validResponse()}
	c, err := NewChain([]string{llm.GroqName, llm.GeminiName, RulesName},
		map[string]Provider{llm.GroqName: groq, llm.GeminiName: gemini, RulesName: rules},
		testConfig(), logger.Discard())
	if err != nil {
		t.Fatalf("NewChain: %v", err)
	}

	resp, err := c.Classify(context.Background(), &classifierv1.ClassifyRequest{})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if len(resp.GetHops()) != 2 {
		t.Fatalf("hops = %+v, want exactly two: groq tried and failed, gemini answered", resp.GetHops())
	}
	assertHop(t, resp, 0, llm.GroqName, HopTimeout)
	assertHop(t, resp, 1, llm.GeminiName, HopOK)
	if resp.GetSource() != commonv1.Source_SOURCE_LLM {
		t.Errorf("Source = %v, want SOURCE_LLM: a model answered, even though it was not the first one tried", resp.GetSource())
	}
	if rules.calls != 0 {
		t.Error("rules rung was invoked even though gemini already answered")
	}
}

// errCircuitOpenLike proves hopResultForError distinguishes an open circuit
// from a plain error: it is wrapped, exactly as breaker.go actually returns
// it (fmt.Errorf("...: %w", ErrCircuitOpen)), so errors.Is finds it the same
// way the real breaker's caller would.
func TestFallbackOnOpenCircuit(t *testing.T) {
	failing := &fakeRung{name: llm.GroqName, err: fmt.Errorf("%s: %w", llm.GroqName, ErrCircuitOpen)}
	c, rules := twoRungChain(t, failing)

	resp, err := c.Classify(context.Background(), &classifierv1.ClassifyRequest{})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	assertHop(t, resp, 0, llm.GroqName, HopCircuitOpen)
	if rules.calls != 1 {
		t.Error("fallback rung was not tried after an open circuit")
	}
	if !errors.Is(failing.err, ErrCircuitOpen) {
		t.Fatal("test setup broken: failing.err does not wrap ErrCircuitOpen")
	}
}
