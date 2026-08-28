package provider

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/clock"
	"github.com/thisizaro/Momotaro/internal/platform/logger"
	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// Nothing in this file asserts wall-clock time.
//
// PLAN.md words this unit as "a test proving that a sustained provider outage
// does not make every record pay the full timeout", and the literal reading is
// a latency assertion. This repo has five docs/INCIDENTS.md entries about
// timing-dependent tests, so the provable version asserts CALL COUNTS on the
// wrapped provider and hop results in the trail, with clock.Fake driving the
// cooldown (docs/PHASE3_IMPLEMENTATION.md Flaw 6). If the provider is never
// called, no timeout can be paid; that is the property, and it is binary.

var errProviderDown = errors.New("provider exploded")

// countingRung records how many times it was actually invoked, which is the
// entire measurement in this file.
type countingRung struct {
	name string

	mu    sync.Mutex
	calls int
	err   error
	resp  *classifierv1.ClassifyResponse
	// gate, when non-nil, holds the call open until the test releases it.
	// Needed to keep the half-open trial genuinely in flight while other
	// racers arrive; see the concurrency test for why.
	gate chan struct{}
}

func (c *countingRung) Name() string { return c.name }

func (c *countingRung) Classify(context.Context, *classifierv1.ClassifyRequest) (*classifierv1.ClassifyResponse, error) {
	c.mu.Lock()
	c.calls++
	gate, resp, err := c.gate, c.resp, c.err
	c.mu.Unlock()

	if gate != nil {
		<-gate
	}
	return resp, err
}

func (c *countingRung) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *countingRung) setOutcome(resp *classifierv1.ClassifyResponse, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resp, c.err = resp, err
}

func (c *countingRung) setGate(g chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gate = g
}

// throttled satisfies the breaker's structural rateLimited check, standing in
// for llm.RateLimitedError without this package importing that one.
type throttled struct{ after time.Duration }

func (t throttled) Error() string              { return "rate limited" }
func (t throttled) RateLimited() time.Duration { return t.after }

func testBreaker(t *testing.T, inner Provider, cfg BreakerConfig) (*Breaker, *clock.Fake) {
	t.Helper()
	fake := clock.NewFake(time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC))
	b, err := NewBreaker(inner, cfg, fake, logger.Discard())
	if err != nil {
		t.Fatalf("NewBreaker: %v", err)
	}
	return b, fake
}

// --- The headline property -------------------------------------------------

func TestBreakerStopsCallingADeadProvider(t *testing.T) {
	const threshold = 5
	const afterOpen = 50

	down := &countingRung{name: "llm", err: errProviderDown}
	b, _ := testBreaker(t, down, BreakerConfig{Threshold: threshold, Cooldown: 30 * time.Second})

	for i := 0; i < threshold; i++ {
		if _, err := b.Classify(context.Background(), &classifierv1.ClassifyRequest{}); err == nil {
			t.Fatalf("call %d: want the provider's error", i)
		}
	}
	if got := down.callCount(); got != threshold {
		t.Fatalf("provider calls before the breaker opened = %d, want %d", got, threshold)
	}

	// The whole point: the next fifty records cost nothing.
	for i := 0; i < afterOpen; i++ {
		_, err := b.Classify(context.Background(), &classifierv1.ClassifyRequest{})
		if !errors.Is(err, ErrCircuitOpen) {
			t.Fatalf("request %d after opening: err = %v, want ErrCircuitOpen", i, err)
		}
	}
	if got := down.callCount(); got != threshold {
		t.Errorf("provider was called %d times, want it to stop at %d: every extra call is a full timeout paid per record",
			got, threshold)
	}
}

// The chain must turn an open circuit into its own hop result, not a generic
// error, and must still answer from the rung that cannot fail.
func TestChainRecordsCircuitOpenAndStillAnswers(t *testing.T) {
	down := &countingRung{name: "llm", err: errProviderDown}
	b, _ := testBreaker(t, down, BreakerConfig{Threshold: 1, Cooldown: 30 * time.Second})
	rules := &fakeRung{name: RulesName, resp: validResponse()}

	c, err := NewChain([]string{"llm", RulesName},
		map[string]Provider{"llm": b, RulesName: rules}, testConfig(), logger.Discard())
	if err != nil {
		t.Fatalf("NewChain: %v", err)
	}

	// First request trips the breaker (threshold 1) and is recorded as a
	// plain error, since the call really was attempted.
	resp, err := c.Classify(context.Background(), &classifierv1.ClassifyRequest{})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if got := resp.GetHops()[0].GetResult(); got != HopError {
		t.Errorf("first hop = %q, want %q: the call was made and failed", got, HopError)
	}

	// Every subsequent request is skipped, and says so.
	resp, err = c.Classify(context.Background(), &classifierv1.ClassifyRequest{})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	hops := resp.GetHops()
	if len(hops) != 2 || hops[0].GetResult() != HopCircuitOpen || hops[1].GetResult() != HopOK {
		t.Errorf("hops = %+v, want [{llm circuit_open} {rules ok}]", hops)
	}
	if resp.GetSource() != commonv1.Source_SOURCE_RULES_FALLBACK {
		t.Errorf("Source = %v, want the chain to still answer", resp.GetSource())
	}
}

// --- Rate limiting, the failure most likely to actually fire ---------------

func TestBreakerOpensOnASingle429WithoutWaitingForTheThreshold(t *testing.T) {
	down := &countingRung{name: "llm", err: throttled{after: 90 * time.Second}}
	b, fake := testBreaker(t, down, BreakerConfig{Threshold: 5, Cooldown: 30 * time.Second})

	if _, err := b.Classify(context.Background(), &classifierv1.ClassifyRequest{}); err == nil {
		t.Fatal("want the rate-limit error")
	}
	if _, err := b.Classify(context.Background(), &classifierv1.ClassifyRequest{}); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("err = %v, want ErrCircuitOpen after a single 429: waiting for 5 failures means paying 4 doomed calls", err)
	}
	if got := down.callCount(); got != 1 {
		t.Errorf("provider calls = %d, want exactly 1", got)
	}

	// Retry-After (90s) must win over the configured cooldown (30s), or we
	// hammer a provider that told us precisely when to come back.
	fake.Advance(31 * time.Second)
	if _, err := b.Classify(context.Background(), &classifierv1.ClassifyRequest{}); !errors.Is(err, ErrCircuitOpen) {
		t.Error("breaker reopened at the configured 30s cooldown, ignoring the provider's 90s Retry-After")
	}
	fake.Advance(60 * time.Second)
	if _, err := b.Classify(context.Background(), &classifierv1.ClassifyRequest{}); errors.Is(err, ErrCircuitOpen) {
		t.Error("breaker still open past the 90s Retry-After")
	}
}

// Gemini's live rate limit sent no Retry-After at all, so this branch is not
// hypothetical (docs/PHASE3_IMPLEMENTATION.md Unit B).
func TestBreakerFallsBackToTheConfiguredCooldownWhenNoRetryAfterIsSupplied(t *testing.T) {
	down := &countingRung{name: "llm", err: throttled{after: 0}}
	b, fake := testBreaker(t, down, BreakerConfig{Threshold: 5, Cooldown: 30 * time.Second})

	b.Classify(context.Background(), &classifierv1.ClassifyRequest{})
	if _, err := b.Classify(context.Background(), &classifierv1.ClassifyRequest{}); !errors.Is(err, ErrCircuitOpen) {
		t.Fatal("want the circuit open after a 429 with no Retry-After")
	}
	fake.Advance(29 * time.Second)
	if _, err := b.Classify(context.Background(), &classifierv1.ClassifyRequest{}); !errors.Is(err, ErrCircuitOpen) {
		t.Error("breaker reopened before the configured cooldown elapsed")
	}
	fake.Advance(2 * time.Second)
	if _, err := b.Classify(context.Background(), &classifierv1.ClassifyRequest{}); errors.Is(err, ErrCircuitOpen) {
		t.Error("breaker did not reopen after the configured cooldown")
	}
}

// --- Half-open ------------------------------------------------------------

// The half-open trial must admit exactly ONE request, under concurrency.
// This is the same "exactly once under contention" property Phase 2's Unit L
// spent two iterations failing to prove (docs/INCIDENTS.md 2026-08-26), so it
// is raced rather than asserted serially.
func TestBreakerHalfOpenAdmitsExactlyOneTrialUnderConcurrency(t *testing.T) {
	const racers = 25

	down := &countingRung{name: "llm", err: errProviderDown}
	b, fake := testBreaker(t, down, BreakerConfig{Threshold: 1, Cooldown: 30 * time.Second})

	b.Classify(context.Background(), &classifierv1.ClassifyRequest{}) // trip it
	callsWhenOpen := down.callCount()

	// Hold the trial open. Without this the test is nearly useless: the first
	// admitted racer fails, record() immediately re-opens the circuit, and the
	// remaining racers are refused for that reason rather than by
	// trialInFlight. Measured against deliberately broken locking, that
	// version caught the bug 4 times in 20 runs. Holding the trial in flight
	// keeps every racer in the half-open window, which is the state actually
	// under test, and takes the catch rate to 20 in 20.
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
			b.Classify(context.Background(), &classifierv1.ClassifyRequest{})
		}()
	}
	close(start)

	// Let every racer reach admit() while the trial is still in flight. This
	// is a wall-clock sleep, but it fails safe: a longer one only makes the
	// pile-up more complete, and the assertion is a call count, not a
	// duration (docs/PHASE3_IMPLEMENTATION.md Flaw 6).
	time.Sleep(100 * time.Millisecond)
	close(gate)
	wg.Wait()

	if got := down.callCount() - callsWhenOpen; got != 1 {
		t.Errorf("half-open admitted %d requests, want exactly 1: the point is to spend one call finding out, not to reopen the floodgates", got)
	}
}

func TestBreakerClosesOnASuccessfulTrialAndReopensOnAFailedOne(t *testing.T) {
	rung := &countingRung{name: "llm", err: errProviderDown}
	b, fake := testBreaker(t, rung, BreakerConfig{Threshold: 1, Cooldown: 30 * time.Second})

	b.Classify(context.Background(), &classifierv1.ClassifyRequest{}) // open
	fake.Advance(31 * time.Second)

	// Failed trial: reopens, and the cooldown restarts rather than the
	// breaker flapping open on every subsequent request.
	if _, err := b.Classify(context.Background(), &classifierv1.ClassifyRequest{}); errors.Is(err, ErrCircuitOpen) {
		t.Fatal("the trial request should have reached the provider")
	}
	if _, err := b.Classify(context.Background(), &classifierv1.ClassifyRequest{}); !errors.Is(err, ErrCircuitOpen) {
		t.Error("a failed trial must reopen the circuit")
	}

	// Successful trial: closes, and normal traffic resumes.
	fake.Advance(31 * time.Second)
	rung.setOutcome(validResponse(), nil)
	if _, err := b.Classify(context.Background(), &classifierv1.ClassifyRequest{}); err != nil {
		t.Fatalf("trial request: %v", err)
	}
	before := rung.callCount()
	for i := 0; i < 5; i++ {
		if _, err := b.Classify(context.Background(), &classifierv1.ClassifyRequest{}); err != nil {
			t.Fatalf("request after the circuit closed: %v", err)
		}
	}
	if got := rung.callCount() - before; got != 5 {
		t.Errorf("provider calls after closing = %d, want 5: the circuit should be fully closed", got)
	}
}

// A run of failures that does not reach the threshold, then a success, must
// not leave the breaker one failure from opening forever.
func TestBreakerResetsTheFailureRunOnSuccess(t *testing.T) {
	rung := &countingRung{name: "llm", err: errProviderDown}
	b, _ := testBreaker(t, rung, BreakerConfig{Threshold: 3, Cooldown: 30 * time.Second})

	b.Classify(context.Background(), &classifierv1.ClassifyRequest{})
	b.Classify(context.Background(), &classifierv1.ClassifyRequest{})
	rung.setOutcome(validResponse(), nil)
	b.Classify(context.Background(), &classifierv1.ClassifyRequest{})

	rung.setOutcome(nil, errProviderDown)
	b.Classify(context.Background(), &classifierv1.ClassifyRequest{})
	b.Classify(context.Background(), &classifierv1.ClassifyRequest{})
	if _, err := b.Classify(context.Background(), &classifierv1.ClassifyRequest{}); errors.Is(err, ErrCircuitOpen) {
		t.Error("circuit opened after 2 failures with threshold 3: the run was not reset by the intervening success")
	}
}

// --- Schema failures count -------------------------------------------------

// chain.go validates AFTER a rung returns, so a rung emitting well-formed
// garbage reports nil to the breaker and would never trip it. A provider
// returning garbage is as useless as one that is down.
func TestBreakerCountsAnInvalidResponseAsAFailure(t *testing.T) {
	bad := &countingRung{name: "llm", resp: invalidResponses()["bucket outside enum"]}
	b, _ := testBreaker(t, bad, BreakerConfig{Threshold: 2, Cooldown: 30 * time.Second})

	for i := 0; i < 2; i++ {
		if _, err := b.Classify(context.Background(), &classifierv1.ClassifyRequest{}); err != nil {
			t.Fatalf("call %d: the rung reported success, so the breaker should pass it through: %v", i, err)
		}
	}
	if _, err := b.Classify(context.Background(), &classifierv1.ClassifyRequest{}); !errors.Is(err, ErrCircuitOpen) {
		t.Error("two schema-invalid responses did not trip a threshold-2 breaker")
	}
}

// --- Construction ----------------------------------------------------------

func TestNewBreakerRefusesToWrapTheRungThatCannotFail(t *testing.T) {
	rules := &fakeRung{name: RulesName, resp: validResponse()}
	_, err := NewBreaker(rules, BreakerConfig{Threshold: 1, Cooldown: time.Second}, clock.NewFake(time.Now()), logger.Discard())
	if err == nil {
		t.Fatal("NewBreaker on the rules rung: want error, got nil")
	}
	// An open breaker in front of rules leaves the chain with no answer at
	// all, destroying the guarantee NewChain enforces, so the error should
	// say which rung was refused rather than being generic.
	if !strings.Contains(err.Error(), RulesName) {
		t.Errorf("error = %q, want it to name %q", err, RulesName)
	}
}

func TestNewBreakerRejectsANonsensicalConfig(t *testing.T) {
	inner := &countingRung{name: "llm"}
	cases := map[string]BreakerConfig{
		"zero threshold":     {Threshold: 0, Cooldown: time.Second},
		"negative threshold": {Threshold: -1, Cooldown: time.Second},
		"zero cooldown":      {Threshold: 1, Cooldown: 0},
		"negative cooldown":  {Threshold: 1, Cooldown: -time.Second},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewBreaker(inner, cfg, clock.NewFake(time.Now()), logger.Discard()); err == nil {
				t.Errorf("NewBreaker with %s: want error, got nil", name)
			}
		})
	}
}

func TestBreakerReportsTheWrappedRungsName(t *testing.T) {
	inner := &countingRung{name: "llm"}
	b, _ := testBreaker(t, inner, BreakerConfig{Threshold: 1, Cooldown: time.Second})
	if b.Name() != "llm" {
		t.Errorf("Name() = %q, want the wrapped rung's name: a breaker is not a different provider, and hops key on this",
			b.Name())
	}
}
