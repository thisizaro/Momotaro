// scenarios.go: the demo scenario presets Phase 5.5 Unit W adds (backs
// GET /v1/demo/scenarios and POST /v1/demo/batches's scenario field). Each
// preset is a distribution over internal/platform/syntheticgen's own
// generator, not a second copy of it: generateScenarioRecord always calls
// syntheticgen.GenerateRecord first and only forces the fields a scenario
// cares about, reusing syntheticgen.ProfileForBucket for the recovery
// numbers so the forced records use the exact same hidden-profile model as
// everything else.
//
// Every forced failure_code below is one of Razorpay's real, published
// codes, matching services/classifier/internal/rules/buckets.go's own
// table (verified by hand: this package cannot import that one, it is
// compiler-private to the classifier service, docs/ARCHITECTURE.md section
// 2a). Inventing a code here is exactly the bug this unit exists to not
// repeat (docs/PHASE5_5_IMPLEMENTATION.md Unit W).
package server

import (
	"context"
	"math/rand"

	"github.com/thisizaro/Momotaro/internal/platform/syntheticgen"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	worldsimv1 "github.com/thisizaro/Momotaro/proto/gen/worldsim/v1"
)

// scenarioPreset is one named distribution over the generator. A zero-value
// ForceBucket (ROOT_CAUSE_BUCKET_UNSPECIFIED) means "do not force anything",
// which is what makes "normal" a pure pass-through onto syntheticgen's own
// default mix.
type scenarioPreset struct {
	Name        string
	Description string

	ForceBucket   commonv1.RootCauseBucket
	ForceType     commonv1.RecordType
	Codes         []string
	Concentration float64
}

// scenarioPresets is the full catalogue, in the order GET /v1/demo/scenarios
// returns them. Descriptions are the plain-English "what this makes
// visible" story from docs/PHASE5_5_IMPLEMENTATION.md Unit W, not just a
// name, since a judge choosing one on the dashboard needs to know what it
// demonstrates before running it.
var scenarioPresets = []scenarioPreset{
	{
		Name:        "normal",
		Description: "The current default mix: a realistic spread across every root-cause bucket, no concentration.",
	},
	{
		Name: "bank-outage",
		Description: "Concentrated on one bank being unavailable (BANK_NOT_AVAILABLE), all seeded in the same short window, " +
			"so per-bucket reporting shows a systemic spike instead of 80 unrelated customer problems.",
		ForceBucket:   commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
		ForceType:     commonv1.RecordType_RECORD_TYPE_PAYMENT,
		Codes:         []string{"BANK_NOT_AVAILABLE"},
		Concentration: 0.85,
	},
	{
		Name: "salary-day",
		Description: "Heavy INSUFFICIENT_FUNDS, so the salary-window retry timing (wait for the 1st to 7th, not tomorrow) " +
			"becomes the visible story.",
		ForceBucket:   commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS,
		ForceType:     commonv1.RecordType_RECORD_TYPE_PAYMENT,
		Codes:         []string{"INSUFFICIENT_FUNDS"},
		Concentration: 0.85,
	},
	{
		Name: "dead-cards",
		Description: "Heavy CARD_EXPIRED and DEBIT_INSTRUMENT_BLOCKED, so the nudge-versus-retry distinction and the " +
			"uneconomic close are visible: a retry cannot fix a dead instrument, only a method update can.",
		ForceBucket:   commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE,
		ForceType:     commonv1.RecordType_RECORD_TYPE_PAYMENT,
		Codes:         []string{"CARD_EXPIRED", "DEBIT_INSTRUMENT_BLOCKED"},
		Concentration: 0.85,
	},
}

// scenarioByName looks up a preset by name, case-sensitive (the wire
// vocabulary is fixed and lower-case throughout, docs/API_GATEWAY.md wire
// convention). Empty defaults to "normal", the same default
// SeedBatchRequest.scenario documents.
func scenarioByName(name string) (scenarioPreset, bool) {
	if name == "" {
		name = "normal"
	}
	for _, p := range scenarioPresets {
		if p.Name == name {
			return p, true
		}
	}
	return scenarioPreset{}, false
}

// listScenarios returns every preset, in catalogue order.
func listScenarios() []scenarioPreset {
	return scenarioPresets
}

// ListScenarios backs GET /v1/demo/scenarios. Pure: no store, no queue, no
// producer, so it needs none of Server's collaborators and works even
// against a zero-value *Server.
func (s *Server) ListScenarios(ctx context.Context, req *worldsimv1.ListScenariosRequest) (*worldsimv1.ListScenariosResponse, error) {
	presets := listScenarios()
	out := make([]*worldsimv1.ScenarioPreset, len(presets))
	for i, p := range presets {
		out[i] = &worldsimv1.ScenarioPreset{Name: p.Name, Description: p.Description}
	}
	return &worldsimv1.ListScenariosResponse{Scenarios: out}, nil
}

// generateScenarioRecord produces one record under preset. It always starts
// from syntheticgen.GenerateRecord so the amount distribution, instrument
// sharing and general record shape are identical to a "normal" batch; a
// non-normal preset then overrides type/failure_code/bucket (and the
// profile numbers that follow from the bucket) for Concentration of the
// records, leaving the rest as an unforced, realistic draw.
func generateScenarioRecord(rng *rand.Rand, preset scenarioPreset) syntheticgen.GeneratedRecord {
	rec := syntheticgen.GenerateRecord(rng)
	if preset.ForceBucket == commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_UNSPECIFIED {
		return rec
	}
	if rng.Float64() >= preset.Concentration {
		return rec
	}

	profile := syntheticgen.ProfileForBucket(preset.ForceBucket)
	rec.Type = preset.ForceType
	rec.FailureCode = preset.Codes[rng.Intn(len(preset.Codes))]
	rec.TrueBucket = preset.ForceBucket
	rec.RecoveryProbability = profile.RecoveryProbability
	rec.WrongActionProbability = profile.WrongActionProbability
	rec.ResponseDelaySeconds = pickInRange(rng, profile.ResponseDelayRange)
	return rec
}

// pickInRange draws a value from [r[0], r[1]). Generic range arithmetic,
// not the generation model itself (the code pools and bucket weighting
// that Unit V extracted to internal/platform/syntheticgen precisely so it
// would not be duplicated); mirrors that package's own unexported helper
// of the same name and shape.
func pickInRange(rng *rand.Rand, r [2]int32) int32 {
	if r[1] <= r[0] {
		return r[0]
	}
	return r[0] + rng.Int31n(r[1]-r[0])
}
