// Package server implements the ExecutorService gRPC handler.
//
// The handler orchestrates and nothing else (docs/ENGINEERING.md section 14):
// validation lives in validate.go, the durable idempotency guard and all SQL
// in internal/attempt, and every outside-world call behind the two ports in
// internal/ports. Execute below should read as a list of steps.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/thisizaro/Momotaro/internal/platform/clock"
	"github.com/thisizaro/Momotaro/internal/platform/logger"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	executorv1 "github.com/thisizaro/Momotaro/proto/gen/executor/v1"
	"github.com/thisizaro/Momotaro/services/executor/internal/attempt"
	"github.com/thisizaro/Momotaro/services/executor/internal/ports"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Server implements executorv1.ExecutorServiceServer.
type Server struct {
	executorv1.UnimplementedExecutorServiceServer

	attempts *attempt.Store
	router   *ports.Router
	clock    clock.Clock
}

// New returns a Server. router is injected rather than constructed here so a
// test can count exactly how many times an action really ran, which is what
// docs/ENGINEERING.md section 8 requires of anything touching money.
func New(attempts *attempt.Store, router *ports.Router, clk clock.Clock) *Server {
	return &Server{attempts: attempts, router: router, clock: clk}
}

// Execute performs one action exactly once for a given (record_id,
// attempt_number), however many times it is called with that pair.
func (s *Server) Execute(ctx context.Context, req *executorv1.ExecuteRequest) (*executorv1.ExecuteResponse, error) {
	if err := validateExecute(req); err != nil {
		return nil, err
	}

	log := logger.ForRecord(logger.From(ctx), req.GetRecordId(), req.GetBatchId())
	if isNudge(req.GetActionType()) && req.GetMessage() == "" {
		// Expected until Phase 5 composes nudge text: the Decision Engine
		// sends no message today. Logged rather than rejected, because the
		// gap is real but it is not this service's to fix.
		log.Warn("nudge has no composed message text", logger.KeyAction, req.GetActionType().String())
	}

	// Claim the slot BEFORE the side effect. This, not the Redis fast path
	// deferred to Phase 6, is the actual guarantee (docs/ARCHITECTURE.md
	// section 11).
	attemptID, claimed, err := s.attempts.Claim(ctx, req.GetRecordId(), req.GetAttemptNumber(),
		req.GetActionType(), req.GetMessage(), req.GetEvScoreAtDecision(), req.GetPRecoveryAtDecision(), s.clock.Now())
	if err != nil {
		if errors.Is(err, attempt.ErrUnknownRecord) {
			return nil, status.Errorf(codes.NotFound, "record %s does not exist", req.GetRecordId())
		}
		return nil, err
	}
	if !claimed {
		return s.replay(ctx, req, log)
	}

	log.Info("attempt claimed, executing action",
		logger.KeyAttempt, req.GetAttemptNumber(), logger.KeyAction, req.GetActionType().String())

	result, err := s.router.Execute(ctx, req.GetRecordId(), req.GetActionType(), req.GetAttemptNumber(), req.GetMessage())
	if err != nil {
		// The slot stays claimed. Releasing it would let a retry re-run an
		// action that may well have reached the outside world already, which
		// for a payment retry means a double charge.
		return nil, fmt.Errorf("execute %s for record %s attempt %d: %w",
			req.GetActionType(), req.GetRecordId(), req.GetAttemptNumber(), err)
	}

	if err := s.attempts.RecordOutcome(ctx, attemptID, result.Outcome, result.CostPaise, result.FailureCode); err != nil {
		return nil, err
	}

	log.Info("attempt executed",
		logger.KeyAttempt, req.GetAttemptNumber(),
		logger.KeyOutcome, result.Outcome.String(),
		logger.KeyCostPaise, result.CostPaise)

	return &executorv1.ExecuteResponse{
		Outcome:         result.Outcome,
		CostPaise:       result.CostPaise,
		FailureCode:     result.FailureCode,
		ResolvesAt:      resolvesAtOrNil(result),
		AlreadyExecuted: false,
	}, nil
}

// replay answers a duplicate request from the attempt the original already
// recorded, without performing any side effect.
func (s *Server) replay(ctx context.Context, req *executorv1.ExecuteRequest, log *slog.Logger) (*executorv1.ExecuteResponse, error) {
	log.Info("attempt already claimed, replaying the recorded outcome",
		logger.KeyAttempt, req.GetAttemptNumber(), logger.KeyAction, req.GetActionType().String())

	rec, err := s.attempts.Await(ctx, s.clock, req.GetRecordId(), req.GetAttemptNumber())
	if err != nil {
		if errors.Is(err, attempt.ErrAbandonedClaim) {
			// Claimed but never resolved, so the process holding it died
			// mid-attempt. Aborted rather than guessed: re-running could
			// double-charge, and inventing an outcome would put a fiction in
			// the audit trail. Deliberately retryable at the gRPC level.
			log.Error("attempt slot claimed but never resolved", logger.KeyError, err.Error())
			return nil, status.Errorf(codes.Aborted,
				"record %s attempt %d was claimed but never completed; it needs manual resolution",
				req.GetRecordId(), req.GetAttemptNumber())
		}
		return nil, err
	}

	return &executorv1.ExecuteResponse{
		Outcome:         rec.Outcome,
		CostPaise:       rec.CostPaise,
		FailureCode:     rec.FailureCode,
		AlreadyExecuted: true,
	}, nil
}

// resolvesAtOrNil sets resolves_at only for a genuinely pending outcome, so
// the field is never a meaningless timestamp on a settled attempt.
func resolvesAtOrNil(result ports.Result) *timestamppb.Timestamp {
	if result.Outcome != commonv1.Outcome_OUTCOME_PENDING || result.ResolvesAt.IsZero() {
		return nil
	}
	return timestamppb.New(result.ResolvesAt)
}

func isNudge(action commonv1.ActionType) bool {
	return action == commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER ||
		action == commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE
}
