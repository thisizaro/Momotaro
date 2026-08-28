package provider

import (
	"context"
	"time"
)

// Deadline budgeting for the provider chain (docs/PHASE3_IMPLEMENTATION.md
// Unit A, Flaw 3).
//
// Before this file, Chain.Classify handed the inbound context straight to
// every rung, so a hanging provider hung until the *caller's* deadline expired
// and the rules rung, the one that always answers, never ran at all. Adding a
// flat per-rung timeout is not enough on its own either: with LLM_TIMEOUT=2s
// and the Decision Engine's CALL_TIMEOUT=5s, a two-LLM chain that fully times
// out burns 4s before the terminal rung gets a turn, and the correct answer it
// then produces arrives after its caller has given up. The Decision Engine
// treats that as a failed Classify, retries three times and dead-letters the
// record (engine.go, maxClassifyAttempts). The fallback chain, whose whole
// purpose is to stop a provider failure from losing a record, would have
// become the mechanism that lost it.
//
// So each rung gets the smaller of the per-rung timeout and what is left of
// the caller's deadline after holding `reserve` back for the terminal rung and
// the response marshal.

// rungCtx derives the context a single rung runs under.
//
// ok is false when too little of the caller's deadline remains to be worth
// spending on this rung. The caller records a HopDeadlineExhausted hop and
// moves on WITHOUT invoking it: calling a provider you cannot afford to wait
// for buys nothing and costs a request.
//
// isTerminal marks the last rung, which is exempt from both the per-rung
// timeout and the reserve. That rung is the deterministic rules engine
// (NewChain enforces it), which does no I/O and cannot fail, so capping it
// would only create a way for the chain to produce no answer at all. It is
// also always attempted, even with the deadline already blown: it costs
// microseconds, and refusing to run it guarantees the failure that running it
// might still avoid.
//
// The arithmetic here deliberately uses the real clock via ctx.Deadline() and
// time.Until, not an injected clock.Clock. docs/ENGINEERING.md section 2 bans
// time.Now() in business logic, and this looks like a violation but is not: a
// context's deadline IS wall-clock, so a clock.Fake driving the budget while a
// real context drives the cancellation gives a rung that either never times
// out or always does, depending only on which of the two the test advanced.
// Unit D's circuit breaker does take the injected clock, because a breaker
// cooldown is an interval it measures itself rather than a deadline someone
// else set. The difference is the point.
func rungCtx(parent context.Context, perRung, reserve time.Duration, isTerminal bool) (context.Context, context.CancelFunc, bool) {
	noop := func() {}

	deadline, hasDeadline := parent.Deadline()
	if isTerminal {
		// Nothing to budget: it is exempt from the cap and always runs.
		return parent, noop, true
	}
	if !hasDeadline {
		// No inbound deadline to divide up (a caller that did not set one, or
		// a unit test). The per-rung cap still applies, so a hanging provider
		// cannot hang the request forever.
		ctx, cancel := context.WithTimeout(parent, perRung)
		return ctx, cancel, true
	}

	budget := time.Until(deadline) - reserve
	if budget <= 0 {
		return parent, noop, false
	}
	if budget > perRung {
		budget = perRung
	}
	ctx, cancel := context.WithTimeout(parent, budget)
	return ctx, cancel, true
}
