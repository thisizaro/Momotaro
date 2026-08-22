package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thisizaro/Momotaro/internal/platform/clock"
	"github.com/thisizaro/Momotaro/internal/platform/kafkax"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	executorv1 "github.com/thisizaro/Momotaro/proto/gen/executor/v1"
	"google.golang.org/grpc"
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

func seedRecord(ctx context.Context, t *testing.T, pool *pgxpkg.Pool) (batchID, recordID string) {
	t.Helper()
	batchID = uuid.NewString()
	recordID = uuid.NewString()
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
		_, _ = pool.Exec(bg, `DELETE FROM audit_entry WHERE record_id = $1`, recordID)
		_, _ = pool.Exec(bg, `DELETE FROM record_state WHERE record_id = $1`, recordID)
		_, _ = pool.Exec(bg, `DELETE FROM record WHERE id = $1`, recordID)
		_, _ = pool.Exec(bg, `DELETE FROM batch WHERE id = $1`, batchID)
	})
	return batchID, recordID
}

func rawEventMessage(t *testing.T, evt RawEvent) kafkax.Message {
	t.Helper()
	b, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("marshal RawEvent: %v", err)
	}
	return kafkax.Message{Topic: "raw.events", Key: evt.RecordID, Value: b}
}

type fakeClassifier struct {
	resp  *classifierv1.ClassifyResponse
	err   error
	calls int32
}

func (f *fakeClassifier) Classify(ctx context.Context, in *classifierv1.ClassifyRequest, opts ...grpc.CallOption) (*classifierv1.ClassifyResponse, error) {
	atomic.AddInt32(&f.calls, 1)
	return f.resp, f.err
}
func (f *fakeClassifier) ComposeNudge(ctx context.Context, in *classifierv1.ComposeNudgeRequest, opts ...grpc.CallOption) (*classifierv1.ComposeNudgeResponse, error) {
	return nil, errors.New("not used by the walking skeleton")
}

type fakeExecutor struct {
	resp  *executorv1.ExecuteResponse
	err   error
	calls int32
}

func (f *fakeExecutor) Execute(ctx context.Context, in *executorv1.ExecuteRequest, opts ...grpc.CallOption) (*executorv1.ExecuteResponse, error) {
	atomic.AddInt32(&f.calls, 1)
	return f.resp, f.err
}

func successClassifier() *fakeClassifier {
	return &fakeClassifier{resp: &classifierv1.ClassifyResponse{
		Bucket:            commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
		RecommendedAction: commonv1.ActionType_ACTION_TYPE_RETRY,
		Rationale:         "transient bank failure, retry",
		Confidence:        1,
		Source:            commonv1.Source_SOURCE_RULES_FALLBACK,
	}}
}

func TestHandleMessageReachesRecoveredOnSuccess(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	batchID, recordID := seedRecord(ctx, t, pool)

	classifier := successClassifier()
	executor := &fakeExecutor{resp: &executorv1.ExecuteResponse{Outcome: commonv1.Outcome_OUTCOME_SUCCESS, CostPaise: 0}}

	e := New(pool, classifier, executor, clock.New(), 2*time.Second)

	msg := rawEventMessage(t, RawEvent{
		RecordID: recordID, BatchID: batchID, Type: "RECORD_TYPE_PAYMENT",
		AmountPaise: 10000, Currency: "INR", FailureCode: "BANK_TIMEOUT",
	})
	if err := e.HandleMessage(ctx, msg); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	var state string
	var attemptCount int
	var bucket string
	if err := pool.QueryRow(ctx, `SELECT current_state, attempt_count, root_cause_bucket FROM record_state WHERE record_id=$1`, recordID).
		Scan(&state, &attemptCount, &bucket); err != nil {
		t.Fatalf("query record_state: %v", err)
	}
	if state != commonv1.RecordState_RECORD_STATE_RECOVERED.String() {
		t.Errorf("current_state = %q, want %q", state, commonv1.RecordState_RECORD_STATE_RECOVERED.String())
	}
	if attemptCount != 1 {
		t.Errorf("attempt_count = %d, want 1", attemptCount)
	}
	if bucket != commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK.String() {
		t.Errorf("root_cause_bucket = %q, want TRANSIENT_BANK", bucket)
	}

	var entryCount int
	var toState, rationale, source string
	var attemptNumber int
	if err := pool.QueryRow(ctx, `
		SELECT count(*), max(to_state), max(rationale), max(source), max(attempt_number)
		FROM audit_entry WHERE record_id=$1`, recordID,
	).Scan(&entryCount, &toState, &rationale, &source, &attemptNumber); err != nil {
		t.Fatalf("query audit_entry: %v", err)
	}
	if entryCount != 1 {
		t.Fatalf("audit_entry rows = %d, want 1", entryCount)
	}
	if toState != commonv1.RecordState_RECORD_STATE_RECOVERED.String() {
		t.Errorf("audit to_state = %q, want RECOVERED", toState)
	}
	if rationale != "transient bank failure, retry" {
		t.Errorf("audit rationale = %q, want the classifier's rationale", rationale)
	}
	if source != commonv1.Source_SOURCE_RULES_FALLBACK.String() {
		t.Errorf("audit source = %q, want SOURCE_RULES_FALLBACK", source)
	}
	if attemptNumber != 1 {
		t.Errorf("audit attempt_number = %d, want 1", attemptNumber)
	}

	if classifier.calls != 1 {
		t.Errorf("classifier called %d times, want 1", classifier.calls)
	}
	if executor.calls != 1 {
		t.Errorf("executor called %d times, want 1", executor.calls)
	}
}

func TestHandleMessageEscalatesOnExecutionFailure(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	batchID, recordID := seedRecord(ctx, t, pool)

	classifier := successClassifier()
	executor := &fakeExecutor{resp: &executorv1.ExecuteResponse{Outcome: commonv1.Outcome_OUTCOME_FAILURE, FailureCode: "HARD_DECLINE"}}

	e := New(pool, classifier, executor, clock.New(), 2*time.Second)
	msg := rawEventMessage(t, RawEvent{RecordID: recordID, BatchID: batchID, Type: "RECORD_TYPE_PAYMENT", AmountPaise: 10000, FailureCode: "BANK_TIMEOUT"})

	if err := e.HandleMessage(ctx, msg); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	var state string
	if err := pool.QueryRow(ctx, `SELECT current_state FROM record_state WHERE record_id=$1`, recordID).Scan(&state); err != nil {
		t.Fatalf("query record_state: %v", err)
	}
	if state != commonv1.RecordState_RECORD_STATE_ESCALATED.String() {
		t.Errorf("current_state = %q, want ESCALATED", state)
	}
}

// A record already in a terminal state must not be reprocessed: this is
// the guard against a Kafka redelivery (e.g. crash before offset commit)
// double-writing the audit trail.
func TestHandleMessageSkipsAlreadyTerminalRecord(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	batchID, recordID := seedRecord(ctx, t, pool)

	if _, err := pool.Exec(ctx, `INSERT INTO record_state (record_id, current_state, attempt_count) VALUES ($1, $2, 1)`,
		recordID, commonv1.RecordState_RECORD_STATE_RECOVERED.String()); err != nil {
		t.Fatalf("seed terminal record_state: %v", err)
	}

	classifier := successClassifier()
	executor := &fakeExecutor{resp: &executorv1.ExecuteResponse{Outcome: commonv1.Outcome_OUTCOME_SUCCESS}}
	e := New(pool, classifier, executor, clock.New(), 2*time.Second)

	msg := rawEventMessage(t, RawEvent{RecordID: recordID, BatchID: batchID, Type: "RECORD_TYPE_PAYMENT", AmountPaise: 10000, FailureCode: "BANK_TIMEOUT"})
	if err := e.HandleMessage(ctx, msg); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	if classifier.calls != 0 {
		t.Errorf("classifier called %d times for an already-terminal record, want 0", classifier.calls)
	}
	if executor.calls != 0 {
		t.Errorf("executor called %d times for an already-terminal record, want 0", executor.calls)
	}

	var entryCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_entry WHERE record_id=$1`, recordID).Scan(&entryCount); err != nil {
		t.Fatalf("query audit_entry: %v", err)
	}
	if entryCount != 0 {
		t.Errorf("audit_entry rows = %d, want 0 (no new entries for a skipped redelivery)", entryCount)
	}
}

func TestHandleMessagePropagatesClassifierError(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	batchID, recordID := seedRecord(ctx, t, pool)

	classifier := &fakeClassifier{err: errors.New("classifier unavailable")}
	executor := &fakeExecutor{}
	e := New(pool, classifier, executor, clock.New(), 2*time.Second)

	msg := rawEventMessage(t, RawEvent{RecordID: recordID, BatchID: batchID, Type: "RECORD_TYPE_PAYMENT", AmountPaise: 10000, FailureCode: "BANK_TIMEOUT"})
	if err := e.HandleMessage(ctx, msg); err == nil {
		t.Fatal("expected an error when the classifier call fails")
	}
	if executor.calls != 0 {
		t.Errorf("executor called %d times despite classifier failure, want 0", executor.calls)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM record_state WHERE record_id=$1`, recordID).Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Error("record_state was written despite the classify failure")
	}
}

func TestHandleMessageRejectsMalformedPayload(t *testing.T) {
	pool := testPool(t)
	e := New(pool, &fakeClassifier{}, &fakeExecutor{}, clock.New(), 2*time.Second)

	msg := kafkax.Message{Topic: "raw.events", Key: "bad", Value: []byte("not json")}
	if err := e.HandleMessage(context.Background(), msg); err == nil {
		t.Fatal("expected an error for a malformed raw event payload")
	}
}
