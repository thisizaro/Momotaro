// Phase 5 Unit E: ComposeNudge's provider chain.
//
// Mirrors chain.go's Chain/Classify shape deliberately, reusing everything
// that does not depend on the request/response type: rungCtx (budget.go),
// newHop/the hop-result constants (provider.go), hopResultForError, and the
// terminal-rung construction logic (validateConfig/validateChainOrder/
// resolveRungs, chain.go). Only the walk itself and validation are new,
// because those are the two places the Classify/ComposeNudge shapes
// actually differ.
package provider

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/thisizaro/Momotaro/internal/platform/logger"
	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// NudgeProvider is one rung of the nudge-composition chain, the ComposeNudge
// equivalent of Provider. Implementations must not hold mutable per-request
// state, same rule as Provider: a chain is built once at startup and shared
// across concurrent gRPC requests.
type NudgeProvider interface {
	Name() string
	ComposeNudge(ctx context.Context, req *classifierv1.ComposeNudgeRequest) (*classifierv1.ComposeNudgeResponse, error)
}

// NudgeChain walks an ordered list of nudge-composition rungs, the
// ComposeNudge equivalent of Chain. It always terminates in a valid answer
// because NewNudgeChain requires the last rung to be named RulesName (the
// static Hinglish template, which does no I/O and cannot fail), the same
// invariant Chain enforces for Classify.
type NudgeChain struct {
	rungs []NudgeProvider
	cfg   Config
	log   *slog.Logger
}

// NewNudgeChain resolves names against registry, in order, enforcing the
// same construction invariants NewChain does (validateConfig,
// validateChainOrder, resolveRungs are shared with it in chain.go).
func NewNudgeChain(names []string, registry map[string]NudgeProvider, cfg Config, log *slog.Logger) (*NudgeChain, error) {
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
	return &NudgeChain{rungs: rungs, cfg: cfg, log: log}, nil
}

// ComposeNudge walks the chain in order, recording a hop for every rung
// actually attempted and for every rung deliberately skipped for want of
// budget, the ComposeNudge equivalent of Chain.Classify.
// ForceTemplateOnly skips every rung not named RulesName, the nudge
// equivalent of Classify's force_rules_only: the load generator's
// cost-safety switch, and a way to prove the fallback path without
// depending on a live provider failing on cue.
func (c *NudgeChain) ComposeNudge(ctx context.Context, req *classifierv1.ComposeNudgeRequest) (*classifierv1.ComposeNudgeResponse, error) {
	rungs := c.rungs
	if req.GetForceTemplateOnly() {
		rungs = onlyTemplateRung(rungs)
	}

	hops := make([]*commonv1.ProviderHop, 0, len(rungs))
	for i, rung := range rungs {
		isTerminal := i == len(rungs)-1

		rungContext, cancel, affordable := rungCtx(ctx, c.cfg.RungTimeout, c.cfg.Reserve, isTerminal)
		if !affordable {
			cancel()
			hops = append(hops, newHop(rung.Name(), HopDeadlineExhausted))
			c.log.Warn("skipped nudge rung, too little of the caller's deadline left",
				logger.KeyProvider, rung.Name(),
				"result", HopDeadlineExhausted,
			)
			continue
		}

		resp, err := rung.ComposeNudge(rungContext, req)
		cancel()

		if err != nil {
			result := hopResultForError(err)
			hops = append(hops, newHop(rung.Name(), result))
			c.log.Warn("nudge rung failed",
				logger.KeyProvider, rung.Name(),
				"result", result,
				logger.KeyError, err,
			)
			continue
		}

		if verr := validateNudge(resp, req.GetMaxChars()); verr != nil {
			hops = append(hops, newHop(rung.Name(), HopSchemaInvalid))
			c.log.Warn("nudge rung returned an invalid response",
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

	// Unreachable while the terminal rung is the static template, which
	// cannot fail: NewNudgeChain enforces that it is present and last.
	return nil, fmt.Errorf("nudge chain: no rung produced a valid response")
}

func onlyTemplateRung(rungs []NudgeProvider) []NudgeProvider {
	out := make([]NudgeProvider, 0, 1)
	for _, r := range rungs {
		if r.Name() == RulesName {
			out = append(out, r)
		}
	}
	return out
}
