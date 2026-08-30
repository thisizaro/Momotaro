package provider

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/clock"
	"github.com/thisizaro/Momotaro/internal/platform/logger"
	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
)

// NudgeBreaker wraps one ComposeNudge rung and stops calling it once it is
// clearly not working, the ComposeNudge equivalent of Breaker (breaker.go).
//
// Deliberately a self-contained mirror rather than a wrapper sharing
// runtime state with Breaker: see the file comment on
// nudge_breaker_test.go for why, and docs/PHASE5_IMPLEMENTATION.md Unit E /
// docs/DECISIONS.md for the tradeoff recorded in full.
type NudgeBreaker struct {
	inner NudgeProvider
	cfg   BreakerConfig
	clk   clock.Clock
	log   *slog.Logger

	mu            sync.Mutex
	consecutive   int
	openUntil     time.Time
	trialInFlight bool
}

// NewNudgeBreaker wraps inner. See Breaker's constructor for why the rung
// that cannot fail (the static Hinglish template, named RulesName) must
// never be wrapped: an open breaker in front of it would leave the chain
// with no answer at all.
func NewNudgeBreaker(inner NudgeProvider, cfg BreakerConfig, clk clock.Clock, log *slog.Logger) (*NudgeBreaker, error) {
	if inner == nil {
		return nil, fmt.Errorf("nudge breaker: inner provider is required")
	}
	if inner.Name() == RulesName {
		return nil, fmt.Errorf("nudge breaker: refusing to wrap %q, it is the rung that cannot fail", RulesName)
	}
	if cfg.Threshold <= 0 {
		return nil, fmt.Errorf("nudge breaker: threshold must be positive, got %d", cfg.Threshold)
	}
	if cfg.Cooldown <= 0 {
		return nil, fmt.Errorf("nudge breaker: cooldown must be positive, got %s", cfg.Cooldown)
	}
	return &NudgeBreaker{inner: inner, cfg: cfg, clk: clk, log: log}, nil
}

// Name reports the wrapped rung's name.
func (b *NudgeBreaker) Name() string { return b.inner.Name() }

// ComposeNudge calls the wrapped rung unless the circuit is open.
func (b *NudgeBreaker) ComposeNudge(ctx context.Context, req *classifierv1.ComposeNudgeRequest) (*classifierv1.ComposeNudgeResponse, error) {
	isTrial, ok := b.admit()
	if !ok {
		return nil, ErrCircuitOpen
	}

	resp, err := b.inner.ComposeNudge(ctx, req)
	b.record(err, resp, req.GetMaxChars(), isTrial)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (b *NudgeBreaker) admit() (isTrial bool, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.openUntil.IsZero() {
		return false, true
	}
	if b.clk.Now().Before(b.openUntil) {
		return false, false
	}
	if b.trialInFlight {
		return false, false
	}
	b.trialInFlight = true
	return true, true
}

// record folds one outcome back into the breaker's state. Same rule as
// Breaker.record: a response that fails validateNudge counts as a failure
// even though the rung itself reported success, because a rung emitting
// unusable text is as useless as one that is down.
func (b *NudgeBreaker) record(err error, resp *classifierv1.ComposeNudgeResponse, maxChars int32, isTrial bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if isTrial {
		b.trialInFlight = false
	}

	if err == nil {
		if verr := validateNudge(resp, maxChars); verr != nil {
			err = fmt.Errorf("schema invalid: %w", verr)
		}
	}

	if err == nil {
		if !b.openUntil.IsZero() {
			b.log.Info("nudge circuit closed, trial request succeeded", logger.KeyProvider, b.inner.Name())
		}
		b.consecutive = 0
		b.openUntil = time.Time{}
		return
	}

	b.consecutive++

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
func (b *NudgeBreaker) openCircuit(cooldown time.Duration, reason, cooldownSource string, cause error) {
	b.openUntil = b.clk.Now().Add(cooldown)
	b.log.Warn("nudge circuit opened",
		logger.KeyProvider, b.inner.Name(),
		"reason", reason,
		"consecutive_failures", b.consecutive,
		"cooldown", cooldown.String(),
		"cooldown_source", cooldownSource,
		logger.KeyError, cause,
	)
}
