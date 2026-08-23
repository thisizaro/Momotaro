//go:build integration

// Decision Engine's tests exercise real Postgres and Kafka rather than a
// mock, per docs/ENGINEERING.md section 1 ("do not mock what you own").
// They therefore need the docker-compose stack up, so they sit behind the
// `integration` build tag. Run with `make test-integration`.

package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/clock"
	"github.com/thisizaro/Momotaro/internal/platform/kafkax"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

func testConfig(dlqTopic string) Config {
	return Config{CallTimeout: 2 * time.Second, RetryDelay: time.Minute, NudgeDelay: time.Minute, DLQTopic: dlqTopic}
}

func TestHandleMessageSchedulesRetry(t *testing.T) {
	pool := testPool(t)
	dlqProducer, dlqTopic := testDLQ(t)
	ctx := context.Background()
	batchID, recordID := seedRecord(ctx, t, pool)

	classifier := retryClassifier()
	e := New(pool, classifier, &fakeExecutor{}, dlqProducer, clock.New(), testConfig(dlqTopic))

	msg := rawEventMessage(t, RawEvent{RecordID: recordID, BatchID: batchID, Type: "RECORD_TYPE_PAYMENT", AmountPaise: 10000, FailureCode: "BANK_TIMEOUT"})
	if err := e.HandleMessage(ctx, msg); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	var state, bucket, pendingAction string
	var attemptCount int
	var dueAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT current_state, attempt_count, root_cause_bucket, pending_action, due_at FROM record_state WHERE record_id=$1`, recordID).
		Scan(&state, &attemptCount, &bucket, &pendingAction, &dueAt); err != nil {
		t.Fatalf("query record_state: %v", err)
	}
	if state != commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED.String() {
		t.Errorf("current_state = %q, want RETRY_SCHEDULED", state)
	}
	if attemptCount != 0 {
		t.Errorf("attempt_count = %d, want 0 (scheduling is not itself an attempt)", attemptCount)
	}
	if pendingAction != commonv1.ActionType_ACTION_TYPE_RETRY.String() {
		t.Errorf("pending_action = %q, want ACTION_TYPE_RETRY", pendingAction)
	}
	if dueAt == nil {
		t.Fatal("due_at is nil, want a future timestamp")
	}
	if !dueAt.After(time.Now().Add(-time.Second)) {
		t.Errorf("due_at = %v, want roughly now + RetryDelay", dueAt)
	}

	var entryCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_entry WHERE record_id=$1`, recordID).Scan(&entryCount); err != nil {
		t.Fatalf("query audit_entry: %v", err)
	}
	if entryCount != 1 {
		t.Errorf("audit_entry rows = %d, want 1", entryCount)
	}
	if classifier.calls != 1 {
		t.Errorf("classifier called %d times, want 1", classifier.calls)
	}
}

func TestHandleMessageSchedulesNudge(t *testing.T) {
	pool := testPool(t)
	dlqProducer, dlqTopic := testDLQ(t)
	ctx := context.Background()
	batchID, recordID := seedRecord(ctx, t, pool)

	classifier := &fakeClassifier{resp: classifyResponseWithAction(commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER)}
	e := New(pool, classifier, &fakeExecutor{}, dlqProducer, clock.New(), testConfig(dlqTopic))

	msg := rawEventMessage(t, RawEvent{RecordID: recordID, BatchID: batchID, Type: "RECORD_TYPE_CHECKOUT", AmountPaise: 5000, FailureCode: "ABANDONED"})
	if err := e.HandleMessage(ctx, msg); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	var state, pendingAction string
	if err := pool.QueryRow(ctx, `SELECT current_state, pending_action FROM record_state WHERE record_id=$1`, recordID).Scan(&state, &pendingAction); err != nil {
		t.Fatalf("query record_state: %v", err)
	}
	if state != commonv1.RecordState_RECORD_STATE_NUDGE_SCHEDULED.String() {
		t.Errorf("current_state = %q, want NUDGE_SCHEDULED", state)
	}
	if pendingAction != commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER.String() {
		t.Errorf("pending_action = %q, want ACTION_TYPE_NUDGE_REMINDER", pendingAction)
	}
}

func TestHandleMessageEscalatesOnExplicitEscalateAction(t *testing.T) {
	pool := testPool(t)
	dlqProducer, dlqTopic := testDLQ(t)
	ctx := context.Background()
	batchID, recordID := seedRecord(ctx, t, pool)

	classifier := &fakeClassifier{resp: classifyResponseWithAction(commonv1.ActionType_ACTION_TYPE_ESCALATE)}
	e := New(pool, classifier, &fakeExecutor{}, dlqProducer, clock.New(), testConfig(dlqTopic))

	msg := rawEventMessage(t, RawEvent{RecordID: recordID, BatchID: batchID, Type: "RECORD_TYPE_PAYMENT", AmountPaise: 10000, FailureCode: "RISK_HOLD"})
	if err := e.HandleMessage(ctx, msg); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	var state string
	var pendingAction *string
	var dueAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT current_state, pending_action, due_at FROM record_state WHERE record_id=$1`, recordID).Scan(&state, &pendingAction, &dueAt); err != nil {
		t.Fatalf("query record_state: %v", err)
	}
	if state != commonv1.RecordState_RECORD_STATE_ESCALATED.String() {
		t.Errorf("current_state = %q, want ESCALATED", state)
	}
	if pendingAction != nil {
		t.Errorf("pending_action = %q, want NULL for an escalated record", *pendingAction)
	}
	if dueAt != nil {
		t.Errorf("due_at = %v, want NULL for a terminal record", *dueAt)
	}
}

// A record already in RECORD_STATE, in any state, means this raw.events
// message is a redelivery: at-least-once delivery plus a crash before
// offset commit. Reprocessing it would double the audit trail (or, worse,
// violate record_state's primary key).
func TestHandleMessageSkipsRecordAlreadyHavingState(t *testing.T) {
	pool := testPool(t)
	dlqProducer, dlqTopic := testDLQ(t)
	ctx := context.Background()
	batchID, recordID := seedRecord(ctx, t, pool)

	if _, err := pool.Exec(ctx, `INSERT INTO record_state (record_id, current_state, attempt_count) VALUES ($1, $2, 0)`,
		recordID, commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED.String()); err != nil {
		t.Fatalf("seed record_state: %v", err)
	}

	classifier := retryClassifier()
	e := New(pool, classifier, &fakeExecutor{}, dlqProducer, clock.New(), testConfig(dlqTopic))

	msg := rawEventMessage(t, RawEvent{RecordID: recordID, BatchID: batchID, Type: "RECORD_TYPE_PAYMENT", AmountPaise: 10000, FailureCode: "BANK_TIMEOUT"})
	if err := e.HandleMessage(ctx, msg); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	if classifier.calls != 0 {
		t.Errorf("classifier called %d times for a redelivered message, want 0", classifier.calls)
	}
	var entryCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_entry WHERE record_id=$1`, recordID).Scan(&entryCount); err != nil {
		t.Fatalf("query audit_entry: %v", err)
	}
	if entryCount != 0 {
		t.Errorf("audit_entry rows = %d, want 0 (no new entries for a skipped redelivery)", entryCount)
	}
}

func TestHandleMessageDeadLettersMalformedPayload(t *testing.T) {
	pool := testPool(t)
	dlqProducer, dlqTopic := testDLQ(t)
	e := New(pool, &fakeClassifier{}, &fakeExecutor{}, dlqProducer, clock.New(), testConfig(dlqTopic))

	raw := "not json"
	msg := kafkax.Message{Topic: "raw.events", Key: "bad-key", Value: []byte(raw)}
	if err := e.HandleMessage(context.Background(), msg); err != nil {
		t.Fatalf("HandleMessage: %v, want nil (malformed payloads are dead-lettered, not returned as an error)", err)
	}

	dl := waitForDeadLetter(t, dlqTopic, raw, 10*time.Second)
	if dl.RawValue != raw {
		t.Errorf("dead letter RawValue = %q, want %q", dl.RawValue, raw)
	}
	if dl.FailureReason == "" {
		t.Error("dead letter FailureReason is empty")
	}
}

func TestHandleMessageDeadLettersAfterClassifyRetriesExhausted(t *testing.T) {
	pool := testPool(t)
	dlqProducer, dlqTopic := testDLQ(t)
	ctx := context.Background()
	batchID, recordID := seedRecord(ctx, t, pool)

	classifier := &fakeClassifier{err: errors.New("classifier unavailable")}
	e := New(pool, classifier, &fakeExecutor{}, dlqProducer, clock.New(), testConfig(dlqTopic))

	msg := rawEventMessage(t, RawEvent{RecordID: recordID, BatchID: batchID, Type: "RECORD_TYPE_PAYMENT", AmountPaise: 10000, FailureCode: "BANK_TIMEOUT"})
	if err := e.HandleMessage(ctx, msg); err != nil {
		t.Fatalf("HandleMessage: %v, want nil (exhausted retries are dead-lettered, not returned as an error)", err)
	}

	if classifier.calls != maxClassifyAttempts {
		t.Errorf("classifier called %d times, want exactly maxClassifyAttempts=%d", classifier.calls, maxClassifyAttempts)
	}

	dl := waitForDeadLetter(t, dlqTopic, recordID, 10*time.Second)
	if dl.RecordID != recordID {
		t.Errorf("dead letter RecordID = %q, want %q", dl.RecordID, recordID)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM record_state WHERE record_id=$1`, recordID).Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Error("record_state was written despite the record being dead-lettered")
	}
}

func TestHandleMessageRetriesTransientClassifyFailureThenSucceeds(t *testing.T) {
	pool := testPool(t)
	dlqProducer, dlqTopic := testDLQ(t)
	ctx := context.Background()
	batchID, recordID := seedRecord(ctx, t, pool)

	classifier := retryClassifier()
	classifier.err = errors.New("transient")
	classifier.failN = maxClassifyAttempts - 1 // fails all but the last attempt

	e := New(pool, classifier, &fakeExecutor{}, dlqProducer, clock.New(), testConfig(dlqTopic))
	msg := rawEventMessage(t, RawEvent{RecordID: recordID, BatchID: batchID, Type: "RECORD_TYPE_PAYMENT", AmountPaise: 10000, FailureCode: "BANK_TIMEOUT"})
	if err := e.HandleMessage(ctx, msg); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	if classifier.calls != maxClassifyAttempts {
		t.Errorf("classifier called %d times, want %d", classifier.calls, maxClassifyAttempts)
	}

	var state string
	if err := pool.QueryRow(ctx, `SELECT current_state FROM record_state WHERE record_id=$1`, recordID).Scan(&state); err != nil {
		t.Fatalf("query record_state: %v", err)
	}
	if state != commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED.String() {
		t.Errorf("current_state = %q, want RETRY_SCHEDULED", state)
	}
}

func TestHandleMessageRejectsMalformedPayloadIsNeverFatal(t *testing.T) {
	pool := testPool(t)
	dlqProducer, dlqTopic := testDLQ(t)
	e := New(pool, &fakeClassifier{}, &fakeExecutor{}, dlqProducer, clock.New(), testConfig(dlqTopic))

	msg := kafkax.Message{Topic: "raw.events", Key: "bad", Value: []byte("{not valid json")}
	if err := e.HandleMessage(context.Background(), msg); err != nil {
		t.Fatalf("HandleMessage: %v, want nil", err)
	}
}
