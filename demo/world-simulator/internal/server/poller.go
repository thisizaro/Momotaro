package server

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/thisizaro/Momotaro/internal/platform/clock"
	decisionenginev1 "github.com/thisizaro/Momotaro/proto/gen/decisionengine/v1"
)

// maxDeliverAttempts/deliverRetryDelay mirror services/decision-engine/
// internal/engine/scheduler.go's executeWithRetry: a blip calling
// DecisionEngine is retried a bounded number of times before the outcome
// is logged as lost, rather than silently dropped on the first error.
const (
	maxDeliverAttempts = 3
	deliverRetryDelay  = 200 * time.Millisecond
)

// Poller is docs/ARCHITECTURE.md section 6's "background loop inside
// World Simulator (a simple ticker, no separate service)": it drains the
// delayed-outcome queue and resumes each record's state machine via
// DecisionEngine.ReportDelayedOutcome.
type Poller struct {
	queue          *queue
	decisionEngine decisionenginev1.DecisionEngineServiceClient
	clock          clock.Clock
	pollInterval   time.Duration
	callTimeout    time.Duration
	log            *slog.Logger
}

// NewPoller returns a Poller. redisClient backs the same delayed-outcome
// queue Server writes to (docs/ARCHITECTURE.md section 6); the queue type
// itself stays unexported, so this is the only door into it from outside
// the package, matching Server.New's own redis.Client parameter.
func NewPoller(redisClient *redis.Client, de decisionenginev1.DecisionEngineServiceClient, clk clock.Clock, pollInterval, callTimeout time.Duration, log *slog.Logger) *Poller {
	return &Poller{
		queue:          newQueue(redisClient),
		decisionEngine: de,
		clock:          clk,
		pollInterval:   pollInterval,
		callTimeout:    callTimeout,
		log:            log,
	}
}

// Run polls every pollInterval, via the injected clock rather than
// time.Ticker so this is testable without a real wait
// (docs/ENGINEERING.md section 2), until ctx is cancelled.
func (p *Poller) Run(ctx context.Context) error {
	for {
		if err := p.tick(ctx); err != nil {
			return err
		}
		select {
		case <-p.clock.After(p.pollInterval):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// tick drains every due entry. A queue-scan failure (Redis unreachable) is
// a genuine infrastructure failure and is returned; a single delivery's
// failure is handled internally (bounded retry, then logged) and never
// stops the tick, matching Scheduler.tick's split between "the claim
// itself failed" and "one record's processing failed".
func (p *Poller) tick(ctx context.Context) error {
	delivered, malformed, err := p.queue.due(ctx, p.clock.Now())
	if err != nil {
		return fmt.Errorf("scan due delayed outcomes: %w", err)
	}
	for _, m := range malformed {
		p.log.Warn("dropped malformed delayed-outcome member, written by this service to itself", "member", m)
	}
	for _, d := range delivered {
		p.deliver(ctx, d)
	}
	return nil
}

func (p *Poller) deliver(ctx context.Context, d delayedOutcome) {
	var lastErr error
	for attempt := 1; attempt <= maxDeliverAttempts; attempt++ {
		callCtx, cancel := context.WithTimeout(ctx, p.callTimeout)
		resp, err := p.decisionEngine.ReportDelayedOutcome(callCtx, &decisionenginev1.ReportDelayedOutcomeRequest{
			RecordId:      d.RecordID,
			AttemptNumber: d.AttemptNumber,
			Outcome:       d.Outcome,
			FailureCode:   d.FailureCode,
		})
		cancel()
		if err == nil {
			p.log.Info("delayed outcome delivered",
				"record_id", d.RecordID, "attempt_number", d.AttemptNumber,
				"applied", resp.GetApplied(), "resulting_state", resp.GetResultingState().String())
			return
		}
		lastErr = err
		if attempt < maxDeliverAttempts {
			select {
			case <-p.clock.After(deliverRetryDelay):
			case <-ctx.Done():
				return
			}
		}
	}
	// Already removed from the queue by due(): a delivery that exhausts
	// its retries here is lost, not retried on the next tick. Acceptable
	// for a DEMO ONLY component (docs/ARCHITECTURE.md section 6); logged
	// loudly rather than silently, so it is visible if it ever happens.
	p.log.Error("report delayed outcome failed after retries, outcome lost",
		"record_id", d.RecordID, "attempt_number", d.AttemptNumber, "err", lastErr)
}
