// Package engine is the Decision Engine's core: the raw.events consumer
// handler (this file), the pure state-transition rules (state.go), the
// scheduler worker (scheduler.go), and their shared SQL (store.go) and
// wire types (rawevent.go, dlq.go). Each does one job
// (docs/ENGINEERING.md section 14); this file is orchestration only.
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
	"github.com/thisizaro/Momotaro/services/decision-engine/internal/economics"

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
	// RetryMandateLeadTime is RETRY_MANDATE_LEAD_TIME (docs/PRD.md section
	// 11a, docs/PHASE5_IMPLEMENTATION.md Unit J): the RBI e-mandate
	// framework's minimum pre-debit notification lead time, enforced by
	// retryDueAt as a floor on a MANDATE record's retry timing. Left
	// unscaled here for the same reason RetryDelay is: retryDueAt scales it
	// itself via TimeScale below.
	RetryMandateLeadTime time.Duration
	DLQTopic             string
	// AuditEventsTopic is audit.events (docs/ARCHITECTURE.md sections 8,
	// 10a, 6a): published to, best-effort, after every RECORD_STATE +
	// AUDIT_ENTRY transaction commits, so Reporting can drive
	// StreamBatchUpdates (docs/PHASE5_IMPLEMENTATION.md Unit F).
	AuditEventsTopic string
	// TimeScale is DEMO_TIME_SCALE (docs/ARCHITECTURE.md section 17),
	// threaded from config.Common so retryDueAt can compress salary-window
	// waits for a live demo.
	TimeScale float64
	// Guardrails are the hard limits from docs/PRD.md section 11: retry
	// budget, contact cap, contact cooldown and recovery window. They filter
	// the Classifier's recommendation before it is scheduled
	// (docs/ARCHITECTURE.md section 5a) and can only ever remove options.
	Guardrails GuardrailConfig
	// LLMSampleRate is LLM_SAMPLE_RATE (docs/PHASE3_IMPLEMENTATION.md Unit
	// H, reinterpreted by docs/DEMO_READINESS.md Unit AI): a ceiling on the
	// fraction of ALL classified records that ever reach a live model,
	// never a selector of which ones do. Default 0.0, validated at startup
	// in [0,1] by cmd/main.go, so every existing test and every default run
	// stays free. See llm_budget.go.
	LLMSampleRate float64
	// RouteConfidenceThreshold is LLM_ROUTE_CONFIDENCE_THRESHOLD
	// (docs/DEMO_READINESS.md Unit AI, docs/ARCHITECTURE.md section 17):
	// clients.classify asks the deterministic rules engine first and only
	// spends a live model call when its Confidence for this record is
	// below this threshold. Default 0.0, validated at startup in [0,1] by
	// cmd/main.go: the comparison is strict less-than and confidence is
	// never negative (rules/actions.go's lowest value is 0.00, for the
	// unknown-code path), so a threshold of 0.0 can never be satisfied and a
	// deployment that never sets this routes nothing to a live model,
	// matching LLMSampleRate's own zero-value default.
	RouteConfidenceThreshold float64
	// ClassifyConfidenceThreshold is CLASSIFY_CONFIDENCE_THRESHOLD
	// (docs/PHASE3_IMPLEMENTATION.md Unit G, classifier.proto): below this,
	// a classification is escalated as a safety call rather than priced.
	// Default 0.0, validated at startup in [0,1] by cmd/main.go, so a
	// deployment that never sets it escalates nothing on confidence (the
	// rules engine's own confidence values are all > 0).
	ClassifyConfidenceThreshold float64
}

// Engine consumes raw.events: classify a fresh record and schedule its
// first action. Execution itself happens later, driven by the scheduler
// worker (scheduler.go) when the scheduled due_at arrives, never inline
// here (docs/ARCHITECTURE.md section 7's diagram never executes directly
// from New).
type Engine struct {
	store     *store
	clients   *clients
	dlq       *deadLetterPublisher
	audit     *auditEventPublisher
	clock     clock.Clock
	economics *economics.Model
	cfg       Config
}

// New returns an Engine. dlqProducer publishes to cfg.DLQTopic
// (raw.events.dlq in production) and, on cfg.AuditEventsTopic, audit.events
// (docs/PHASE5_IMPLEMENTATION.md Unit F): one producer serves both, since
// kafkax.Producer.Publish takes the topic per call.
func New(pool *pgxpkg.Pool, classifier classifierv1.ClassifierServiceClient, executor executorv1.ExecutorServiceClient, dlqProducer *kafkax.Producer, clk clock.Clock, model *economics.Model, cfg Config) *Engine {
	return &Engine{
		store: newStore(pool),
		clients: &clients{
			classifier:               classifier,
			executor:                 executor,
			callTimeout:              cfg.CallTimeout,
			routeConfidenceThreshold: cfg.RouteConfidenceThreshold,
			llmBudget:                newLLMBudget(cfg.LLMSampleRate),
		},
		dlq:       newDeadLetterPublisher(dlqProducer, cfg.DLQTopic),
		audit:     newAuditEventPublisher(dlqProducer, cfg.AuditEventsTopic),
		clock:     clk,
		economics: model,
		cfg:       cfg,
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

	// Loaded before Classify, not after: these are its inputs
	// (classifier.proto ClassifyRequest.history/instrument_history), not the
	// guardrails' aggregate counters loaded below. A brand new record has no
	// rows of its own yet, but the instrument may already carry history from
	// other records (Phase 3 Unit F).
	classifyHistory, err := e.store.loadAttemptRows(ctx, evt.RecordID)
	if err != nil {
		return err
	}
	var instrumentHistory []*commonv1.InterventionAttempt
	if evt.InstrumentRef != "" {
		instrumentHistory, err = e.store.loadInstrumentHistory(ctx, evt.InstrumentRef, evt.RecordID)
		if err != nil {
			return err
		}
	}

	classifyResp, err := e.classifyWithRetry(ctx, record, classifyHistory, instrumentHistory)
	if err != nil {
		log.Error("classify failed after retries, dead-lettering", logger.KeyError, err.Error())
		return e.deadLetterEvent(ctx, evt, fmt.Sprintf("classify failed after %d attempts: %v", maxClassifyAttempts, err))
	}

	now := e.clock.Now()

	history, err := e.store.loadAttemptHistory(ctx, evt.RecordID)
	if err != nil {
		// ErrRecordNotFound is a permanent, data-shaped condition (the
		// record this message points at does not exist, most often because
		// something deleted it after the message was published), not an
		// infrastructure failure: retrying cannot help, because the row is
		// never coming back. Anything else here (a dropped connection, a
		// genuine query failure) stays fatal, exactly as ConsumeKeyed's
		// contract expects (docs/INCIDENTS.md 2026-08-31).
		if errors.Is(err, ErrRecordNotFound) {
			log.Error("record no longer exists, dead-lettering", logger.KeyError, err.Error())
			return e.deadLetterEvent(ctx, evt, fmt.Sprintf("record not found: %v", err))
		}
		return err
	}

	// Loaded fresh from PAYMENT_DOWNTIME on every classification, never
	// cached: the whole point is that a bank outage raised or resolved
	// between two records must be visible to both without a restart
	// (docs/PHASE5_5_IMPLEMENTATION.md Unit Y).
	downtime, err := e.store.loadDowntimeStatus(ctx, evt.InstrumentRef)
	if err != nil {
		return err
	}

	steps, pendingAction, score, trace := e.decide(classifyResp, history, downtime, evt.AmountPaise, now)
	final := steps[len(steps)-1].To
	var dueAt *time.Time
	if final == commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED {
		dueAt = retryDueAt(classifyResp.GetBucket(), record.GetType(), now, e.cfg.RetryDelay, e.cfg.RetryMandateLeadTime, e.cfg.TimeScale)
	} else {
		dueAt = dueAtFor(final, e.cfg.NudgeDelay, now, e.cfg.TimeScale)
	}

	if err := e.store.scheduleNew(ctx, log, evt, classifyResp.GetBucket(), steps, pendingAction, classifyResp.GetRationale(), classifyResp.GetSource(), classifyResp.GetHops(), score, trace, dueAt, now); err != nil {
		// Same classification as the loadAttemptHistory check above, for the
		// narrower race it closes: the record existed when this handler
		// call started but was deleted before this insert (store.go's
		// isForeignKeyViolation), which is the same permanent, data-shaped
		// condition, just caught later.
		if errors.Is(err, ErrRecordNotFound) {
			log.Error("record deleted during processing, dead-lettering", logger.KeyError, err.Error())
			return e.deadLetterEvent(ctx, evt, fmt.Sprintf("record deleted during processing: %v", err))
		}
		return fmt.Errorf("schedule record %s: %w", evt.RecordID, err)
	}
	// Best-effort, after the transaction above has already committed:
	// audit.events is a notification stream, never a system of record
	// (docs/ARCHITECTURE.md section 10a), so a publish failure here must
	// not undo or fail a state change that already happened.
	if err := e.audit.Publish(ctx, evt.RecordID, evt.BatchID, steps[0].From, final, recoveredDelta(final, evt.AmountPaise), now); err != nil {
		log.Error("failed to publish audit event", logger.KeyError, err.Error())
	}

	log.Info("record classified and scheduled",
		logger.KeyState, final.String(), logger.KeyBucket, classifyResp.GetBucket().String(), logger.KeySource, classifyResp.GetSource().String(),
		"recommended_action", classifyResp.GetRecommendedAction().String(), "scheduled_action", pendingAction.String(),
		"ev_paise", score.EVPaise, "p_recovery", score.PRecovery)
	return nil
}

// decide runs the fixed order from docs/ARCHITECTURE.md section 5a: the
// Classifier has proposed, the guardrails now constrain, and deterministic
// economics decides. It returns the transitions to record, the action that was
// scheduled, the winning score, and the full decision trace (every candidate
// considered and every action the guardrails blocked, docs/PHASE5_IMPLEMENTATION.md
// Unit M); the trace is the zero value on either bypass path below, since
// escalating on confidence or recommendation never reaches scoring at all.
//
// Note what the Classifier's recommended_action is NOT used for here. Once the
// scorer exists, selection is by expected value over the whole permitted menu,
// and the Classifier's real contribution is the BUCKET, which is what the
// prior table is keyed on. That is the concrete answer to "does the model
// decide how money is spent?": it does not, it only says what went wrong.
func (e *Engine) decide(resp *classifierv1.ClassifyResponse, history attemptHistory, downtime downtimeStatus, amountPaise int64, now time.Time) ([]stateStep, commonv1.ActionType, economics.Score, DecisionTrace) {
	none := commonv1.ActionType_ACTION_TYPE_UNSPECIFIED

	// Below the configured confidence threshold is a safety call, exactly
	// like an explicit escalation recommendation: bypass economics entirely
	// rather than price a diagnosis the classifier itself was not confident
	// in (classifier.proto, ARCHITECTURE.md section 5, Phase 3 Unit G).
	// Checked before the recommended-action escalate check below on
	// purpose, because the rules engine's unknown-code path always returns
	// confidence 0.0 AND recommends ESCALATE (rules/actions.go), so once a
	// threshold above 0.0 is configured, both conditions are true for that
	// same record. The reason string names whichever is the REAL cause
	// rather than whichever branch happened to run first, so the audit
	// trail can still tell "we do not recognise this failure code" from
	// "the model was unsure" -- those call for different human follow-up.
	// Threshold 0.0 (the default) never fires here, since every rules
	// engine confidence value is > 0: this changes no existing behaviour
	// until deliberately turned on.
	if resp.GetConfidence() < e.cfg.ClassifyConfidenceThreshold {
		reason := "classification confidence below threshold"
		if resp.GetRecommendedAction() == commonv1.ActionType_ACTION_TYPE_ESCALATE {
			reason = "classifier recommended escalation"
		}
		return directPath(commonv1.RecordState_RECORD_STATE_ESCALATED, reason), none, economics.Score{}, DecisionTrace{}
	}

	// Escalation is the one recommendation that bypasses economics. A risk
	// hold is a safety call, and pricing it would imply it were negotiable.
	if resp.GetRecommendedAction() == commonv1.ActionType_ACTION_TYPE_ESCALATE {
		return directPath(commonv1.RecordState_RECORD_STATE_ESCALATED, "classifier recommended escalation"), none, economics.Score{}, DecisionTrace{}
	}

	state, pendingAction, reason, score, trace := scoreAndRoute(e.economics, e.cfg.Guardrails, resp.GetBucket(), history, downtime, amountPaise, now)
	return scoringPath(state, reason), pendingAction, score, trace
}

// classifyWithRetry retries a failing Classify call up to
// maxClassifyAttempts times, waiting classifyRetryDelay between attempts
// via the injected clock so this is testable without a real wait
// (docs/ENGINEERING.md section 2).
func (e *Engine) classifyWithRetry(ctx context.Context, record *commonv1.Record, history, instrumentHistory []*commonv1.InterventionAttempt) (*classifierv1.ClassifyResponse, error) {
	var lastErr error
	for attempt := 1; attempt <= maxClassifyAttempts; attempt++ {
		resp, err := e.clients.classify(ctx, record, history, instrumentHistory)
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
