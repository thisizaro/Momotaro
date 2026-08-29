// The expected-value maths from docs/PRD.md section 2b:
//
//	EV(action) = P(recovery | action, bucket, attempt_no) * amount_at_risk
//	             - direct_cost(action) - indirect_cost(action)
//
// One job: turn a loaded Model plus a record's situation into a chosen action.
// Loading lives in config.go (docs/ENGINEERING.md section 14).
package economics

import commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"

// bpsPerUnit converts basis points to a probability. The prior table is in
// basis points so the checked-in numbers stay exact integers that a reader can
// argue with, rather than floats that invite rounding debates.
const bpsPerUnit = 10000.0

// Score is the economics of one candidate action, kept together because all
// three numbers are persisted per attempt: the trail has to be able to answer
// "why did you think this was worth spending?" even after the priors have been
// recalibrated and would no longer produce the same answer
// (docs/ARCHITECTURE.md section 5a).
type Score struct {
	Action    commonv1.ActionType
	LiftBps   int
	PRecovery float64 // LiftBps as a probability, 0 to 1
	// EVPaise is deliberately a float, unlike every other money value in this
	// codebase (docs/ENGINEERING.md section 8, money is integer paise). An
	// expected value is not money anyone holds, it is a probability-weighted
	// estimate, and INTERVENTION_ATTEMPT stores it as DOUBLE PRECISION for the
	// same reason. CostPaise below is real money and stays an integer.
	EVPaise   float64
	CostPaise int64
}

// Candidate is one permitted action together with ITS OWN next attempt
// number. The two travel together because retries and customer contacts are
// counted separately: a record on its third retry may be on its first nudge,
// and the prior table asks about each action's own depth.
type Candidate struct {
	Action    commonv1.ActionType
	AttemptNo int
}

// Score computes the expected value of one action for one record.
//
// attemptNo is that action's own attempt number, not the record's total
// attempt count: the prior table asks how well a second retry does, which is
// a different question from how well anything does after two interventions.
func (m *Model) Score(action commonv1.ActionType, bucket commonv1.RootCauseBucket, attemptNo int, amountPaise int64) Score {
	lift := m.liftBps(action, bucket, attemptNo)
	cost := m.costOf(action)
	total := cost.DirectPaise + cost.IndirectPaise
	p := float64(lift) / bpsPerUnit

	return Score{
		Action:    action,
		LiftBps:   lift,
		PRecovery: p,
		EVPaise:   p*float64(amountPaise) - float64(total),
		CostPaise: total,
	}
}

// ScoreAll scores every permitted candidate and returns the full ranking, in
// the same order as permitted, no filtering. This is what makes "why not the
// alternatives" answerable from the audit trail (docs/PHASE5_IMPLEMENTATION.md
// Unit M): Best alone discards every candidate the instant it is beaten,
// including a sub-zero-EV one, which is exactly the data a guardrail-blocked
// or economically-losing alternative needs to be shown at all.
//
// Best is built on top of this (not a second, separately-maintained loop),
// specifically so the two can never independently drift on which candidate
// actually wins.
func (m *Model) ScoreAll(permitted []Candidate, bucket commonv1.RootCauseBucket, amountPaise int64) []Score {
	scores := make([]Score, len(permitted))
	for i, candidate := range permitted {
		scores[i] = m.Score(candidate.Action, bucket, candidate.AttemptNo, amountPaise)
	}
	return scores
}

// Best picks the highest expected value among the permitted actions, and
// reports ok=false when none of them is worth doing.
//
// permitted comes from the guardrails, which run first and may only ever
// remove options (docs/ARCHITECTURE.md section 5a). Scoring only what they
// allow is what stops a hard cap being spent around by an action that happens
// to look profitable.
//
// A false result is the ClosedUneconomic case: we deliberately decided this
// record is not worth chasing, which is a different outcome from escalation
// and is reported separately (docs/PRD.md section 9). The comparison is
// strictly greater than zero, so an action that exactly breaks even is not
// worth the operational risk of doing.
//
// Ties resolve to whichever action appears first in permitted, so the caller
// controls tie-breaking by passing a stable order.
func (m *Model) Best(permitted []Candidate, bucket commonv1.RootCauseBucket, amountPaise int64) (Score, bool) {
	return BestOf(m.ScoreAll(permitted, bucket, amountPaise))
}

// BestOf picks the highest-EV score out of an already-computed slice (see
// Best's own doc comment for the selection rule and tie-breaking). Exposed
// separately so a caller that already has the full ranking from ScoreAll
// (docs/PHASE5_IMPLEMENTATION.md Unit M: persisting it for the audit trail)
// can find the winner without scoring every candidate a second time, while
// guaranteeing byte-for-byte the same winner Best itself would report, since
// Best is defined in terms of this function, not a separately maintained copy.
func BestOf(scores []Score) (Score, bool) {
	var best Score
	found := false

	for _, score := range scores {
		if score.EVPaise <= 0 {
			continue
		}
		if !found || score.EVPaise > best.EVPaise {
			best, found = score, true
		}
	}
	return best, found
}

// liftBps is P(recovery) for one (action, bucket, attempt) in basis points.
//
// The table holds LIFT, the improvement an action buys over leaving the record
// alone, not an absolute recovery rate. That distinction matters: scoring
// against absolute rates would credit every action with the recoveries that
// would have happened anyway, which flatters the agent.
//
// An unmodelled combination falls to beyondListedAttemptsBps rather than being
// extrapolated, so running past the deepest modelled attempt closes the record
// instead of chasing it on a guess. That is a modelling stop and never a
// substitute for the guardrails, which refuse a capped action before the
// scorer is ever consulted.
func (m *Model) liftBps(action commonv1.ActionType, bucket commonv1.RootCauseBucket, attemptNo int) int {
	row, ok := m.priors[action][bucket]
	if !ok {
		return m.beyondListedAttemptsBps
	}
	if row.AllAttempts != nil {
		return *row.AllAttempts
	}

	var value *int
	switch attemptNo {
	case 1:
		value = row.Attempt1
	case 2:
		value = row.Attempt2
	case 3:
		value = row.Attempt3
	}
	if value == nil {
		return m.beyondListedAttemptsBps
	}
	return *value
}

// costOf returns what one action costs. An action absent from the cost model
// costs nothing, which would make it look free and therefore attractive, so
// Load rejects a config that fails to name every action rather than letting
// that happen at scoring time.
func (m *Model) costOf(action commonv1.ActionType) actionCost {
	return m.costs[action]
}
