//go:build integration

// The Executor's tests exercise real Postgres rather than a mock, per
// docs/ENGINEERING.md section 1 ("do not mock what you own"), because the
// UNIQUE (record_id, attempt_number) constraint IS the idempotency guarantee
// and a mock would only test our beliefs about it. They therefore need the
// docker-compose stack up and sit behind the `integration` build tag. Run
// with `make test-integration`.

package server

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thisizaro/Momotaro/internal/platform/clock"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	notifierv1 "github.com/thisizaro/Momotaro/proto/gen/notifier/v1"
	"github.com/thisizaro/Momotaro/services/executor/internal/attempt"
	"github.com/thisizaro/Momotaro/services/executor/internal/ports"
)

func dsn(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("POSTGRES_DSN"); v != "" {
		return v
	}
	return "postgres://momotaro:momotaro@localhost:5432/momotaro?sslmode=disable"
}

func testPool(t *testing.T) *pgxpkg.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpkg.NewPool(ctx, dsn(t))
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedRecord inserts a minimal batch+record so intervention_attempt's foreign
// key is satisfiable, and returns the record id.
func seedRecord(ctx context.Context, t *testing.T, pool *pgxpkg.Pool) string {
	t.Helper()
	batchID := uuid.NewString()
	recordID := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO batch (id, source) VALUES ($1, 'test')`, batchID); err != nil {
		t.Fatalf("seed batch: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO record (id, batch_id, type, amount_paise, failure_code)
		VALUES ($1, $2, 'RECORD_TYPE_PAYMENT', 10000, 'BANK_TIMEOUT')`, recordID, batchID); err != nil {
		t.Fatalf("seed record: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM intervention_attempt WHERE record_id = $1`, recordID)
		_, _ = pool.Exec(bg, `DELETE FROM record WHERE id = $1`, recordID)
		_, _ = pool.Exec(bg, `DELETE FROM batch WHERE id = $1`, batchID)
	})
	return recordID
}

// countingRecovery wraps a scripted answer and counts how many times the
// action really ran. docs/ENGINEERING.md section 8: idempotency for anything
// touching money must be proven by a test that delivers the same action twice
// and asserts one effect, which needs a real count, not an inference.
type countingRecovery struct {
	calls atomic.Int32
	out   ports.RecoveryAction
	err   error
}

func (c *countingRecovery) SimulateOutcome(ctx context.Context, recordID string, action commonv1.ActionType, attemptNumber int32) (ports.RecoveryAction, error) {
	c.calls.Add(1)
	if c.err != nil {
		return ports.RecoveryAction{}, c.err
	}
	return c.out, nil
}

type countingNotification struct {
	calls atomic.Int32
	out   ports.Notification
	err   error
}

func (c *countingNotification) SimulateSend(ctx context.Context, recordID string, channel notifierv1.Channel, message string) (ports.Notification, error) {
	c.calls.Add(1)
	if c.err != nil {
		return ports.Notification{}, c.err
	}
	return c.out, nil
}

func succeedingRetry() *countingRecovery {
	return &countingRecovery{out: ports.RecoveryAction{
		Outcome:   commonv1.Outcome_OUTCOME_SUCCESS,
		Immediate: true,
		CostPaise: 200,
	}}
}

// newServer wires a Server against real Postgres and the given ports.
func newServer(t *testing.T, pool *pgxpkg.Pool, rec ports.RecoveryActionPort, note ports.NotificationPort) *Server {
	t.Helper()
	clk := clock.New()
	return New(attempt.NewStore(pool), ports.NewRouter(rec, note), clk)
}

type storedAttempt struct {
	Outcome             string
	CostPaise           int64
	FailureCode         string
	MessageText         string
	ActionType          string
	AttemptNumber       int32
	Count               int
	EVScoreAtDecision   *float64
	PRecoveryAtDecision *float64
}

// loadAttempt reads back what was actually persisted, so assertions are
// against Postgres rather than against the response object we just built.
func loadAttempt(ctx context.Context, t *testing.T, pool *pgxpkg.Pool, recordID string, attemptNumber int32) storedAttempt {
	t.Helper()
	var got storedAttempt
	err := pool.QueryRow(ctx, `
		SELECT count(*), max(outcome), max(cost_paise), coalesce(max(failure_code), ''),
		       coalesce(max(message_text), ''), max(action_type),
		       max(ev_score_at_decision), max(p_recovery_at_decision)
		FROM intervention_attempt WHERE record_id=$1 AND attempt_number=$2`,
		recordID, attemptNumber,
	).Scan(&got.Count, &got.Outcome, &got.CostPaise, &got.FailureCode, &got.MessageText, &got.ActionType,
		&got.EVScoreAtDecision, &got.PRecoveryAtDecision)
	if err != nil {
		t.Fatalf("load attempt: %v", err)
	}
	got.AttemptNumber = attemptNumber
	return got
}
