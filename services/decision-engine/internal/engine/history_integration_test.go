//go:build integration

// Phase 3 Unit F: ClassifyRequest.history and instrument_history were empty
// since Phase 1 (services/classifier/SPEC.md section 3), so a model rung
// received exactly the two inputs the rules table receives. These tests
// prove the Decision Engine now fills both from INTERVENTION_ATTEMPT,
// against real Postgres, because the join and ordering semantics ARE the
// behaviour (docs/ENGINEERING.md section 1, do not mock what you own).
package engine

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thisizaro/Momotaro/internal/platform/clock"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// seedRecordWithInstrument is seedRecord plus a non-NULL instrument_ref;
// seedRecord itself always leaves the column NULL, and the instrument_history
// tests below need real records that share one.
func seedRecordWithInstrument(ctx context.Context, t *testing.T, pool *pgxpkg.Pool, instrumentRef string) (batchID, recordID string) {
	t.Helper()
	batchID = uuid.NewString()
	recordID = uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO batch (id, source) VALUES ($1, 'test')`, batchID); err != nil {
		t.Fatalf("seed batch: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO record (id, batch_id, type, amount_paise, failure_code, instrument_ref)
		VALUES ($1, $2, 'RECORD_TYPE_PAYMENT', 10000, 'BANK_TIMEOUT', $3)`,
		recordID, batchID, instrumentRef); err != nil {
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

// seedAttemptOutcome is seedAttempt (guardrails_integration_test.go) with a
// configurable outcome; that helper hardcodes OUTCOME_FAILURE, but the
// history rows tests below need to tell attempts apart by outcome too.
func seedAttemptOutcome(ctx context.Context, t *testing.T, pool *pgxpkg.Pool, recordID string, n int, action commonv1.ActionType, outcome commonv1.Outcome, executedAt time.Time) {
	t.Helper()
	id := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO intervention_attempt (id, record_id, attempt_number, action_type, outcome, executed_at, cost_paise)
		VALUES ($1, $2, $3, $4, $5, $6, 0)`,
		id, recordID, n, action.String(), outcome.String(), executedAt,
	); err != nil {
		t.Fatalf("seed intervention_attempt: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM intervention_attempt WHERE id = $1`, id)
	})
}

func TestLoadAttemptRowsEmptyForFreshRecord(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	_, recordID := seedRecord(ctx, t, pool)

	rows, err := newStore(pool).loadAttemptRows(ctx, recordID)
	if err != nil {
		t.Fatalf("loadAttemptRows: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("rows = %d, want 0 for a record with no attempts", len(rows))
	}
}

func TestLoadAttemptRowsOrdersOldestFirstWithFields(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	_, recordID := seedRecord(ctx, t, pool)
	base := time.Now().Add(-time.Hour).Truncate(time.Millisecond)

	seedAttemptOutcome(ctx, t, pool, recordID, 1, commonv1.ActionType_ACTION_TYPE_RETRY, commonv1.Outcome_OUTCOME_FAILURE, base)
	seedAttemptOutcome(ctx, t, pool, recordID, 2, commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, commonv1.Outcome_OUTCOME_SUCCESS, base.Add(10*time.Minute))

	rows, err := newStore(pool).loadAttemptRows(ctx, recordID)
	if err != nil {
		t.Fatalf("loadAttemptRows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0].GetActionType() != commonv1.ActionType_ACTION_TYPE_RETRY || rows[0].GetOutcome() != commonv1.Outcome_OUTCOME_FAILURE {
		t.Errorf("rows[0] = %v/%v, want RETRY/FAILURE (oldest first)", rows[0].GetActionType(), rows[0].GetOutcome())
	}
	if rows[1].GetActionType() != commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER || rows[1].GetOutcome() != commonv1.Outcome_OUTCOME_SUCCESS {
		t.Errorf("rows[1] = %v/%v, want NUDGE_REMINDER/SUCCESS (second, i.e. newest last)", rows[1].GetActionType(), rows[1].GetOutcome())
	}
	if !rows[0].GetExecutedAt().AsTime().Before(rows[1].GetExecutedAt().AsTime()) {
		t.Errorf("rows not oldest first: %v then %v", rows[0].GetExecutedAt().AsTime(), rows[1].GetExecutedAt().AsTime())
	}

	// Proves the count assertion above can actually fail: seed a third
	// attempt and confirm the count moves off the old value.
	seedAttemptOutcome(ctx, t, pool, recordID, 3, commonv1.ActionType_ACTION_TYPE_RETRY, commonv1.Outcome_OUTCOME_FAILURE, base.Add(20*time.Minute))
	rows, err = newStore(pool).loadAttemptRows(ctx, recordID)
	if err != nil {
		t.Fatalf("loadAttemptRows after third attempt: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("rows = %d after seeding a third attempt, want 3", len(rows))
	}
}

func TestLoadInstrumentHistoryExcludesOwnRecordAndCarriesOthers(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	instrument := "instr-" + uuid.NewString()

	_, target := seedRecordWithInstrument(ctx, t, pool, instrument)
	_, other1 := seedRecordWithInstrument(ctx, t, pool, instrument)
	_, other2 := seedRecordWithInstrument(ctx, t, pool, instrument)
	base := time.Now().Add(-time.Hour).Truncate(time.Millisecond)

	// Target's own rows must never appear in its instrument_history: they are
	// already in `history`, and duplicating them tells the model the same
	// fact twice with more weight (Phase 3 Unit F).
	seedAttemptOutcome(ctx, t, pool, target, 1, commonv1.ActionType_ACTION_TYPE_RETRY, commonv1.Outcome_OUTCOME_FAILURE, base)
	seedAttemptOutcome(ctx, t, pool, other1, 1, commonv1.ActionType_ACTION_TYPE_RETRY, commonv1.Outcome_OUTCOME_FAILURE, base.Add(5*time.Minute))
	seedAttemptOutcome(ctx, t, pool, other2, 1, commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE, commonv1.Outcome_OUTCOME_SUCCESS, base.Add(10*time.Minute))

	rows, err := newStore(pool).loadInstrumentHistory(ctx, instrument, target)
	if err != nil {
		t.Fatalf("loadInstrumentHistory: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (other1 and other2, target's own excluded)", len(rows))
	}
	// Most recent first: other2 (10m) before other1 (5m).
	if rows[0].GetActionType() != commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE {
		t.Errorf("rows[0] action = %v, want NUDGE_METHOD_UPDATE (most recent first)", rows[0].GetActionType())
	}
	if rows[1].GetActionType() != commonv1.ActionType_ACTION_TYPE_RETRY {
		t.Errorf("rows[1] action = %v, want RETRY", rows[1].GetActionType())
	}
}

func TestLoadInstrumentHistoryCapped(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	instrument := "instr-" + uuid.NewString()

	_, target := seedRecordWithInstrument(ctx, t, pool, instrument)
	base := time.Now().Add(-time.Hour).Truncate(time.Millisecond)

	// One more than the cap, spread across distinct other records, so a
	// popular instrument cannot make this query (or the resulting prompt)
	// grow without bound.
	for i := 0; i < instrumentHistoryLimit+1; i++ {
		_, other := seedRecordWithInstrument(ctx, t, pool, instrument)
		seedAttemptOutcome(ctx, t, pool, other, 1, commonv1.ActionType_ACTION_TYPE_RETRY, commonv1.Outcome_OUTCOME_FAILURE, base.Add(time.Duration(i)*time.Minute))
	}

	rows, err := newStore(pool).loadInstrumentHistory(ctx, instrument, target)
	if err != nil {
		t.Fatalf("loadInstrumentHistory: %v", err)
	}
	if len(rows) != instrumentHistoryLimit {
		t.Errorf("rows = %d, want exactly %d (the cap), even though %d attempts exist", len(rows), instrumentHistoryLimit, instrumentHistoryLimit+1)
	}
}

// TestHandleMessageSendsHistoryAndInstrumentHistoryToClassifier is the
// end-to-end proof: HandleMessage's ClassifyRequest actually carries what
// the two queries above return, not just that the queries work in
// isolation.
func TestHandleMessageSendsHistoryAndInstrumentHistoryToClassifier(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	dlqProducer, dlqTopic, auditTopic := testProducer(t)
	instrument := "instr-" + uuid.NewString()
	base := time.Now().Add(-time.Hour).Truncate(time.Millisecond)

	batchID, recordID := seedRecord(ctx, t, pool)
	seedAttemptOutcome(ctx, t, pool, recordID, 1, commonv1.ActionType_ACTION_TYPE_RETRY, commonv1.Outcome_OUTCOME_FAILURE, base)
	seedAttemptOutcome(ctx, t, pool, recordID, 2, commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, commonv1.Outcome_OUTCOME_SUCCESS, base.Add(10*time.Minute))

	_, otherRecordID := seedRecordWithInstrument(ctx, t, pool, instrument)
	seedAttemptOutcome(ctx, t, pool, otherRecordID, 1, commonv1.ActionType_ACTION_TYPE_RETRY, commonv1.Outcome_OUTCOME_FAILURE, base.Add(5*time.Minute))

	classifier := &fakeClassifier{resp: classifyResponseWithAction(commonv1.ActionType_ACTION_TYPE_ESCALATE)}
	e := New(pool, classifier, &fakeExecutor{}, dlqProducer, clock.New(), testEconomics(t), testConfig(dlqTopic, auditTopic))

	msg := rawEventMessage(t, RawEvent{RecordID: recordID, BatchID: batchID, Type: "RECORD_TYPE_PAYMENT", AmountPaise: 10000, FailureCode: "RISK_HOLD", InstrumentRef: instrument})
	if err := e.HandleMessage(ctx, msg); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	req := classifier.lastRequest()
	if req == nil {
		t.Fatal("classifier never received a ClassifyRequest")
	}
	if len(req.GetHistory()) != 2 {
		t.Fatalf("History = %d entries, want 2 (this record's own prior attempts)", len(req.GetHistory()))
	}
	if req.GetHistory()[0].GetActionType() != commonv1.ActionType_ACTION_TYPE_RETRY {
		t.Errorf("History[0] action = %v, want RETRY (oldest first)", req.GetHistory()[0].GetActionType())
	}
	if req.GetHistory()[1].GetActionType() != commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER {
		t.Errorf("History[1] action = %v, want NUDGE_REMINDER", req.GetHistory()[1].GetActionType())
	}
	if len(req.GetInstrumentHistory()) != 1 {
		t.Fatalf("InstrumentHistory = %d entries, want 1 (the other record sharing the instrument)", len(req.GetInstrumentHistory()))
	}
	for _, a := range req.GetHistory() {
		for _, ih := range req.GetInstrumentHistory() {
			if a == ih {
				t.Error("this record's own attempt also appears in InstrumentHistory, want it excluded")
			}
		}
	}
}

// TestHandleMessageSendsNoInstrumentHistoryWhenInstrumentRefEmpty covers the
// nullable case: instrument_ref empty means skip the second query entirely,
// not query for the empty string.
func TestHandleMessageSendsNoInstrumentHistoryWhenInstrumentRefEmpty(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	dlqProducer, dlqTopic, auditTopic := testProducer(t)
	batchID, recordID := seedRecord(ctx, t, pool)

	classifier := &fakeClassifier{resp: classifyResponseWithAction(commonv1.ActionType_ACTION_TYPE_ESCALATE)}
	e := New(pool, classifier, &fakeExecutor{}, dlqProducer, clock.New(), testEconomics(t), testConfig(dlqTopic, auditTopic))

	msg := rawEventMessage(t, RawEvent{RecordID: recordID, BatchID: batchID, Type: "RECORD_TYPE_PAYMENT", AmountPaise: 10000, FailureCode: "RISK_HOLD"})
	if err := e.HandleMessage(ctx, msg); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	req := classifier.lastRequest()
	if req == nil {
		t.Fatal("classifier never received a ClassifyRequest")
	}
	if len(req.GetInstrumentHistory()) != 0 {
		t.Errorf("InstrumentHistory = %d entries, want 0 when instrument_ref is empty", len(req.GetInstrumentHistory()))
	}
}
