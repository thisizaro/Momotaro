// Package server implements WorldSimulatorService. docs/ARCHITECTURE.md
// section 6 is the design; this package is orchestration only (store.go
// holds the SQL, queue.go holds the Redis sorted-set logic, outcome.go and
// bucket.go hold the pure roll logic, poller.go holds the background
// delayed-outcome delivery loop), per docs/ENGINEERING.md section 14.
package server

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/thisizaro/Momotaro/internal/platform/clock"
	"github.com/thisizaro/Momotaro/internal/platform/kafkax"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	worldsimv1 "github.com/thisizaro/Momotaro/proto/gen/worldsim/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Server implements worldsimv1.WorldSimulatorServiceServer.
type Server struct {
	worldsimv1.UnimplementedWorldSimulatorServiceServer

	store *store
	queue *queue
	rng   randSource
	// seed is 0 until SeedBatch (POST /v1/demo/batches) resolves one; see
	// randFor below. atomic because SeedBatch writes it from one gRPC call
	// while SimulateOutcome reads it from many others, concurrently.
	seed  atomic.Int64
	clock clock.Clock
	// scale applies config.Common.Scale: every wall-clock delay in this
	// service goes through it, so DEMO_TIME_SCALE compresses a nudge's
	// hours-long response delay the same way it compresses everything
	// else (docs/ARCHITECTURE.md section 17).
	scale func(time.Duration) time.Duration

	// producer/rawEventsTopic back Phase 5.5 Unit W's demo control RPCs
	// (SeedBatch, InjectPoison), which publish onto raw.events exactly the
	// way scripts/batchgen and Ingestion do. Both may be nil/empty in a
	// test that only exercises SimulateOutcome; SeedBatch and InjectPoison
	// are the only callers that touch them.
	producer       *kafkax.Producer
	rawEventsTopic string
}

// New returns a Server. redisClient backs the delayed-outcome queue; scale
// is normally config.Common.Scale. producer/rawEventsTopic back SeedBatch
// and InjectPoison (Phase 5.5 Unit W); pass nil/"" when only SimulateOutcome
// is needed, e.g. in a test.
func New(pool *pgxpkg.Pool, redisClient *redis.Client, clk clock.Clock, scale func(time.Duration) time.Duration, producer *kafkax.Producer, rawEventsTopic string) *Server {
	return &Server{
		store:          newStore(pool),
		queue:          newQueue(redisClient),
		rng:            realRand{},
		clock:          clk,
		scale:          scale,
		producer:       producer,
		rawEventsTopic: rawEventsTopic,
	}
}

// randFor returns the draw source for one record's one outcome roll.
// Before any batch has been seeded through SeedBatch, s.seed is still its
// zero value and every call falls through to s.rng, realRand{} in
// production: exactly the unseeded behaviour this had before Unit AD, so
// scripts/batchgen and any record written straight into Postgres are
// unaffected. Once SeedBatch has resolved a seed
// (docs/DEMO_READINESS.md Unit AD; POST /v1/demo/batches's seed, echoed
// back on the response even when auto-picked), every subsequent roll for
// any record derives from it instead, so the whole run, batch generation
// and outcome rolls together, reproduces from that one seed. See rand.go's
// seededRand for why the derivation is per-record rather than a shared
// sequential stream.
//
// One known gap: seeding a second batch overwrites s.seed, so rolls for a
// still-in-flight earlier batch would then derive from the new seed
// instead of the one they started under. Acceptable for a DEMO ONLY
// component whose intended usage is one seeded batch played out at a time
// (docs/DECISIONS.md 2026-09-02); a per-batch seed stored alongside
// GROUND_TRUTH would close it if that ever becomes a real workflow.
func (s *Server) randFor(recordID string, attemptNumber int32) randSource {
	if seed := s.seed.Load(); seed != 0 {
		return seededRand{seed: seed, recordID: recordID, attemptNumber: attemptNumber}
	}
	return s.rng
}

// SimulateOutcome rolls the record's hidden recoverability profile against
// action. A retry (or any other synchronous action) answers immediately.
// A nudge with a nonzero response delay answers PENDING and schedules the
// real, already-rolled answer onto the delayed-outcome queue
// (docs/ARCHITECTURE.md section 6 steps 1-2); a nudge whose profile has a
// zero delay resolves immediately instead of scheduling a zero-delay entry
// pointlessly, which is the "usually PENDING" the proto comment allows for.
func (s *Server) SimulateOutcome(ctx context.Context, req *worldsimv1.SimulateOutcomeRequest) (*worldsimv1.SimulateOutcomeResponse, error) {
	recordID := req.GetRecordId()
	if recordID == "" {
		return nil, status.Error(codes.InvalidArgument, "record_id is required")
	}
	action := req.GetActionType()

	rp, err := s.store.loadRecordProfile(ctx, recordID)
	if err != nil {
		if errors.Is(err, errNoGroundTruth) {
			return nil, status.Errorf(codes.NotFound, "%v", err)
		}
		return nil, err
	}

	success := rollOutcome(s.randFor(recordID, req.GetAttemptNumber()), action, rp.Profile)
	scaledDelay := s.scale(time.Duration(rp.Profile.ResponseDelaySeconds) * time.Second)

	if isNudge(action) && scaledDelay > 0 {
		resolvesAt := s.clock.Now().Add(scaledDelay)
		d := delayedOutcome{RecordID: recordID, AttemptNumber: req.GetAttemptNumber(), Outcome: commonv1.Outcome_OUTCOME_SUCCESS}
		if !success {
			d.Outcome = commonv1.Outcome_OUTCOME_FAILURE
			d.FailureCode = rp.FailureCode
		}
		if err := s.queue.schedule(ctx, d, resolvesAt); err != nil {
			return nil, err
		}
		return &worldsimv1.SimulateOutcomeResponse{
			Outcome:    commonv1.Outcome_OUTCOME_PENDING,
			Immediate:  false,
			ResolvesAt: timestamppb.New(resolvesAt),
		}, nil
	}

	if success {
		return &worldsimv1.SimulateOutcomeResponse{Outcome: commonv1.Outcome_OUTCOME_SUCCESS, Immediate: true}, nil
	}
	return &worldsimv1.SimulateOutcomeResponse{
		Outcome:     commonv1.Outcome_OUTCOME_FAILURE,
		Immediate:   true,
		FailureCode: rp.FailureCode,
	}, nil
}
