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

// Hop result vocabulary. ProviderHop.result (proto/common/v1/common.proto)
// documents these as the values a hop may carry; before Unit A they existed
// only as bare string literals at three call sites in chain.go, which meant a
// timeout was indistinguishable from a 500 in the audit trail and two of the
// documented values had no producer at all.
//
// Keep this the single source of the vocabulary. Unit E encodes hops into one
// delimited column, so a value added here without thought is a value that has
// to survive that round trip.
const (
	// HopOK: this rung answered and its answer passed validation.
	HopOK = "ok"
	// HopError: the rung returned an error that was not a deadline.
	HopError = "error"
	// HopTimeout: the rung exceeded the per-rung budget (or the caller's
	// deadline expired while it was working).
	HopTimeout = "timeout"
	// HopSchemaInvalid: the rung answered, but named a bucket or action
	// outside the enum, or a confidence outside [0,1]. An answer this
	// service cannot trust is a rung failure, not an answer (validate.go).
	HopSchemaInvalid = "schema_invalid"
	// HopDeadlineExhausted: the rung was never called, because too little of
	// the caller's deadline remained to be worth spending on it (budget.go).
	// Distinct from HopTimeout on purpose: nothing was attempted, so nothing
	// was paid for.
	HopDeadlineExhausted = "deadline_exhausted"
	// HopCircuitOpen: the rung was skipped because its circuit breaker is
	// open (breaker.go). No call was made.
	HopCircuitOpen = "circuit_open"
	// HopRateLimited: the provider answered 429. Kept distinct from
	// HopError because "we were throttled" and "the provider is broken"
	// call for different responses: the first is self-inflicted and
	// resolves on a known schedule, the second needs someone to look.
	HopRateLimited = "rate_limited"
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
