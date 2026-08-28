package provider

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/logger"
	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// blockingRung hangs until its context is cancelled, which is what a provider
// that has stopped answering actually looks like from in here.
type blockingRung struct {
	name  string
	calls int
}

func (b *blockingRung) Name() string { return b.name }

func (b *blockingRung) Classify(ctx context.Context, _ *classifierv1.ClassifyRequest) (*classifierv1.ClassifyResponse, error) {
	b.calls++
	<-ctx.Done()
	return nil, ctx.Err()
}

func rulesRegistry(extra map[string]Provider) map[string]Provider {
	reg := map[string]Provider{RulesName: &fakeRung{name: RulesName, resp: validResponse()}}
	for k, v := range extra {
		reg[k] = v
	}
	return reg
}

// --- Construction invariants (docs/PHASE3_IMPLEMENTATION.md Flaw 2) ---------
//
// Every one of these must fail at NewChain. A chain that builds and then
// dead-letters records at request time is the failure mode this whole set
// exists to make impossible.

func TestNewChainRejectsChainsThatCannotTerminate(t *testing.T) {
	llm := &fakeRung{name: "llm", resp: validResponse()}
	reg := rulesRegistry(map[string]Provider{"llm": llm})

	cases := map[string][]string{
		"empty chain":        {},
		"unknown name":       {"nonexistent", RulesName},
		"rules absent":       {"llm"},
		"rules not last":     {RulesName, "llm"},
		"rules listed twice": {RulesName, "llm", RulesName},
	}
	for name, names := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewChain(names, reg, testConfig(), logger.Discard()); err == nil {
				t.Fatalf("NewChain(%v): want error at construction, got nil", names)
			}
		})
	}
}

func TestNewChainAcceptsAValidChain(t *testing.T) {
	llm := &fakeRung{name: "llm", resp: validResponse()}
	reg := rulesRegistry(map[string]Provider{"llm": llm})
	if _, err := NewChain([]string{"llm", RulesName}, reg, testConfig(), logger.Discard()); err != nil {
		t.Fatalf("NewChain on a valid chain: %v", err)
	}
	if _, err := NewChain([]string{RulesName}, reg, testConfig(), logger.Discard()); err != nil {
		t.Fatalf("NewChain on the rules-only default chain: %v", err)
	}
}

// Unit E encodes hops as "provider:result" pairs joined by ",". Provider names
// come from config, so the delimiters are rejected here rather than escaped at
// the persistence layer, where a bad name corrupts an audit row instead of
// failing a startup.
func TestNewChainRejectsProviderNamesContainingHopDelimiters(t *testing.T) {
	for _, bad := range []string{"groq:v2", "groq,gemini"} {
		t.Run(bad, func(t *testing.T) {
			reg := rulesRegistry(map[string]Provider{bad: &fakeRung{name: bad, resp: validResponse()}})
			_, err := NewChain([]string{bad, RulesName}, reg, testConfig(), logger.Discard())
			if err == nil {
				t.Fatalf("NewChain with provider name %q: want error, got nil", bad)
			}
			if !strings.Contains(err.Error(), "':' or ','") {
				t.Errorf("error = %q, want it to name the offending delimiters", err)
			}
		})
	}
}

func TestNewChainRejectsANonsensicalBudget(t *testing.T) {
	reg := rulesRegistry(nil)
	cases := map[string]Config{
		"zero rung timeout":     {RungTimeout: 0, Reserve: time.Millisecond},
		"negative rung timeout": {RungTimeout: -time.Second, Reserve: time.Millisecond},
		"negative reserve":      {RungTimeout: time.Second, Reserve: -time.Millisecond},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewChain([]string{RulesName}, reg, cfg, logger.Discard()); err == nil {
				t.Fatalf("NewChain with %s: want error, got nil", name)
			}
		})
	}
}

// --- Budget behaviour (docs/PHASE3_IMPLEMENTATION.md Flaw 3) ---------------

func TestChainCutsOffAHangingRungAndRecordsItAsATimeout(t *testing.T) {
	hanging := &blockingRung{name: "llm"}
	rules := &fakeRung{name: RulesName, resp: validResponse()}
	cfg := Config{RungTimeout: 80 * time.Millisecond, Reserve: 10 * time.Millisecond}
	c, err := NewChain([]string{"llm", RulesName},
		map[string]Provider{"llm": hanging, RulesName: rules}, cfg, logger.Discard())
	if err != nil {
		t.Fatalf("NewChain: %v", err)
	}

	resp, err := c.Classify(context.Background(), &classifierv1.ClassifyRequest{})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if hanging.calls != 1 {
		t.Errorf("hanging rung calls = %d, want 1", hanging.calls)
	}
	if rules.calls != 1 {
		t.Errorf("rules rung calls = %d, want 1: a hung rung must not stop the walk", rules.calls)
	}
	hops := resp.GetHops()
	if len(hops) != 2 || hops[0].GetResult() != HopTimeout || hops[1].GetResult() != HopOK {
		t.Errorf("hops = %+v, want [{llm timeout} {rules ok}]", hops)
	}
}

// The Flaw 3 regression test, and the reason Unit A exists.
//
// Before the budget, a chain whose per-rung timeouts summed to more than the
// caller's deadline produced a correct answer that arrived after the caller
// had given up. The Decision Engine reads that as a failed Classify, retries
// three times and dead-letters the record (engine.go, maxClassifyAttempts), so
// the fallback chain became the thing that lost the record it existed to save.
//
// The assertion is deliberately NOT a latency bound, which would be flaky
// (docs/PHASE3_IMPLEMENTATION.md Flaw 6). It is binary: when Classify
// returned, was the caller still listening?
func TestChainAnswersInsideTheCallersDeadlineEvenWhenEveryRungAboveRulesIsDead(t *testing.T) {
	slowA := &blockingRung{name: "a"}
	slowB := &blockingRung{name: "b"}
	rules := &fakeRung{name: RulesName, resp: validResponse()}

	// Two rungs each willing to burn 2s, against a 600ms deadline. Without a
	// reserve the first rung alone consumes the whole budget.
	cfg := Config{RungTimeout: 2 * time.Second, Reserve: 300 * time.Millisecond}
	c, err := NewChain([]string{"a", "b", RulesName},
		map[string]Provider{"a": slowA, "b": slowB, RulesName: rules}, cfg, logger.Discard())
	if err != nil {
		t.Fatalf("NewChain: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()

	resp, err := c.Classify(ctx, &classifierv1.ClassifyRequest{})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if ctx.Err() != nil {
		t.Fatalf("chain answered after the caller's deadline expired (%v): "+
			"the Decision Engine would dead-letter this record", ctx.Err())
	}
	if resp.GetSource() != commonv1.Source_SOURCE_RULES_FALLBACK {
		t.Errorf("Source = %v, want SOURCE_RULES_FALLBACK", resp.GetSource())
	}

	// The second dead rung must be skipped rather than attempted: there was
	// not enough deadline left to afford it.
	if slowB.calls != 0 {
		t.Errorf("rung b calls = %d, want 0: it should have been skipped for want of budget", slowB.calls)
	}
	hops := resp.GetHops()
	if len(hops) != 3 {
		t.Fatalf("hops = %+v, want three: one per rung, attempted or deliberately skipped", hops)
	}
	if hops[0].GetResult() != HopTimeout {
		t.Errorf("hops[0] = %+v, want a timeout for the rung that was actually attempted", hops[0])
	}
	if hops[1].GetResult() != HopDeadlineExhausted {
		t.Errorf("hops[1] = %+v, want deadline_exhausted for the rung that was never called", hops[1])
	}
	if hops[2].GetResult() != HopOK {
		t.Errorf("hops[2] = %+v, want the rules rung to have answered", hops[2])
	}
}
