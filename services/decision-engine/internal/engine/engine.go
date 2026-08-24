// Package engine is the Decision Engine's core: the raw.events consumer
// handler (this file), the pure state-transition rules (state.go), the
// scheduler worker (scheduler.go), and their shared SQL (store.go) and
// wire types (rawevent.go, dlq.go). Each does one job
// (docs/ENGINEERING.md section 14); this file is orchestration only.
package engine

import (
	"context"
	"encoding/json"
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

// maxClassifyAttempts and classifyRetryDelay bound HandleMessage's own
// retry of a transient classify failure before dead-lettering the record
// (docs/ARCHITECTURE.md section 8b): a blip must not immediately poison a
// partition, but a permanently unreachable Classifier must not spin
// forever either.
const (
	maxClassifyAttempts = 3
	classifyRetryDelay  = 200 * time.Millisecond
)

// Config holds Engine's tunable knobs, grouped so New's call site does not
// become an unreadable run of positional durations.
type Config struct {
	CallTimeout time.Duration
	// RetryDelay and NudgeDelay are Phase 1 placeholders for the
	// cause-aware retry timing docs/ARCHITECTURE.md section 5a describes
	// (salary window for insufficient_funds, short backoff for
	// bank_timeout, etc). That policy is Phase 2 work, built on top of the
	// scheduler mechanism Phase 1 proves here with one fixed delay per
	// action family.
	RetryDelay time.Duration
	NudgeDelay time.Duration
	DLQTopic   string
	// Guardrails are the hard limits from docs/PRD.md section 11: retry
	// budget, contact cap, contact cooldown and recovery window. They filter
	// the Classifier's recommendation before it is scheduled
	// (docs/ARCHITECTURE.md section 5a) and can only ever remove options.
	Guardrails GuardrailConfig
}

// Engine consumes raw.events: classify a fresh record and schedule its
// first action. Execution itself happens later, driven by the scheduler
// worker (scheduler.go) when the scheduled due_at arrives, never inline
// here (docs/ARCHITECTURE.md section 7's diagram never executes directly
// from New).
type Engine struct {
	store   *store
	clients *clients
	dlq     *deadLetterPublisher
	clock   clock.Clock
	cfg     Config
}

// New returns an Engine. dlqProducer publishes to cfg.DLQTopic
// (raw.events.dlq in production).
func New(pool *pgxpkg.Pool, classifier classifierv1.ClassifierServiceClient, executor executorv1.ExecutorServiceClient, dlqProducer *kafkax.Producer, clk clock.Clock, cfg Config) *Engine {
	return &Engine{
		store:   newStore(pool),
		clients: &clients{classifier: classifier, executor: executor, callTimeout: cfg.CallTimeout},
		dlq:     newDeadLetterPublisher(dlqProducer, cfg.DLQTopic),
		clock:   clk,
		cfg:     cfg,
	}
}

// HandleMessage processes one raw.events record: classify it and schedule
// its first action. This is the handler passed to kafkax.ConsumeKeyed, so
// it must resolve every record's fate itself: a malformed payload or a
// Classifier that will not answer is dead-lettered, never returned as an
// error, which under ConsumeKeyed's contract would be treated as an
// infrastructure failure and stop the whole consumer.
func (e *Engine) HandleMessage(ctx context.Context, msg kafkax.Message) error {
	var evt RawEvent
	if err := json.Unmarshal(msg.Value, &evt); err != nil {
		return e.deadLetterRaw(ctx, msg, fmt.Sprintf("unmarshal raw event: %v", err))
	}

	log := logger.ForRecord(logger.From(ctx), evt.RecordID, evt.BatchID)

	exists, err := e.store.recordStateExists(ctx, evt.RecordID)
	if err != nil {
		return err
	}
	if exists {
		log.Info("record_state already exists, skipping redelivered raw.events message")
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

	classifyResp, err := e.classifyWithRetry(ctx, record)
	if err != nil {
		log.Error("classify failed after retries, dead-lettering", logger.KeyError, err.Error())
		return e.deadLetterEvent(ctx, evt, fmt.Sprintf("classify failed after %d attempts: %v", maxClassifyAttempts, err))
	}

	now := e.clock.Now()

	// Classify, then guardrails filter, then (Phase 2) economics decides. The
	// order is fixed (docs/ARCHITECTURE.md section 5a) and is what lets us say
	// the model proposes but never decides how money is spent.
	history, err := e.store.loadAttemptHistory(ctx, evt.RecordID)
	if err != nil {
		return err
	}
	recommended := classifyResp.GetRecommendedAction()
	action, downgrade := permittedOrEscalate(recommended, applyGuardrails(history, e.cfg.Guardrails, now))

	state, pendingAction, reason := decideForAction(action)
	if downgrade != "" {
		// The guardrail's own words become the audit trail's justification for
		// the downgrade, so the trail answers "why did it not retry?".
		reason = fmt.Sprintf("guardrail blocked %s: %s", recommended, downgrade)
	}
	dueAt := e.dueAtFor(state, now)

	if err := e.store.scheduleNew(ctx, evt, classifyResp.GetBucket(), state, pendingAction, reason, classifyResp.GetRationale(), classifyResp.GetSource(), dueAt, now); err != nil {
		return fmt.Errorf("schedule record %s: %w", evt.RecordID, err)
	}

	log.Info("record classified and scheduled",
		logger.KeyState, state.String(), logger.KeyBucket, classifyResp.GetBucket().String(), logger.KeySource, classifyResp.GetSource().String(),
		"recommended_action", recommended.String(), "scheduled_action", action.String(), "guardrail_downgrade", downgrade)
	return nil
}

// dueAtFor computes when a newly scheduled record should first become
// actionable, or nil for a state that is not waiting on the scheduler
// (docs/ARCHITECTURE.md section 7a: due_at is what the scheduler polls).
func (e *Engine) dueAtFor(state commonv1.RecordState, now time.Time) *time.Time {
	var delay time.Duration
	switch state {
	case commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED:
		delay = e.cfg.RetryDelay
	case commonv1.RecordState_RECORD_STATE_NUDGE_SCHEDULED:
		delay = e.cfg.NudgeDelay
	default:
		return nil
	}
	due := now.Add(delay)
	return &due
}

// classifyWithRetry retries a failing Classify call up to
// maxClassifyAttempts times, waiting classifyRetryDelay between attempts
// via the injected clock so this is testable without a real wait
// (docs/ENGINEERING.md section 2).
func (e *Engine) classifyWithRetry(ctx context.Context, record *commonv1.Record) (*classifierv1.ClassifyResponse, error) {
	var lastErr error
	for attempt := 1; attempt <= maxClassifyAttempts; attempt++ {
		resp, err := e.clients.classify(ctx, record)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if attempt < maxClassifyAttempts {
			select {
			case <-e.clock.After(classifyRetryDelay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	return nil, lastErr
}

func (e *Engine) deadLetterRaw(ctx context.Context, msg kafkax.Message, reason string) error {
	dl := DeadLetter{RawValue: string(msg.Value), FailureReason: reason, FailedAt: e.clock.Now()}
	if err := e.dlq.Publish(ctx, dl); err != nil {
		return fmt.Errorf("publish dead letter: %w", err)
	}
	logger.From(ctx).Error("record dead-lettered, payload could not be parsed", logger.KeyError, reason)
	return nil
}

func (e *Engine) deadLetterEvent(ctx context.Context, evt RawEvent, reason string) error {
	dl := DeadLetter{RecordID: evt.RecordID, BatchID: evt.BatchID, FailureReason: reason, FailedAt: e.clock.Now()}
	if err := e.dlq.Publish(ctx, dl); err != nil {
		return fmt.Errorf("publish dead letter for %s: %w", evt.RecordID, err)
	}
	logger.ForRecord(logger.From(ctx), evt.RecordID, evt.BatchID).Error("record dead-lettered", logger.KeyError, reason)
	return nil
}
