package engine

import (
	"context"
	"time"

	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	executorv1 "github.com/thisizaro/Momotaro/proto/gen/executor/v1"
)

// clients wraps the two gRPC dependencies this service calls out to,
// bounding every call with an explicit deadline (docs/ENGINEERING.md
// section 3). Kept separate from engine.go and scheduler.go so neither has
// to repeat the context-deadline boilerplate per call site.
type clients struct {
	classifier  classifierv1.ClassifierServiceClient
	executor    executorv1.ExecutorServiceClient
	callTimeout time.Duration
}

func (c *clients) classify(ctx context.Context, record *commonv1.Record) (*classifierv1.ClassifyResponse, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.callTimeout)
	defer cancel()
	return c.classifier.Classify(callCtx, &classifierv1.ClassifyRequest{Record: record})
}

// evScoreAtDecision and pRecoveryAtDecision are the economics scorer's
// decision snapshot from when this action was scheduled (or re-scored),
// forwarded so the Executor can persist what was actually decided rather
// than nothing at all (docs/PHASE2_IMPLEMENTATION.md Unit G). The Decision
// Engine is the only service that scores; the Executor never recomputes
// these.
func (c *clients) execute(ctx context.Context, recordID, batchID string, action commonv1.ActionType, attemptNumber int32, amountPaise int64, evScoreAtDecision, pRecoveryAtDecision float64) (*executorv1.ExecuteResponse, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.callTimeout)
	defer cancel()
	return c.executor.Execute(callCtx, &executorv1.ExecuteRequest{
		RecordId:            recordID,
		BatchId:             batchID,
		ActionType:          action,
		AttemptNumber:       attemptNumber,
		AmountPaise:         amountPaise,
		EvScoreAtDecision:   evScoreAtDecision,
		PRecoveryAtDecision: pRecoveryAtDecision,
	})
}
