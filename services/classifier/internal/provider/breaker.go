package provider

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/clock"
	"github.com/thisizaro/Momotaro/internal/platform/logger"
	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
)

// ErrCircuitOpen is returned instead of calling a provider whose breaker is
// open. The chain records HopCircuitOpen and moves to the next rung.
var ErrCircuitOpen = errors.New("provider circuit is open")

// rateLimited is the structural check for provider throttling.
//
// Declared as an interface rather than importing the llm package's concrete
// error type on purpose: this package is the generic chain and must not know
// which vendors exist. Any error exposing RateLimited() is treated as a 429,
// which keeps the dependency pointing one way (a vendor package may know about
// the chain's vocabulary; the chain must not know about vendors).
type rateLimited interface {
	// RateLimited reports the provider's requested cooldown, or zero when it
	// supplied none.
	RateLimited() time.Duration
}

func isRateLimited(err error) bool {
	var rl rateLimited
	return errors.As(err, &rl)
}

func rateLimitCooldown(err error) time.Duration {
	var rl rateLimited
	if errors.As(err, &rl) {
		return rl.RateLimited()
	}
	return 0
}

// BreakerConfig bounds how much a failing provider is allowed to cost.
type BreakerConfig struct {
	// Threshold is how many consecutive failures close the tap. A 429
	// bypasses it entirely and opens on the first one.
	Threshold int
	// Cooldown is how long the breaker stays open, and the fallback when a
	// 429 carries no Retry-After.
	Cooldown time.Duration
}

// Breaker wraps one rung and stops calling it once it is clearly not working.
//
// Why a timeout alone is not enough (ARCHITECTURE.md section 5): with a dead
// provider and no breaker, every single record pays the full LLM_TIMEOUT
// before falling through. At PRD.md section 10's target of 50 records/sec and
// a 2s timeout, that is 100 seconds of waiting per second of traffic. One
// external outage becomes a pipeline-wide stall. The breaker turns it into a
// barely visible dip, trading classification quality for throughput, which is
// the right way round.
//
// It wraps a rung and is itself a Provider, which is the shape chain.go's walk
// was built for (SPEC.md section 4.7) and is why no change to that walk was
// needed. Do not move breaker state into the chain: the next wrapper would
// have to go there too.
//
// State is per-pod and in-memory, deliberately. ARCHITECTURE.md section 5:
// "Breaker state is deliberately per-pod and in-memory, not shared: it is a
// local health observation, and a shared breaker would itself become a
// coordination point and a shared failure mode. Do not build distributed
// breaker state." That means do not add Redis here, however tempting.
type Breaker struct {
	inner Provider
	cfg   BreakerConfig
	clk   clock.Clock
	log   *slog.Logger

	// A single chain is built once at startup and shared across every
	// concurrent gRPC request, so all of this is contended.
	mu          sync.Mutex
	consecutive int
	// openUntil is zero when closed. Non-zero and in the future means open;
	// non-zero and in the past means half-open, awaiting a trial.
	openUntil time.Time
	// trialInFlight makes the half-open state admit exactly one request. The
	// point of half-open is to spend ONE call finding out, not to reopen the
	// floodgates and discover the provider is still down 50 calls later.
	trialInFlight bool
}

// NewBreaker wraps inner. clk is injected because the cooldown is an interval
// the breaker measures itself, which a test must be able to drive; contrast
// budget.go's rungCtx, which must use the real clock because it compares
// against a real context deadline.
func NewBreaker(inner Provider, cfg BreakerConfig, clk clock.Clock, log *slog.Logger) (*Breaker, error) {
	if inner == nil {
		return nil, fmt.Errorf("breaker: inner provider is required")
	}
	// Wrapping the rules rung would be actively harmful: it is the rung that
	// cannot fail, and an open breaker in front of it would leave the chain
	// with no answer at all, destroying the guarantee NewChain enforces.
	if inner.Name() == RulesName {
		return nil, fmt.Errorf("breaker: refusing to wrap %q, it is the rung that cannot fail", RulesName)
	}
	if cfg.Threshold <= 0 {
		return nil, fmt.Errorf("breaker: threshold must be positive, got %d", cfg.Threshold)
	}
	if cfg.Cooldown <= 0 {
		return nil, fmt.Errorf("breaker: cooldown must be positive, got %s", cfg.Cooldown)
	}
	return &Breaker{inner: inner, cfg: cfg, clk: clk, log: log}, nil
}

// Name reports the wrapped rung's name, so hops and Source resolution are
// unchanged by the wrapping. A breaker is not a different provider.
func (b *Breaker) Name() string { return b.inner.Name() }

// Classify calls the wrapped rung unless the circuit is open.
func (b *Breaker) Classify(ctx context.Context, req *classifierv1.ClassifyRequest) (*classifierv1.ClassifyResponse, error) {
	isTrial, ok := b.admit()
	if !ok {
		return nil, ErrCircuitOpen
	}

	resp, err := b.inner.Classify(ctx, req)
	b.record(err, resp, isTrial)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// admit decides whether this request reaches the provider, and whether it is
// the half-open trial.
func (b *Breaker) admit() (isTrial bool, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.openUntil.IsZero() {
		return false, true // closed
	}
	if b.clk.Now().Before(b.openUntil) {
		return false, false // open
	}
	// Half-open: exactly one request gets through to find out.
	if b.trialInFlight {
		return false, false
	}
	b.trialInFlight = true
	return true, true
}

// record folds one outcome back into the breaker's state.
func (b *Breaker) record(err error, resp *classifierv1.ClassifyResponse, isTrial bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if isTrial {
		b.trialInFlight = false
	}

	// A response that fails validation counts as a failure even though the
	// rung reported success. validate() is this package's own function, so
	// calling it here is a second cheap call rather than duplicated logic, and
	// without it a provider emitting well-formed garbage would never trip the
	// breaker: chain.go validates AFTER the rung returns, so the breaker would
	// only ever see nil. A provider returning garbage is as useless as one
	// that is down, and the breaker exists to stop paying for useless calls
	// (docs/DECISIONS.md 2026-08-28).
	if err == nil {
		if verr := validate(resp); verr != nil {
			err = fmt.Errorf("schema invalid: %w", verr)
		}
	}

	if err == nil {
		if !b.openUntil.IsZero() {
			b.log.Info("provider circuit closed, trial request succeeded",
				logger.KeyProvider, b.inner.Name())
		}
		b.consecutive = 0
		b.openUntil = time.Time{}
		return
	}

	b.consecutive++

	// A 429 is not "N failures suggest a problem", it is the provider stating
	// a fact about when it will serve us again. Waiting for the threshold
	// means paying Threshold-1 more calls that are guaranteed to fail, which
	// on a demo is a visible multi-second stall (docs/DECISIONS.md
	// 2026-08-28). Open now, and prefer the provider's own number.
	if isRateLimited(err) {
		cooldown := rateLimitCooldown(err)
		source := "retry-after"
		if cooldown <= 0 {
			cooldown = b.cfg.Cooldown
			source = "configured default"
		}
		b.openCircuit(cooldown, "rate limited", source, err)
		return
	}

	if b.consecutive >= b.cfg.Threshold {
		b.openCircuit(b.cfg.Cooldown, "consecutive failures", "configured default", err)
	}
}

// openCircuit must be called with b.mu held.
func (b *Breaker) openCircuit(cooldown time.Duration, reason, cooldownSource string, cause error) {
	b.openUntil = b.clk.Now().Add(cooldown)
	// The Warn is the compensating control for the deferred llm_circuit_state
	// metric. ARCHITECTURE.md section 13 puts metric export behind a shared
	// interceptor in Phase 4, and hand-rolling an exporter in one service is
	// exactly what that section forbids, so until then this log line is how
	// anyone finds out a provider was taken out of the chain.
	b.log.Warn("provider circuit opened",
		logger.KeyProvider, b.inner.Name(),
		"reason", reason,
		"consecutive_failures", b.consecutive,
		"cooldown", cooldown.String(),
		"cooldown_source", cooldownSource,
		logger.KeyError, cause,
	)
}
