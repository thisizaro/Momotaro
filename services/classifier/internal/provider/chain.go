package provider

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/thisizaro/Momotaro/internal/platform/logger"
	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// Chain walks an ordered list of rungs, trying the next one only if the
// previous errored or failed validation. It always terminates in a valid
// answer as long as the last configured rung cannot fail (SPEC.md section
// 4.7; the rules engine is that rung).
type Chain struct {
	rungs []Provider
	log   *slog.Logger
}

// NewChain resolves names against registry, in order. An unknown name fails
// here, at construction time, so a config typo stops the pod at startup
// rather than degrading every classification silently (ENGINEERING.md
// section 5). log must not be nil.
func NewChain(names []string, registry map[string]Provider, log *slog.Logger) (*Chain, error) {
	if len(names) == 0 {
		return nil, fmt.Errorf("provider chain: at least one provider is required")
	}
	rungs := make([]Provider, 0, len(names))
	for _, name := range names {
		p, ok := registry[name]
		if !ok {
			return nil, fmt.Errorf("provider chain: unknown provider %q", name)
		}
		rungs = append(rungs, p)
	}
	return &Chain{rungs: rungs, log: log}, nil
}

// Classify walks the chain in order, recording a hop for every rung
// actually attempted (SPEC.md section 4.7). force_rules_only skips every
// rung not named RulesName (SPEC.md section 4.8), the load generator's
// cost-safety switch.
func (c *Chain) Classify(ctx context.Context, req *classifierv1.ClassifyRequest) (*classifierv1.ClassifyResponse, error) {
	rungs := c.rungs
	if req.GetForceRulesOnly() {
		rungs = onlyRulesRung(rungs)
	}

	hops := make([]*commonv1.ProviderHop, 0, len(rungs))
	for _, rung := range rungs {
		resp, err := rung.Classify(ctx, req)
		if err != nil {
			hops = append(hops, newHop(rung.Name(), "error"))
			c.log.Warn("provider rung failed",
				logger.KeyProvider, rung.Name(),
				"result", "error",
				logger.KeyError, err,
			)
			continue
		}

		if verr := validate(resp); verr != nil {
			hops = append(hops, newHop(rung.Name(), "schema_invalid"))
			c.log.Warn("provider rung returned an invalid response",
				logger.KeyProvider, rung.Name(),
				"result", "schema_invalid",
				logger.KeyError, verr,
			)
			continue
		}

		hops = append(hops, newHop(rung.Name(), "ok"))
		resp.Hops = hops
		resp.Source = sourceFor(rung.Name())
		return resp, nil
	}

	return nil, fmt.Errorf("provider chain: no rung produced a valid response")
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
