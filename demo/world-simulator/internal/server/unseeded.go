package server

import (
	"github.com/thisizaro/Momotaro/internal/platform/syntheticgen"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// unseededProfile derives a plausible hidden profile for a record that has
// no GROUND_TRUTH row, from the failure code the caller actually sent.
//
// Why this exists. Records that arrive through POST
// /v1/webhooks/payment-failed, which is how scripts/loadgen and any real
// gateway integration deliver them, are never seeded, so they have no
// sealed profile. store.loadRecordProfile returns errNoGroundTruth for
// them, and SimulateOutcome used to turn that into a NotFound. The
// Executor could then never settle the attempt, so the record sat in
// NUDGED or RETRYING forever: 146 and 55 of them respectively on a live
// stack, with the dashboard reading 0% recovery
// (docs/INCIDENTS.md 2026-09-03).
//
// Why this does not undo the reason store.go says World Simulator "must
// not guess a probability". That rule protects the accuracy measurement,
// and it still holds: nothing here writes a GROUND_TRUTH row. Accuracy and
// the baseline comparison are computed from that table, so both stay
// correctly absent for webhook traffic, which is exactly the honest
// distinction Unit AJ set out to draw. What changes is only that World
// Simulator now plays the world for traffic it did not seed, instead of
// refusing to answer at all. In production a real bank answers here; in a
// demo there is no bank, so refusing to answer is not neutrality, it is a
// record that never finishes.
//
// The derivation is pure and code-driven rather than random, so a record's
// second attempt is answered on the same terms as its first.
func unseededProfile(failureCode string) (groundTruthProfile, bool) {
	bucket, known := syntheticgen.BucketForFailureCode(failureCode)
	if !known {
		// Real gateways invent codes we have never seen. Treat an
		// unrecognised one as needing the customer to act, the broadest
		// bucket and the one that assumes the least, rather than
		// stranding the record.
		bucket = commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED
	}

	p := syntheticgen.ProfileForBucket(bucket)
	return groundTruthProfile{
		TrueBucket:             bucket,
		RecoveryProbability:    p.RecoveryProbability,
		WrongActionProbability: p.WrongActionProbability,
		// Midpoint of the bucket's own range rather than a draw: this
		// function must stay pure so two attempts on one record agree.
		ResponseDelaySeconds: (p.ResponseDelayRange[0] + p.ResponseDelayRange[1]) / 2,
	}, true
}
