package server

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"github.com/google/uuid"
	"github.com/thisizaro/Momotaro/internal/platform/syntheticgen"
	worldsimv1 "github.com/thisizaro/Momotaro/proto/gen/worldsim/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// maxSeedBatchCount bounds POST /v1/demo/batches: this is a demo control,
// not a load-test entry point (that is scripts/loadgen), so a fat-fingered
// request must not be able to overwhelm a live demo stack's Postgres/Kafka.
// Comfortably above any batch size docs/PRD.md's demo script uses (50-100).
const maxSeedBatchCount = 1000

// rawEvent is the raw.events wire payload, mirroring
// services/ingestion/internal/server.RawEvent and
// services/decision-engine/internal/engine.RawEvent field for field. There
// is no proto for this topic (docs/ARCHITECTURE.md section 9's proto
// discipline applies to gRPC contracts, not this internal topic payload),
// so every producer, including this one, keeps its own copy in sync by
// hand. Shared by SeedBatch and InjectPoison below.
type rawEvent struct {
	RecordID      string    `json:"record_id"`
	BatchID       string    `json:"batch_id"`
	Type          string    `json:"type"`
	AmountPaise   int64     `json:"amount_paise"`
	Currency      string    `json:"currency"`
	FailureCode   string    `json:"failure_code"`
	InstrumentRef string    `json:"instrument_ref"`
	CreatedAt     time.Time `json:"created_at"`
}

// SeedBatch seeds a batch of synthetic records with hidden GROUND_TRUTH,
// exactly like scripts/batchgen, and publishes each one to raw.events so
// the real pipeline picks it up. Backs POST /v1/demo/batches
// (docs/PHASE5_5_IMPLEMENTATION.md Unit W). Lives here, not on the Gateway,
// because only a demo/ component may ever write GROUND_TRUTH
// (docs/ARCHITECTURE.md section 6); World Simulator already holds that
// permission for the read side (loadRecordProfile).
func (s *Server) SeedBatch(ctx context.Context, req *worldsimv1.SeedBatchRequest) (*worldsimv1.SeedBatchResponse, error) {
	preset, ok := scenarioByName(req.GetScenario())
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "unknown scenario %q", req.GetScenario())
	}
	count := req.GetCount()
	if count <= 0 || count > maxSeedBatchCount {
		return nil, status.Errorf(codes.InvalidArgument, "count must be between 1 and %d, got %d", maxSeedBatchCount, count)
	}
	if s.producer == nil || s.rawEventsTopic == "" {
		return nil, status.Error(codes.FailedPrecondition, "world simulator has no raw.events producer configured")
	}

	seed := req.GetSeed()
	if seed == 0 {
		seed = s.clock.Now().UnixNano()
	}
	rng := rand.New(rand.NewSource(seed))

	// Every SimulateOutcome roll for any record, not just this batch's,
	// derives from this same seed from here on, so the whole run
	// reproduces end to end from the one seed on this request
	// (docs/DEMO_READINESS.md Unit AD; see Server.randFor in server.go).
	s.seed.Store(seed)

	batchID := uuid.NewString()
	source := fmt.Sprintf("demo:%s", preset.Name)
	if err := s.store.insertBatch(ctx, batchID, source, count); err != nil {
		return nil, err
	}

	instrumentRefs := syntheticgen.InstrumentRefPool(int(count))

	for i := int32(0); i < count; i++ {
		rec := generateScenarioRecord(rng, preset)
		instrumentRef := syntheticgen.PickInstrumentRef(rng, rec.Type, instrumentRefs)
		recordID := uuid.NewString()
		createdAt := s.clock.Now()

		if err := s.store.insertSeedRecord(ctx, seedRecord{
			RecordID:               recordID,
			BatchID:                batchID,
			Type:                   rec.Type.String(),
			AmountPaise:            rec.AmountPaise,
			FailureCode:            rec.FailureCode,
			InstrumentRef:          instrumentRef,
			CreatedAt:              createdAt,
			TrueBucket:             rec.TrueBucket.String(),
			RecoveryProbability:    rec.RecoveryProbability,
			WrongActionProbability: rec.WrongActionProbability,
			ResponseDelaySeconds:   rec.ResponseDelaySeconds,
		}); err != nil {
			return nil, fmt.Errorf("seed record %d/%d: %w", i+1, count, err)
		}

		payload, err := json.Marshal(rawEvent{
			RecordID:      recordID,
			BatchID:       batchID,
			Type:          rec.Type.String(),
			AmountPaise:   rec.AmountPaise,
			Currency:      "INR",
			FailureCode:   rec.FailureCode,
			InstrumentRef: instrumentRef,
			CreatedAt:     createdAt,
		})
		if err != nil {
			return nil, fmt.Errorf("marshal raw event %d/%d: %w", i+1, count, err)
		}
		if err := s.producer.Publish(ctx, s.rawEventsTopic, recordID, payload); err != nil {
			return nil, fmt.Errorf("publish raw event %d/%d: %w", i+1, count, err)
		}
	}

	return &worldsimv1.SeedBatchResponse{BatchId: batchID, GeneratedCount: count, Seed: seed}, nil
}
