// Package provider implements the classifier's LLM provider chain skeleton
// (SPEC.md section 4.7, ARCHITECTURE.md section 5): an ordered list of
// rungs, tried in order, that always terminates in a valid answer because
// the deterministic rules engine is always the last rung and cannot fail.
package provider

import (
	"context"

	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// RulesName is the rung name the deterministic rules engine registers
// under. The chain uses it to resolve the coarse Source (SPEC.md section
// 4.6) and to filter for force_rules_only (SPEC.md section 4.8), so it must
// match the name the rules provider is registered under in the chain's
// registry.
const RulesName = "rules"

// Provider is one rung of the chain. Implementations must not hold mutable
// per-request state: a chain is built once at startup and shared across
// concurrent gRPC requests.
type Provider interface {
	// Name identifies this rung for hop recording and Source resolution,
	// e.g. "rules", "claude".
	Name() string
	Classify(ctx context.Context, req *classifierv1.ClassifyRequest) (*classifierv1.ClassifyResponse, error)
}

func newHop(providerName, result string) *commonv1.ProviderHop {
	return &commonv1.ProviderHop{Provider: providerName, Result: result}
}
