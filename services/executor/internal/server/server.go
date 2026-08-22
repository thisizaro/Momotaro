// Package server implements the ExecutorService gRPC handler.
//
// Idempotency here is durable, not best-effort (docs/ARCHITECTURE.md
// section 11): Execute inserts the intervention_attempt row BEFORE
// performing the side effect, against the UNIQUE(record_id, attempt_number)
// constraint. A redelivered request loses the insert race and is answered
// from the row that already exists, never by re-running the action.
package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/thisizaro/Momotaro/internal/platform/clock"
	"github.com/thisizaro/Momotaro/internal/platform/logger"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	executorv1 "github.com/thisizaro/Momotaro/proto/gen/executor/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// uniqueViolation is Postgres's SQLSTATE for a unique constraint violation.
const uniqueViolation = "23505"

// pollInterval bounds how long a redelivered request waits for a
// concurrently in-flight original attempt to finish recording its outcome.
// The stub outcome source is synchronous and fast, so this window is tiny
// in practice; it exists so a genuine race never sees a torn PENDING read.
const pollInterval = 5 * time.Millisecond

// OutcomeFunc performs the actual recovery action and reports what happened.
//
// This stands in for docs/ARCHITECTURE.md section 3b's RecoveryActionPort
// (demo/world-simulator in this repo, a real bank/notification provider in
// production). The walking skeleton (docs/PLAN.md) wires a stub that always
// succeeds; wiring the real port is later, non-skeleton work.
type OutcomeFunc func(ctx context.Context) (outcome commonv1.Outcome, costPaise int64, err error)

// StubOutcome always succeeds at zero cost. The walking skeleton's
// placeholder for RecoveryActionPort.
func StubOutcome(ctx context.Context) (commonv1.Outcome, int64, error) {
	return commonv1.Outcome_OUTCOME_SUCCESS, 0, nil
}

// Server implements executorv1.ExecutorServiceServer.
type Server struct {
	executorv1.UnimplementedExecutorServiceServer

	pool    *pgxpkg.Pool
	clock   clock.Clock
	outcome OutcomeFunc
}

// New returns a Server. outcome is injected so tests can assert exactly how
// many times the action ran (docs/ENGINEERING.md section 8: idempotency
// must be proven by a test that delivers the same action twice).
func New(pool *pgxpkg.Pool, clk clock.Clock, outcome OutcomeFunc) *Server {
	return &Server{pool: pool, clock: clk, outcome: outcome}
}

// Execute performs one action exactly once for a given (record_id,
// attempt_number), regardless of how many times it is called with that
// pair.
func (s *Server) Execute(ctx context.Context, req *executorv1.ExecuteRequest) (*executorv1.ExecuteResponse, error) {
	if req.GetRecordId() == "" {
		return nil, status.Error(codes.InvalidArgument, "record_id is required")
	}
	if req.GetAttemptNumber() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "attempt_number must be positive")
	}
	if req.GetActionType() == commonv1.ActionType_ACTION_TYPE_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "action_type is required")
	}

	log := logger.ForRecord(logger.From(ctx), req.GetRecordId(), req.GetBatchId())

	attemptID := uuid.NewString()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO intervention_attempt
			(id, record_id, attempt_number, action_type, outcome, executed_at, cost_paise, message_text, failure_code)
		VALUES ($1, $2, $3, $4, $5, $6, 0, $7, '')`,
		attemptID, req.GetRecordId(), req.GetAttemptNumber(), req.GetActionType().String(),
		commonv1.Outcome_OUTCOME_PENDING.String(), s.clock.Now(), req.GetMessage())

	if err != nil {
		if isUniqueViolation(err) {
			log.Info("attempt already claimed, replaying recorded outcome",
				logger.KeyAttempt, req.GetAttemptNumber(), logger.KeyAction, req.GetActionType().String())
			return s.awaitExistingAttempt(ctx, req.GetRecordId(), req.GetAttemptNumber())
		}
		return nil, fmt.Errorf("insert intervention_attempt: %w", err)
	}

	log.Info("attempt claimed, executing action",
		logger.KeyAttempt, req.GetAttemptNumber(), logger.KeyAction, req.GetActionType().String())

	outcome, costPaise, err := s.outcome(ctx)
	if err != nil {
		return nil, fmt.Errorf("execute action for record %s attempt %d: %w", req.GetRecordId(), req.GetAttemptNumber(), err)
	}

	if _, err := s.pool.Exec(ctx, `UPDATE intervention_attempt SET outcome=$1, cost_paise=$2 WHERE id=$3`,
		outcome.String(), costPaise, attemptID); err != nil {
		return nil, fmt.Errorf("record attempt outcome: %w", err)
	}

	log.Info("attempt executed",
		logger.KeyAttempt, req.GetAttemptNumber(), logger.KeyOutcome, outcome.String(), logger.KeyCostPaise, costPaise)

	return &executorv1.ExecuteResponse{
		Outcome:         outcome,
		CostPaise:       costPaise,
		AlreadyExecuted: false,
	}, nil
}

// awaitExistingAttempt answers a redelivered request from the row the
// original request already claimed. It polls briefly in case the original
// request is still between its insert and its outcome update.
func (s *Server) awaitExistingAttempt(ctx context.Context, recordID string, attemptNumber int32) (*executorv1.ExecuteResponse, error) {
	for {
		var outcomeStr string
		var costPaise int64
		err := s.pool.QueryRow(ctx,
			`SELECT outcome, cost_paise FROM intervention_attempt WHERE record_id=$1 AND attempt_number=$2`,
			recordID, attemptNumber).Scan(&outcomeStr, &costPaise)
		if err != nil {
			return nil, fmt.Errorf("read existing attempt: %w", err)
		}

		if outcomeStr != commonv1.Outcome_OUTCOME_PENDING.String() {
			return &executorv1.ExecuteResponse{
				Outcome:         commonv1.Outcome(commonv1.Outcome_value[outcomeStr]),
				CostPaise:       costPaise,
				AlreadyExecuted: true,
			}, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.clock.After(pollInterval):
		}
	}
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolation
}
