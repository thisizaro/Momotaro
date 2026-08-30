package server

import commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"

// groundTruthProfile is one record's hidden recoverability profile, read
// from GROUND_TRUTH (docs/ARCHITECTURE.md section 6). World Simulator and
// the Reporting Service's accuracy scorer are the only two things ever
// allowed to hold one of these.
type groundTruthProfile struct {
	TrueBucket             commonv1.RootCauseBucket
	RecoveryProbability    float64
	WrongActionProbability float64
	ResponseDelaySeconds   int32
}

// rollOutcome decides whether action recovers the record, a single
// memoryless Bernoulli trial against whichever probability applies: correct
// diagnosis and action gets RecoveryProbability ("given the CORRECT
// action", scripts/batchgen/profile.go), anything else gets
// WrongActionProbability. Each call re-rolls independently; nothing here
// remembers a prior attempt's result, matching the plain-English model in
// docs/ARCHITECTURE.md section 6 ("resolves on retry with 80%
// probability") rather than inventing a decay curve GROUND_TRUTH does not
// encode.
func rollOutcome(rng randSource, action commonv1.ActionType, profile groundTruthProfile) bool {
	p := profile.WrongActionProbability
	if isCorrectAction(action, profile.TrueBucket) {
		p = profile.RecoveryProbability
	}
	return rng.Float64() < p
}
