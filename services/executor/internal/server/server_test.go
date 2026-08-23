//go:build integration

// Executor's tests exercise real Postgres rather than a mock, per
// docs/ENGINEERING.md section 1 ("do not mock what you own"). They therefore
// need the docker-compose stack up, so they sit behind the `integration`
// build tag: `go test ./...` on a bare checkout must not dial a database that
// is not running. Run with `make test-integration`.

package server

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thisizaro/Momotaro/internal/platform/clock"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	executorv1 "github.com/thisizaro/Momotaro/proto/gen/executor/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

// seedRecord inserts a minimal batch+record row so intervention_attempt's
// foreign key is satisfiable, and returns the record id.
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

func alwaysSucceeds(calls *int32) OutcomeFunc {
	return func(ctx context.Context) (commonv1.Outcome, int64, error) {
		atomic.AddInt32(calls, 1)
		return commonv1.Outcome_OUTCOME_SUCCESS, 0, nil
	}
}

func TestExecuteInsertsAttemptBeforeCallingTheOutcomeStub(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	recordID := seedRecord(ctx, t, pool)

	var calls int32
	s := New(pool, clock.New(), alwaysSucceeds(&calls))

	resp, err := s.Execute(ctx, &executorv1.ExecuteRequest{
		RecordId:      recordID,
		ActionType:    commonv1.ActionType_ACTION_TYPE_RETRY,
		AttemptNumber: 1,
		AmountPaise:   10000,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if resp.Outcome != commonv1.Outcome_OUTCOME_SUCCESS {
		t.Errorf("Outcome = %v, want SUCCESS", resp.Outcome)
	}
	if resp.AlreadyExecuted {
		t.Error("AlreadyExecuted = true on the first call")
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("outcome stub called %d times, want 1", calls)
	}

	var count int
	var outcome string
	err = pool.QueryRow(ctx, `SELECT count(*), max(outcome) FROM intervention_attempt WHERE record_id=$1 AND attempt_number=1`, recordID).Scan(&count, &outcome)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 1 {
		t.Fatalf("intervention_attempt rows = %d, want 1", count)
	}
	if outcome != commonv1.Outcome_OUTCOME_SUCCESS.String() {
		t.Errorf("stored outcome = %q, want %q", outcome, commonv1.Outcome_OUTCOME_SUCCESS.String())
	}
}

// The durable idempotency guarantee (docs/ARCHITECTURE.md section 11): a
// redelivered request for the same (record_id, attempt_number) must not
// execute the action twice.
func TestExecuteIsIdempotentOnRedelivery(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	recordID := seedRecord(ctx, t, pool)

	var calls int32
	s := New(pool, clock.New(), alwaysSucceeds(&calls))

	req := &executorv1.ExecuteRequest{
		RecordId:      recordID,
		ActionType:    commonv1.ActionType_ACTION_TYPE_RETRY,
		AttemptNumber: 1,
		AmountPaise:   10000,
	}

	first, err := s.Execute(ctx, req)
	if err != nil {
		t.Fatalf("first Execute: %v", err)
	}

	second, err := s.Execute(ctx, req)
	if err != nil {
		t.Fatalf("second Execute (redelivery): %v", err)
	}

	if !second.AlreadyExecuted {
		t.Error("AlreadyExecuted = false on redelivery, want true")
	}
	if second.Outcome != first.Outcome {
		t.Errorf("redelivery Outcome = %v, want %v (the original)", second.Outcome, first.Outcome)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("outcome stub called %d times across both calls, want exactly 1", calls)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM intervention_attempt WHERE record_id=$1 AND attempt_number=1`, recordID).Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 1 {
		t.Errorf("intervention_attempt rows = %d, want exactly 1 despite two calls", count)
	}
}

// Concurrent duplicate delivery (the actual at-least-once shape) must also
// collapse to one side effect. This is the -race-sensitive case.
func TestExecuteIsIdempotentUnderConcurrentDuplicateDelivery(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	recordID := seedRecord(ctx, t, pool)

	var calls int32
	s := New(pool, clock.New(), alwaysSucceeds(&calls))

	req := &executorv1.ExecuteRequest{
		RecordId:      recordID,
		ActionType:    commonv1.ActionType_ACTION_TYPE_RETRY,
		AttemptNumber: 1,
		AmountPaise:   10000,
	}

	const n = 8
	var wg sync.WaitGroup
	results := make([]*executorv1.ExecuteResponse, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = s.Execute(ctx, req)
		}(i)
	}
	wg.Wait()

	successCount := 0
	for i, err := range errs {
		if err != nil {
			t.Fatalf("Execute[%d]: %v", i, err)
		}
		if results[i].Outcome != commonv1.Outcome_OUTCOME_SUCCESS {
			t.Errorf("Execute[%d].Outcome = %v, want SUCCESS", i, results[i].Outcome)
		}
		if !results[i].AlreadyExecuted {
			successCount++
		}
	}
	if successCount != 1 {
		t.Errorf("%d of %d concurrent calls executed the side effect, want exactly 1", successCount, n)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("outcome stub called %d times, want exactly 1", calls)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM intervention_attempt WHERE record_id=$1 AND attempt_number=1`, recordID).Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 1 {
		t.Errorf("intervention_attempt rows = %d, want exactly 1 after %d concurrent calls", count, n)
	}
}

// Different attempt_number on the same record is a different attempt, not a
// duplicate, and must execute independently.
func TestExecuteTreatsDifferentAttemptNumbersIndependently(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	recordID := seedRecord(ctx, t, pool)

	var calls int32
	s := New(pool, clock.New(), alwaysSucceeds(&calls))

	for _, attempt := range []int32{1, 2} {
		resp, err := s.Execute(ctx, &executorv1.ExecuteRequest{
			RecordId:      recordID,
			ActionType:    commonv1.ActionType_ACTION_TYPE_RETRY,
			AttemptNumber: attempt,
			AmountPaise:   10000,
		})
		if err != nil {
			t.Fatalf("Execute attempt %d: %v", attempt, err)
		}
		if resp.AlreadyExecuted {
			t.Errorf("attempt %d reported AlreadyExecuted, want a fresh execution", attempt)
		}
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("outcome stub called %d times, want 2 (one per attempt)", calls)
	}
}

func TestExecuteValidation(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	recordID := seedRecord(ctx, t, pool)

	var calls int32
	s := New(pool, clock.New(), alwaysSucceeds(&calls))

	tests := []struct {
		name string
		req  *executorv1.ExecuteRequest
	}{
		{"missing record_id", &executorv1.ExecuteRequest{ActionType: commonv1.ActionType_ACTION_TYPE_RETRY, AttemptNumber: 1}},
		{"zero attempt_number", &executorv1.ExecuteRequest{RecordId: recordID, ActionType: commonv1.ActionType_ACTION_TYPE_RETRY, AttemptNumber: 0}},
		{"negative attempt_number", &executorv1.ExecuteRequest{RecordId: recordID, ActionType: commonv1.ActionType_ACTION_TYPE_RETRY, AttemptNumber: -1}},
		{"unspecified action_type", &executorv1.ExecuteRequest{RecordId: recordID, AttemptNumber: 1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.Execute(ctx, tc.req)
			if status.Code(err) != codes.InvalidArgument {
				t.Errorf("Execute(%+v): err = %v, want InvalidArgument", tc.req, err)
			}
		})
	}
	if calls != 0 {
		t.Errorf("outcome stub called on invalid input, calls = %d", calls)
	}
}
