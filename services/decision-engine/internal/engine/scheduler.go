package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/clock"
	"github.com/thisizaro/Momotaro/internal/platform/kafkax"
	"github.com/thisizaro/Momotaro/internal/platform/logger"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	executorv1 "github.com/thisizaro/Momotaro/proto/gen/executor/v1"
)

// maxExecuteAttempts/executeRetryDelay mirror HandleMessage's classify
// retry, applied to a claimed record's Execute call: a blip is retried, a
// permanently unreachable Executor is dead-lettered rather than wedging
// the scheduler on one record forever.
const (
	maxExecuteAttempts = 3
	executeRetryDelay  = 200 * time.Millisecond

	// claimBatchSize bounds how many due records one tick claims. Not
	// throughput-tuned (docs/PLAN.md Phase 6 does that); just small enough
	// that one tick cannot monopolise the row locks for long.
	claimBatchSize = 20
)

// SchedulerConfig groups the scheduler's tunables.
type SchedulerConfig struct {
	CallTimeout  time.Duration
	PollInterval time.Duration
	DLQTopic     string
}

// Scheduler is docs/ARCHITECTURE.md section 7a's scheduler worker. Without
// it, nothing scheduled for later (a retry, a nudge follow-up) ever
// actually fires: neither a Kafka message nor a gRPC call is what wakes a
// waiting record back up, this poll loop is.
type Scheduler struct {
	store   *store
	clients *clients
	dlq     *deadLetterPublisher
	clock   clock.Clock
	cfg     SchedulerConfig
}

// NewScheduler returns a Scheduler. It only needs the Executor client, not
// the Classifier: resuming a claimed record executes the action already
// decided at classify time, it never re-classifies.
func NewScheduler(pool *pgxpkg.Pool, executor executorv1.ExecutorServiceClient, dlqProducer *kafkax.Producer, clk clock.Clock, cfg SchedulerConfig) *Scheduler {
	return &Scheduler{
		store:   newStore(pool),
		clients: &clients{executor: executor, callTimeout: cfg.CallTimeout},
		dlq:     newDeadLetterPublisher(dlqProducer, cfg.DLQTopic),
		clock:   clk,
		cfg:     cfg,
	}
}

// Run polls every PollInterval, via the injected clock rather than
// time.Ticker so this is testable without a real wait
// (docs/ENGINEERING.md section 2), until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) error {
	for {
		if err := s.tick(ctx); err != nil {
			return err
		}
		select {
		case <-s.clock.After(s.cfg.PollInterval):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// tick claims one batch of due records and processes each in turn. A
// per-record processing failure is handled internally (bounded retry, then
// dead-letter) and never stops the tick; only a genuine infrastructure
// failure in the claim query itself is returned.
func (s *Scheduler) tick(ctx context.Context) error {
	claimed, err := s.store.claimDue(ctx, s.clock.Now(), claimBatchSize)
	if err != nil {
		return fmt.Errorf("claim due records: %w", err)
	}
	for _, c := range claimed {
		s.process(ctx, c)
	}
	return nil
}

func (s *Scheduler) process(ctx context.Context, c claimedRecord) {
	log := logger.ForRecord(logger.From(ctx), c.RecordID, c.BatchID)
	attemptNumber := int32(c.AttemptCount + 1)

	resp, err := s.executeWithRetry(ctx, c, attemptNumber)
	if err != nil {
		log.Error("execute failed after retries, dead-lettering", logger.KeyError, err.Error())
		dl := DeadLetter{
			RecordID:      c.RecordID,
			BatchID:       c.BatchID,
			FailureReason: fmt.Sprintf("execute failed after %d attempts: %v", maxExecuteAttempts, err),
			FailedAt:      s.clock.Now(),
		}
		if pubErr := s.dlq.Publish(ctx, dl); pubErr != nil {
			log.Error("failed to publish dead letter", logger.KeyError, pubErr.Error())
		}
		return
	}

	toState, reason := decideAfterExecute(c.PendingAction, resp.GetOutcome())
	if err := s.store.recordOutcome(ctx, c, toState, reason, int(attemptNumber), resp.GetCostPaise(), s.clock.Now()); err != nil {
		log.Error("failed to record outcome", logger.KeyError, err.Error())
		return
	}

	log.Info("scheduled action executed",
		logger.KeyAction, c.PendingAction.String(), logger.KeyOutcome, resp.GetOutcome().String(), logger.KeyState, toState.String())
}

func (s *Scheduler) executeWithRetry(ctx context.Context, c claimedRecord, attemptNumber int32) (*executorv1.ExecuteResponse, error) {
	var lastErr error
	for attempt := 1; attempt <= maxExecuteAttempts; attempt++ {
		resp, err := s.clients.execute(ctx, c.RecordID, c.BatchID, c.PendingAction, attemptNumber, c.AmountPaise)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if attempt < maxExecuteAttempts {
			select {
			case <-s.clock.After(executeRetryDelay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	return nil, lastErr
}
