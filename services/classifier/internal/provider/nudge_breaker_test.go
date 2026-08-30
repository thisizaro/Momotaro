package provider

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/clock"
	"github.com/thisizaro/Momotaro/internal/platform/logger"
	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
)

// NudgeBreaker is a deliberate, self-contained mirror of Breaker
// (breaker.go), not a shared-state wrapper: sharing runtime circuit state
// between Classify and ComposeNudge calls to the same named provider was
// considered and set aside for this pass (docs/DECISIONS.md, Phase 5 Unit
// E) -- correct, but a materially riskier change to the already-tested
// Classify breaker for a benefit (one shared health signal instead of two
// independent, identically-behaved ones) that does not block this unit.
// Same duplication precedent this codebase already uses for
// correctActionFor across three files: cannot cleanly share, so mirror
// deliberately, document it, and test it exactly as rigorously as the
// original.
//
// countingNudgeRung mirrors breaker_test.go's countingRung.
type countingNudgeRung struct {
	name string

	mu    sync.Mutex
	calls int
	err   error
	resp  *classifierv1.ComposeNudgeResponse
	gate  chan struct{}
}

func (c *countingNudgeRung) Name() string { return c.name }

func (c *countingNudgeRung) ComposeNudge(context.Context, *classifierv1.ComposeNudgeRequest) (*classifierv1.ComposeNudgeResponse, error) {
	c.mu.Lock()
	c.calls++
	gate, resp, err := c.gate, c.resp, c.err
	c.mu.Unlock()

	if gate != nil {
		<-gate
	}
	return resp, err
}

func (c *countingNudgeRung) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *countingNudgeRung) setOutcome(resp *classifierv1.ComposeNudgeResponse, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resp, c.err = resp, err
}

func (c *countingNudgeRung) setGate(g chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gate = g
}

func testNudgeBreaker(t *testing.T, inner NudgeProvider, cfg BreakerConfig) (*NudgeBreaker, *clock.Fake) {
	t.Helper()
	fake := clock.NewFake(time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC))
	b, err := NewNudgeBreaker(inner, cfg, fake, logger.Discard())
	if err != nil {
		t.Fatalf("NewNudgeBreaker: %v", err)
	}
	return b, fake
}

func TestNudgeBreakerStopsCallingADeadProvider(t *testing.T) {
	down := &countingNudgeRung{name: "llm", err: errProviderDown}
	b, _ := testNudgeBreaker(t, down, BreakerConfig{Threshold: 2, Cooldown: 30 * time.Second})

	for i := 0; i < 2; i++ {
		if _, err := b.ComposeNudge(context.Background(), &classifierv1.ComposeNudgeRequest{MaxChars: 160}); err == nil {
			t.Fatalf("call %d: want error from the dead provider", i)
		}
	}
	before := down.callCount()
	if _, err := b.ComposeNudge(context.Background(), &classifierv1.ComposeNudgeRequest{MaxChars: 160}); !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("call after threshold: err = %v, want ErrCircuitOpen", err)
	}
	if down.callCount() != before {
		t.Error("the provider was called again after the breaker opened")
	}
}

func TestNudgeBreakerOpensOnASingle429WithoutWaitingForTheThreshold(t *testing.T) {
	down := &countingNudgeRung{name: "llm", err: throttled{after: 5 * time.Second}}
	b, _ := testNudgeBreaker(t, down, BreakerConfig{Threshold: 5, Cooldown: 30 * time.Second})

	b.ComposeNudge(context.Background(), &classifierv1.ComposeNudgeRequest{MaxChars: 160})
	if _, err := b.ComposeNudge(context.Background(), &classifierv1.ComposeNudgeRequest{MaxChars: 160}); !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("second call after a single 429: err = %v, want ErrCircuitOpen (threshold must not apply to rate limits)", err)
	}
}

func TestNudgeBreakerHalfOpenAdmitsExactlyOneTrialUnderConcurrency(t *testing.T) {
	const racers = 25

	down := &countingNudgeRung{name: "llm", err: errProviderDown}
	b, fake := testNudgeBreaker(t, down, BreakerConfig{Threshold: 1, Cooldown: 30 * time.Second})

	b.ComposeNudge(context.Background(), &classifierv1.ComposeNudgeRequest{MaxChars: 160}) // trip it
	callsWhenOpen := down.callCount()

	gate := make(chan struct{})
	down.setGate(gate)
	fake.Advance(31 * time.Second) // half-open

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			b.ComposeNudge(context.Background(), &classifierv1.ComposeNudgeRequest{MaxChars: 160})
		}()
	}
	close(start)
	time.Sleep(100 * time.Millisecond)
	close(gate)
	wg.Wait()

	if got := down.callCount() - callsWhenOpen; got != 1 {
		t.Errorf("half-open admitted %d requests, want exactly 1", got)
	}
}

func TestNudgeBreakerClosesOnASuccessfulTrialAndReopensOnAFailedOne(t *testing.T) {
	rung := &countingNudgeRung{name: "llm", err: errProviderDown}
	b, fake := testNudgeBreaker(t, rung, BreakerConfig{Threshold: 1, Cooldown: 30 * time.Second})

	b.ComposeNudge(context.Background(), &classifierv1.ComposeNudgeRequest{MaxChars: 160}) // open
	fake.Advance(31 * time.Second)

	if _, err := b.ComposeNudge(context.Background(), &classifierv1.ComposeNudgeRequest{MaxChars: 160}); errors.Is(err, ErrCircuitOpen) {
		t.Fatal("the trial request should have reached the provider")
	}
	if _, err := b.ComposeNudge(context.Background(), &classifierv1.ComposeNudgeRequest{MaxChars: 160}); !errors.Is(err, ErrCircuitOpen) {
		t.Error("a failed trial must reopen the circuit")
	}

	fake.Advance(31 * time.Second)
	rung.setOutcome(validNudgeResponse(), nil)
	if _, err := b.ComposeNudge(context.Background(), &classifierv1.ComposeNudgeRequest{MaxChars: 160}); err != nil {
		t.Fatalf("trial request: %v", err)
	}
	before := rung.callCount()
	for i := 0; i < 5; i++ {
		if _, err := b.ComposeNudge(context.Background(), &classifierv1.ComposeNudgeRequest{MaxChars: 160}); err != nil {
			t.Fatalf("request after the circuit closed: %v", err)
		}
	}
	if got := rung.callCount() - before; got != 5 {
		t.Errorf("provider calls after closing = %d, want 5", got)
	}
}

func TestNudgeBreakerCountsAnInvalidResponseAsAFailure(t *testing.T) {
	bad := &countingNudgeRung{name: "llm", resp: &classifierv1.ComposeNudgeResponse{Message: "Pay Rs 750 now"}}
	b, _ := testNudgeBreaker(t, bad, BreakerConfig{Threshold: 2, Cooldown: 30 * time.Second})

	for i := 0; i < 2; i++ {
		if _, err := b.ComposeNudge(context.Background(), &classifierv1.ComposeNudgeRequest{MaxChars: 160}); err != nil {
			t.Fatalf("call %d: the rung reported success, so the breaker should pass it through: %v", i, err)
		}
	}
	if _, err := b.ComposeNudge(context.Background(), &classifierv1.ComposeNudgeRequest{MaxChars: 160}); !errors.Is(err, ErrCircuitOpen) {
		t.Error("two invalid responses did not trip a threshold-2 breaker")
	}
}

func TestNewNudgeBreakerRefusesToWrapTheRungThatCannotFail(t *testing.T) {
	template := &fakeNudgeRung{name: RulesName, resp: validNudgeResponse()}
	if _, err := NewNudgeBreaker(template, BreakerConfig{Threshold: 1, Cooldown: time.Second}, clock.New(), logger.Discard()); err == nil {
		t.Fatal("NewNudgeBreaker wrapping the template rung: want error, got nil")
	}
}

func TestNewNudgeBreakerRejectsANonsensicalConfig(t *testing.T) {
	llm := &fakeNudgeRung{name: "llm"}
	cases := map[string]BreakerConfig{
		"zero threshold":    {Threshold: 0, Cooldown: time.Second},
		"negative cooldown": {Threshold: 1, Cooldown: -time.Second},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewNudgeBreaker(llm, cfg, clock.New(), logger.Discard()); err == nil {
				t.Fatalf("NewNudgeBreaker with %s: want error, got nil", name)
			}
		})
	}
}

func TestNudgeBreakerReportsTheWrappedRungsName(t *testing.T) {
	llm := &fakeNudgeRung{name: "groq"}
	b, err := NewNudgeBreaker(llm, BreakerConfig{Threshold: 1, Cooldown: time.Second}, clock.New(), logger.Discard())
	if err != nil {
		t.Fatalf("NewNudgeBreaker: %v", err)
	}
	if b.Name() != "groq" {
		t.Errorf("Name() = %q, want %q", b.Name(), "groq")
	}
}
