package provider

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/logger"
	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// Config is the chain's timing budget. Both values are validated in NewChain
// so a nonsensical one stops the pod at startup rather than silently making
// every rung either unbounded or unrunnable (docs/ENGINEERING.md section 5).
type Config struct {
	// RungTimeout caps any single non-terminal rung (LLM_TIMEOUT).
	RungTimeout time.Duration
	// Reserve is held back from the caller's deadline for the terminal rung
	// and the response marshal (CHAIN_RESERVE). See budget.go.
	Reserve time.Duration
}

// Chain walks an ordered list of rungs, trying the next one only if the
// previous errored, timed out, or failed validation. It always terminates in a
// valid answer because NewChain requires the last rung to be the deterministic
// rules engine, which does no I/O and cannot fail (SPEC.md section 4.7).
type Chain struct {
	rungs []Provider
	cfg   Config
	log   *slog.Logger
}

// NewChain resolves names against registry, in order, and enforces the
// invariants the chain's guarantees actually rest on. Everything here fails at
// construction, so a config mistake stops the pod instead of degrading every
// classification silently (docs/ENGINEERING.md section 5). log must not be nil.
func NewChain(names []string, registry map[string]Provider, cfg Config, log *slog.Logger) (*Chain, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	if err := validateChainOrder(names); err != nil {
		return nil, err
	}
	rungs, err := resolveRungs(names, registry)
	if err != nil {
		return nil, err
	}
	return &Chain{rungs: rungs, cfg: cfg, log: log}, nil
}

// validateConfig checks the chain's timing budget. Shared by NewChain and
// NewNudgeChain (nudge.go): both walk a chain under the same Config shape,
// so a nonsensical budget must stop either at construction identically.
func validateConfig(cfg Config) error {
	if cfg.RungTimeout <= 0 {
		return fmt.Errorf("provider chain: rung timeout must be positive, got %s", cfg.RungTimeout)
	}
	if cfg.Reserve < 0 {
		return fmt.Errorf("provider chain: reserve must not be negative, got %s", cfg.Reserve)
	}
	return nil
}

// validateChainOrder enforces the terminal-rung invariant: the deterministic
// fallback (named RulesName) must appear exactly once, last. Until Unit A
// this was a comment and a convention: a chain could be built as
// LLM_PROVIDER_CHAIN=groq, which starts cleanly and then dead-letters every
// record whose classification fails, because the chain runs out of rungs
// and returns an error the caller retries a bounded number of times before
// giving up (docs/PHASE3_IMPLEMENTATION.md Flaw 2).
//
// Shared by NewChain and NewNudgeChain (nudge.go): a chain that cannot
// terminate is the same failure mode regardless of which RPC it serves, and
// both terminal rungs (the rules engine, the static Hinglish template) are
// named RulesName for exactly this reason -- one canonical "the
// deterministic thing that cannot fail" concept, not two.
func validateChainOrder(names []string) error {
	if len(names) == 0 {
		return fmt.Errorf("provider chain: at least one provider is required")
	}
	rulesCount := 0
	for _, name := range names {
		if name == RulesName {
			rulesCount++
		}
	}
	if rulesCount == 0 {
		return fmt.Errorf("provider chain: %q must be in the chain, it is the only rung that cannot fail", RulesName)
	}
	if rulesCount > 1 {
		return fmt.Errorf("provider chain: %q appears %d times, expected exactly once", RulesName, rulesCount)
	}
	if names[len(names)-1] != RulesName {
		return fmt.Errorf("provider chain: %q must be last, got %q", RulesName, names[len(names)-1])
	}
	return nil
}

// resolveRungs looks up each name in registry, in order, rejecting a name
// that could not survive Unit E's hop encoding ("provider:result" pairs
// joined by ","). Generic over the rung type so NewChain and NewNudgeChain
// (nudge.go) share this instead of each carrying their own copy: the
// lookup-and-reject logic does not depend on what a rung's own methods do,
// only on it being registered under a clean name.
func resolveRungs[P any](names []string, registry map[string]P) ([]P, error) {
	rungs := make([]P, 0, len(names))
	for _, name := range names {
		if strings.ContainsAny(name, ":,") {
			return nil, fmt.Errorf("provider chain: provider name %q must not contain ':' or ','", name)
		}
		p, ok := registry[name]
		if !ok {
			return nil, fmt.Errorf("provider chain: unknown provider %q", name)
		}
		rungs = append(rungs, p)
	}
	return rungs, nil
}

// Classify walks the chain in order, recording a hop for every rung actually
// attempted and for every rung deliberately skipped for want of budget
// (SPEC.md section 4.7). force_rules_only skips every rung not named RulesName
// (SPEC.md section 4.8), the load generator's cost-safety switch and, from
// Phase 3 Unit H, the per-record sampling switch.
func (c *Chain) Classify(ctx context.Context, req *classifierv1.ClassifyRequest) (*classifierv1.ClassifyResponse, error) {
	rungs := c.rungs
	if req.GetForceRulesOnly() {
		rungs = onlyRulesRung(rungs)
	}

	hops := make([]*commonv1.ProviderHop, 0, len(rungs))
	for i, rung := range rungs {
		isTerminal := i == len(rungs)-1

		rungContext, cancel, affordable := rungCtx(ctx, c.cfg.RungTimeout, c.cfg.Reserve, isTerminal)
		if !affordable {
			cancel()
			hops = append(hops, newHop(rung.Name(), HopDeadlineExhausted))
			c.log.Warn("skipped provider rung, too little of the caller's deadline left",
				logger.KeyProvider, rung.Name(),
				"result", HopDeadlineExhausted,
			)
			continue
		}

		resp, err := rung.Classify(rungContext, req)
		// Not deferred: this loop runs once per rung, and a deferred cancel
		// would hold every rung's context open until Classify returns.
		cancel()

		if err != nil {
			result := hopResultForError(err)
			hops = append(hops, newHop(rung.Name(), result))
			c.log.Warn("provider rung failed",
				logger.KeyProvider, rung.Name(),
				"result", result,
				logger.KeyError, err,
			)
			continue
		}

		if verr := validate(resp); verr != nil {
			hops = append(hops, newHop(rung.Name(), HopSchemaInvalid))
			c.log.Warn("provider rung returned an invalid response",
				logger.KeyProvider, rung.Name(),
				"result", HopSchemaInvalid,
				logger.KeyError, verr,
			)
			continue
		}

		hops = append(hops, newHop(rung.Name(), HopOK))
		resp.Hops = hops
		resp.Source = sourceFor(rung.Name())
		return resp, nil
	}

	// Unreachable while the terminal rung is the rules engine, which cannot
	// fail: NewChain enforces that it is present and last. Kept as a real
	// error rather than a panic because a future terminal rung that can fail
	// should degrade, not crash the pod.
	return nil, fmt.Errorf("provider chain: no rung produced a valid response")
}

// hopResultForError maps a rung failure onto the hop vocabulary. Four
// distinct outcomes, because they call for four different responses and
// flattening them (as this chain did before Unit A) loses the difference:
// a timeout says tighten the budget or the provider is degraded, a 429 says
// we are spending faster than the tier allows, an open circuit says we
// already knew and did not bother asking, and an error says something is
// actually broken.
func hopResultForError(err error) string {
	switch {
	case errors.Is(err, ErrCircuitOpen):
		return HopCircuitOpen
	case isRateLimited(err):
		return HopRateLimited
	case errors.Is(err, context.DeadlineExceeded):
		return HopTimeout
	default:
		return HopError
	}
}

func onlyRulesRung(rungs []Provider) []Provider {
	out := make([]Provider, 0, 1)
	for _, r := range rungs {
		if r.Name() == RulesName {
			out = append(out, r)
		}
	}
	return out
}

// sourceFor is the coarse/detail resolution of SPEC.md section 4.6: source
// says which kind of thing answered, hops say which named rung it was.
func sourceFor(name string) commonv1.Source {
	if name == RulesName {
		return commonv1.Source_SOURCE_RULES_FALLBACK
	}
	return commonv1.Source_SOURCE_LLM
}
