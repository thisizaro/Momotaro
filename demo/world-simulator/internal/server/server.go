// Package server implements WorldSimulatorService. docs/ARCHITECTURE.md
// section 6 is the design; this package is orchestration only (store.go
// holds the SQL, queue.go holds the Redis sorted-set logic, outcome.go and
// bucket.go hold the pure roll logic, poller.go holds the background
// delayed-outcome delivery loop), per docs/ENGINEERING.md section 14.
package server

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/thisizaro/Momotaro/internal/platform/clock"
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
	clock clock.Clock
	// scale applies config.Common.Scale: every wall-clock delay in this
	// service goes through it, so DEMO_TIME_SCALE compresses a nudge's
	// hours-long response delay the same way it compresses everything
	// else (docs/ARCHITECTURE.md section 17).
	scale func(time.Duration) time.Duration
}

// New returns a Server. redisClient backs the delayed-outcome queue;
// scale is normally config.Common.Scale.
func New(pool *pgxpkg.Pool, redisClient *redis.Client, clk clock.Clock, scale func(time.Duration) time.Duration) *Server {
	return &Server{
		store: newStore(pool),
		queue: newQueue(redisClient),
		rng:   realRand{},
		clock: clk,
		scale: scale,
	}
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

	success := rollOutcome(s.rng, action, rp.Profile)
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
