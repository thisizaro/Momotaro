package engine

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/clock"
	"github.com/thisizaro/Momotaro/internal/platform/kafkax"
	"github.com/thisizaro/Momotaro/internal/platform/logger"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	executorv1 "github.com/thisizaro/Momotaro/proto/gen/executor/v1"
	"github.com/thisizaro/Momotaro/services/decision-engine/internal/economics"
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

// SchedulerConfig groups the scheduler's tunables. RetryDelay, NudgeDelay
// and Guardrails mirror Engine's Config: a failed attempt that gets
// re-scored (docs/ARCHITECTURE.md section 7) needs the same guardrails and
// the same scheduling delays a fresh record's first classification uses, so
// the two paths stay in step (docs/PHASE2_IMPLEMENTATION.md Unit E).
type SchedulerConfig struct {
	CallTimeout  time.Duration
	PollInterval time.Duration
	DLQTopic     string
	RetryDelay   time.Duration
	NudgeDelay   time.Duration
	TimeScale    float64
	Guardrails   GuardrailConfig
}

// Scheduler is docs/ARCHITECTURE.md section 7a's scheduler worker. Without
// it, nothing scheduled for later (a retry, a nudge follow-up) ever
// actually fires: neither a Kafka message nor a gRPC call is what wakes a
// waiting record back up, this poll loop is.
type Scheduler struct {
	store     *store
	clients   *clients
	dlq       *deadLetterPublisher
	clock     clock.Clock
	economics *economics.Model
	cfg       SchedulerConfig
}

// NewScheduler returns a Scheduler. It only needs the Executor client, not
// the Classifier: resuming a claimed record executes the action already
// decided at classify time, it never re-classifies. It does need the
// economics model, because a failed attempt is re-priced in place
// (scoreAndRoute) rather than re-classified.
func NewScheduler(pool *pgxpkg.Pool, executor executorv1.ExecutorServiceClient, dlqProducer *kafkax.Producer, clk clock.Clock, model *economics.Model, cfg SchedulerConfig) *Scheduler {
	return &Scheduler{
		store:     newStore(pool),
		clients:   &clients{executor: executor, callTimeout: cfg.CallTimeout},
		dlq:       newDeadLetterPublisher(dlqProducer, cfg.DLQTopic),
		clock:     clk,
		economics: model,
		cfg:       cfg,
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

	outcome := resp.GetOutcome()
	if outcome == commonv1.Outcome_OUTCOME_FAILURE {
		s.handleFailedAttempt(ctx, log, c, int(attemptNumber), resp.GetCostPaise())
		return
	}

	toState, reason := decideAfterExecute(c.PendingAction, outcome)
	if err := s.store.recordOutcome(ctx, c, toState, reason, int(attemptNumber), resp.GetCostPaise(), s.clock.Now()); err != nil {
		log.Error("failed to record outcome", logger.KeyError, err.Error())
		return
	}

	log.Info("scheduled action executed",
		logger.KeyAction, c.PendingAction.String(), logger.KeyOutcome, outcome.String(), logger.KeyState, toState.String())
}

// handleFailedAttempt is the re-entry to Scoring docs/ARCHITECTURE.md
// section 7 requires after a failed attempt: the record is re-priced with
// this attempt spent, rather than escalated outright. It calls the same
// scoreAndRoute the New path uses (state.go), so the two paths cannot
// disagree about when a record has run out of road.
func (s *Scheduler) handleFailedAttempt(ctx context.Context, log *slog.Logger, c claimedRecord, attemptNumber int, costPaise int64) {
	history, err := s.store.loadAttemptHistory(ctx, c.RecordID)
	if err != nil {
		log.Error("failed to load attempt history for re-scoring", logger.KeyError, err.Error())
		return
	}

	now := s.clock.Now()
	state, pendingAction, reason, score, trace := scoreAndRoute(s.economics, s.cfg.Guardrails, c.RootCauseBucket, history, c.AmountPaise, now)
	var dueAt *time.Time
	if state == commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED {
		dueAt = retryDueAt(c.RootCauseBucket, now, s.cfg.RetryDelay, s.cfg.TimeScale)
	} else {
		dueAt = dueAtFor(state, s.cfg.NudgeDelay, now)
	}
	steps := rescoringPath(c.ClaimedState, state, reason)

	if err := s.store.recordRescore(ctx, c, steps, pendingAction, score, trace, dueAt, attemptNumber, costPaise, now); err != nil {
		log.Error("failed to record rescore", logger.KeyError, err.Error())
		return
	}

	log.Info("attempt failed, re-scored rather than escalated",
		logger.KeyAction, c.PendingAction.String(), logger.KeyState, state.String(),
		"ev_paise", score.EVPaise, "p_recovery", score.PRecovery)
}

// ResumeNudge applies a delayed outcome report to a record parked in
// NUDGED: the counterpart to process()'s synchronous outcome handling, for
// an outcome that arrives later, out of band, via gRPC rather than the
// poll loop (docs/ARCHITECTURE.md section 6, the World Simulator's
// delayed-outcome callback; the RPC itself is services/decision-engine/
// internal/server, docs/PHASE5_IMPLEMENTATION.md Unit A).
//
// applied is false, with no error, whenever the report should be
// discarded rather than acted on: recordID does not exist, is not resting
// in NUDGED, or attemptNumber does not match the attempt currently
// awaiting resolution. All three are normal, not bugs -- this RPC is
// at-least-once like everything else here (decisionengine.proto) -- and
// the caller (services/decision-engine/internal/server) is expected to
// treat a discard as a successful, uneventful response, not an error.
func (s *Scheduler) ResumeNudge(ctx context.Context, recordID string, attemptNumber int, outcome commonv1.Outcome, failureCode string) (applied bool, resultingState commonv1.RecordState, err error) {
	now := s.clock.Now()

	c, state, found, err := s.store.loadNudged(ctx, recordID)
	if err != nil {
		return false, commonv1.RecordState_RECORD_STATE_UNSPECIFIED, err
	}
	if !found {
		return false, commonv1.RecordState_RECORD_STATE_UNSPECIFIED, nil
	}
	if state != commonv1.RecordState_RECORD_STATE_NUDGED || c.AttemptCount != attemptNumber {
		return false, state, nil
	}

	var (
		steps         []stateStep
		pendingAction commonv1.ActionType
		score         economics.Score
		trace         DecisionTrace
		dueAt         *time.Time
	)

	switch outcome {
	case commonv1.Outcome_OUTCOME_SUCCESS:
		toState, reason := decideAfterExecute(c.PendingAction, outcome)
		steps = []stateStep{{From: commonv1.RecordState_RECORD_STATE_NUDGED, To: toState, Reason: reason}}
	case commonv1.Outcome_OUTCOME_FAILURE:
		// The same re-entry to Scoring a synchronous execute failure takes
		// (handleFailedAttempt): a failed nudge outcome is re-priced with
		// this attempt spent, not escalated outright, so the two paths
		// cannot disagree about when a record has run out of road.
		history, err := s.store.loadAttemptHistory(ctx, recordID)
		if err != nil {
			return false, commonv1.RecordState_RECORD_STATE_UNSPECIFIED, fmt.Errorf("load attempt history for %s: %w", recordID, err)
		}
		toState, pending, reason, sc, tr := scoreAndRoute(s.economics, s.cfg.Guardrails, c.RootCauseBucket, history, c.AmountPaise, now)
		pendingAction, score, trace = pending, sc, tr
		if toState == commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED {
			dueAt = retryDueAt(c.RootCauseBucket, now, s.cfg.RetryDelay, s.cfg.TimeScale)
		} else {
			dueAt = dueAtFor(toState, s.cfg.NudgeDelay, now)
		}
		steps = rescoringPath(c.ClaimedState, toState, reason)
	default:
		// PENDING/UNSPECIFIED: a delayed outcome report must resolve to
		// something concrete. Anything else is a malformed report to
		// discard, not something to apply.
		return false, state, nil
	}

	// costPaise is 0 here deliberately: the nudge's own cost was already
	// recorded when it was sent (recordOutcome, at NUDGE_SCHEDULED ->
	// Nudged time). Its outcome resolving later incurs no new spend.
	//
	// failureCode has no further effect on this decision today: bucket
	// (not failure_code) drives scoreAndRoute's pricing, and re-deriving a
	// bucket from a delayed failure code would be a re-classification this
	// path deliberately does not do (see the comment on this method).
	// Logged by the caller so it is not silently dropped.
	applied, err = s.store.applyResumedOutcome(ctx, c, attemptNumber, steps, pendingAction, score, trace, dueAt, 0, now)
	if err != nil {
		return false, commonv1.RecordState_RECORD_STATE_UNSPECIFIED, err
	}
	if !applied {
		return false, state, nil
	}
	return true, steps[len(steps)-1].To, nil
}

func (s *Scheduler) executeWithRetry(ctx context.Context, c claimedRecord, attemptNumber int32) (*executorv1.ExecuteResponse, error) {
	var lastErr error
	for attempt := 1; attempt <= maxExecuteAttempts; attempt++ {
		resp, err := s.clients.execute(ctx, c.RecordID, c.BatchID, c.PendingAction, attemptNumber, c.AmountPaise, c.EVScoreAtDecision, c.PRecoveryAtDecision)
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
