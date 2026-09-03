package engine

import "sync"

// llmBudget enforces LLM_SAMPLE_RATE as a ceiling on live model calls, not
// a selector of which records get one (docs/DEMO_READINESS.md Unit AI,
// docs/ARCHITECTURE.md section 17). Routing itself is a separate decision,
// made in clients.go by comparing a record's rules-engine confidence
// against LLM_ROUTE_CONFIDENCE_THRESHOLD; this type only answers "can we
// still afford to act on that decision".
//
// Why a running ratio rather than an absolute count. The Decision Engine
// consumes raw.events as a stream: records arrive one at a time, and
// nothing here ever sees the whole batch before deciding, so there is no
// list to rank by ambiguity and take the top N% of. A running ratio needs
// no such lookahead: every classify() call reports itself once, whether or
// not it turned out ambiguous, and a live call is only granted when
// spending it would keep llmCalls/total at or under rate. That is the
// literal meaning of "ceiling, not selector": the fraction of ALL records
// that ever reach a live provider is bounded by rate, however many of them
// the confidence threshold judges ambiguous.
//
// This is deliberately simpler than trying to rank the batch: it costs one
// mutex-guarded division per record, needs no buffering, and its only
// unfairness is ordering (an ambiguous record early in the stream is more
// likely to get a live call than an equally ambiguous one arriving after
// the ceiling is already spent). That is an honest tradeoff for a
// streaming pipeline, not a bug: the alternative (buffer the batch, rank
// it, then classify) would give up streaming altogether.
type llmBudget struct {
	mu       sync.Mutex
	rate     float64
	total    int64
	llmCalls int64
}

// newLLMBudget returns a budget enforcing rate, which must already be
// validated in [0,1] by the caller (cmd/main.go does this for
// LLM_SAMPLE_RATE at startup, the same way it always has).
func newLLMBudget(rate float64) *llmBudget {
	return &llmBudget{rate: rate}
}

// consider reports one more record classified, and answers whether a live
// model call is still within budget for it. eligible is the routing
// decision from clients.go (the rules engine was not confident enough);
// consider still counts the record toward the running total when eligible
// is false, because the ceiling bounds a fraction of every record this
// Engine processes, not only the ambiguous ones.
//
// total and llmCalls are updated together under one lock so the ratio
// check is atomic: two concurrent callers reading the same under-count and
// both spending would blow the ceiling, which a check-then-increment
// without a shared lock cannot prevent.
func (b *llmBudget) consider(eligible bool) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.total++
	if !eligible || b.rate <= 0 {
		return false
	}
	// Spend only if doing so keeps the ceiling intact: this is the
	// "would-be ratio after spending" check, not "the ratio right now",
	// which is what makes the budget greedy (it grants a call the instant
	// it can afford one) while never overspending.
	if float64(b.llmCalls+1)/float64(b.total) > b.rate {
		return false
	}
	b.llmCalls++
	return true
}
