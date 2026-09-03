package server

import (
	"testing"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// Traffic that arrived through POST /v1/webhooks/payment-failed has no
// GROUND_TRUTH row, because nothing seeded it. Before this, SimulateOutcome
// answered NotFound for such a record and the Executor could never settle
// it: 146 records sat in NUDGED and 55 in RETRYING forever on a live stack,
// and the dashboard read 0% recovery (docs/INCIDENTS.md 2026-09-03).
//
// World Simulator stands in for the world. For a record it did not seed it
// must still answer as the world would, while writing no GROUND_TRUTH row,
// so accuracy and the baseline comparison stay correctly absent.
func TestUnseededProfileIsDerivedFromTheFailureCode(t *testing.T) {
	got, ok := unseededProfile("BANK_NOT_AVAILABLE")
	if !ok {
		t.Fatal("BANK_NOT_AVAILABLE is a known code, want a derived profile")
	}
	if got.TrueBucket != commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK {
		t.Errorf("TrueBucket = %v, want TRANSIENT_BANK", got.TrueBucket)
	}
	if got.RecoveryProbability <= 0 || got.RecoveryProbability > 1 {
		t.Errorf("RecoveryProbability = %v, want a real probability", got.RecoveryProbability)
	}
}

// An unrecognised code must still produce a usable profile rather than
// stranding the record, which is the whole failure this fixes. Real webhook
// traffic can carry any code a gateway invents.
func TestUnseededProfileHandlesAnUnknownCode(t *testing.T) {
	got, ok := unseededProfile("SOME_CODE_WE_HAVE_NEVER_SEEN")
	if !ok {
		t.Fatal("an unknown code must still yield a profile, not strand the record")
	}
	if got.RecoveryProbability < 0 || got.RecoveryProbability > 1 {
		t.Errorf("RecoveryProbability = %v, want a real probability", got.RecoveryProbability)
	}
}

// The derivation must be pure: the same code always gives the same profile,
// so a record's second attempt is answered on the same terms as its first.
func TestUnseededProfileIsDeterministic(t *testing.T) {
	a, _ := unseededProfile("CARD_EXPIRED")
	b, _ := unseededProfile("CARD_EXPIRED")
	if a != b {
		t.Errorf("same code gave different profiles: %+v vs %+v", a, b)
	}
}
