// Package engine is the Decision Engine's consuming loop.
//
// Walking skeleton (docs/PLAN.md): consume raw.events ONE AT A TIME, no
// keyed worker pool yet (docs/ARCHITECTURE.md section 8a lands with the
// depth work), call Classifier then Executor over gRPC, and write
// RECORD_STATE + AUDIT_ENTRY in one transaction (docs/ARCHITECTURE.md
// section 10a). The full state machine (Scoring, RetryScheduled, the
// scheduler worker) is Phase 1/2 depth; this collapses New straight to
// Recovered or Escalated based on the one hardcoded attempt.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/clock"
	"github.com/thisizaro/Momotaro/internal/platform/kafkax"
	"github.com/thisizaro/Momotaro/internal/platform/logger"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	executorv1 "github.com/thisizaro/Momotaro/proto/gen/executor/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// RawEvent mirrors services/ingestion/internal/server.RawEvent, the
// raw.events wire payload. Kept structurally in sync by hand; see that
// type's doc comment for why there is no shared package for it.
type RawEvent struct {
	RecordID      string    `json:"record_id"`
	BatchID       string    `json:"batch_id"`
	Type          string    `json:"type"`
	AmountPaise   int64     `json:"amount_paise"`
	Currency      string    `json:"currency"`
	FailureCode   string    `json:"failure_code"`
	InstrumentRef string    `json:"instrument_ref"`
	CreatedAt     time.Time `json:"created_at"`
}

// Engine consumes raw.events and drives one record through classification
// and execution to a terminal state.
type Engine struct {
	pool        *pgxpkg.Pool
	classifier  classifierv1.ClassifierServiceClient
	executor    executorv1.ExecutorServiceClient
	clock       clock.Clock
	callTimeout time.Duration
}

// New returns an Engine. callTimeout bounds every outbound gRPC call
// (docs/ENGINEERING.md section 3: no unbounded outbound call).
func New(pool *pgxpkg.Pool, classifier classifierv1.ClassifierServiceClient, executor executorv1.ExecutorServiceClient, clk clock.Clock, callTimeout time.Duration) *Engine {
	return &Engine{pool: pool, classifier: classifier, executor: executor, clock: clk, callTimeout: callTimeout}
}

// HandleMessage processes one raw.events record: classify, execute, then
// record the resulting state and its audit entry in one transaction. This
// is the handler passed to kafkax.Consumer.Consume.
func (e *Engine) HandleMessage(ctx context.Context, msg kafkax.Message) error {
	var evt RawEvent
	if err := json.Unmarshal(msg.Value, &evt); err != nil {
		return fmt.Errorf("unmarshal raw event (key=%s): %w", msg.Key, err)
	}

	log := logger.ForRecord(logger.From(ctx), evt.RecordID, evt.BatchID)

	terminal, err := e.alreadyTerminal(ctx, evt.RecordID)
	if err != nil {
		return err
	}
	if terminal {
		log.Info("record already in a terminal state, skipping redelivery")
		return nil
	}

	record := &commonv1.Record{
		Id:            evt.RecordID,
		BatchId:       evt.BatchID,
		Type:          commonv1.RecordType(commonv1.RecordType_value[evt.Type]),
		AmountPaise:   evt.AmountPaise,
		Currency:      evt.Currency,
		FailureCode:   evt.FailureCode,
		InstrumentRef: evt.InstrumentRef,
		CreatedAt:     timestamppb.New(evt.CreatedAt),
	}

	classifyResp, err := e.classify(ctx, record)
	if err != nil {
		return fmt.Errorf("classify record %s: %w", evt.RecordID, err)
	}
	log.Info("classified",
		logger.KeyBucket, classifyResp.GetBucket().String(),
		logger.KeyAction, classifyResp.GetRecommendedAction().String(),
		logger.KeySource, classifyResp.GetSource().String())

	executeResp, err := e.execute(ctx, evt, classifyResp.GetRecommendedAction())
	if err != nil {
		return fmt.Errorf("execute record %s: %w", evt.RecordID, err)
	}

	resultState := commonv1.RecordState_RECORD_STATE_ESCALATED
	reason := "execution did not succeed, escalating for human review"
	if executeResp.GetOutcome() == commonv1.Outcome_OUTCOME_SUCCESS {
		resultState = commonv1.RecordState_RECORD_STATE_RECOVERED
		reason = "classified and executed successfully"
	}

	now := e.clock.Now()
	err = pgxpkg.WithTx(ctx, e.pool, func(ctx context.Context, tx pgxpkg.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO record_state (record_id, current_state, attempt_count, root_cause_bucket, last_action_at, updated_at)
			VALUES ($1, $2, 1, $3, $4, $4)`,
			evt.RecordID, resultState.String(), classifyResp.GetBucket().String(), now); err != nil {
			return fmt.Errorf("insert record_state: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO audit_entry
				(record_id, batch_id, ts, from_state, to_state, reason, rationale, source, actor, attempt_number, cost_paise)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'system', $9, $10)`,
			evt.RecordID, evt.BatchID, now,
			commonv1.RecordState_RECORD_STATE_NEW.String(), resultState.String(),
			reason, classifyResp.GetRationale(), classifyResp.GetSource().String(),
			int32(1), executeResp.GetCostPaise()); err != nil {
			return fmt.Errorf("insert audit_entry: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("record outcome for %s: %w", evt.RecordID, err)
	}

	log.Info("record processed", logger.KeyState, resultState.String(), logger.KeyOutcome, executeResp.GetOutcome().String())
	return nil
}

func (e *Engine) classify(ctx context.Context, record *commonv1.Record) (*classifierv1.ClassifyResponse, error) {
	callCtx, cancel := context.WithTimeout(ctx, e.callTimeout)
	defer cancel()
	return e.classifier.Classify(callCtx, &classifierv1.ClassifyRequest{Record: record})
}

func (e *Engine) execute(ctx context.Context, evt RawEvent, action commonv1.ActionType) (*executorv1.ExecuteResponse, error) {
	callCtx, cancel := context.WithTimeout(ctx, e.callTimeout)
	defer cancel()
	return e.executor.Execute(callCtx, &executorv1.ExecuteRequest{
		RecordId:      evt.RecordID,
		BatchId:       evt.BatchID,
		ActionType:    action,
		AttemptNumber: 1,
		AmountPaise:   evt.AmountPaise,
	})
}

func (e *Engine) alreadyTerminal(ctx context.Context, recordID string) (bool, error) {
	var stateStr string
	err := e.pool.QueryRow(ctx, `SELECT current_state FROM record_state WHERE record_id = $1`, recordID).Scan(&stateStr)
	if err != nil {
		if errors.Is(err, pgxpkg.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("query record_state for %s: %w", recordID, err)
	}
	switch commonv1.RecordState(commonv1.RecordState_value[stateStr]) {
	case commonv1.RecordState_RECORD_STATE_RECOVERED,
		commonv1.RecordState_RECORD_STATE_ESCALATED,
		commonv1.RecordState_RECORD_STATE_CLOSED_UNECONOMIC:
		return true, nil
	default:
		return false, nil
	}
}
