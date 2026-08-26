//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thisizaro/Momotaro/internal/platform/kafkax"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	executorv1 "github.com/thisizaro/Momotaro/proto/gen/executor/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TestIdempotencyEndToEnd proves the two at-least-once delivery boundaries
// preserve their durable keys against the real Kafka, Postgres, and Executor
// processes. Assertions deliberately read Postgres: an RPC response alone
// cannot prove a duplicate did not create a second money action.
func TestIdempotencyEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	s := startStack(ctx, t, "1s")
	pool := connectPool(ctx, t)

	t.Run("duplicate Kafka raw event creates one state and settles normally", func(t *testing.T) {
		recordID, batchID := seedIdempotencyRecord(ctx, t, pool)
		payload, err := json.Marshal(rawEvent{
			RecordID: recordID, BatchID: batchID, Type: "RECORD_TYPE_PAYMENT",
			AmountPaise: 75000, Currency: "INR", FailureCode: "BANK_TIMEOUT",
			InstrumentRef: "card_idempotency", CreatedAt: time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("marshal raw event: %v", err)
		}

		producer, err := kafkax.NewProducer([]string{kafkaBrokers})
		if err != nil {
			t.Fatalf("new Kafka producer: %v", err)
		}
		t.Cleanup(producer.Close)
		for delivery := 1; delivery <= 2; delivery++ {
			if err := producer.Publish(ctx, s.topic, recordID, payload); err != nil {
				t.Fatalf("publish duplicate delivery %d: %v", delivery, err)
			}
		}

		state := waitForRecordState(ctx, t, pool, recordID)
		if state != commonv1.RecordState_RECORD_STATE_RECOVERED.String() {
			t.Fatalf("state after duplicate Kafka delivery = %q, want %q", state, commonv1.RecordState_RECORD_STATE_RECOVERED.String())
		}
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM record_state WHERE record_id = $1`, recordID).Scan(&count); err != nil {
			t.Fatalf("count record_state rows: %v", err)
		}
		if count != 1 {
			t.Errorf("record_state rows = %d, want exactly 1 after duplicate Kafka delivery", count)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM intervention_attempt WHERE record_id = $1`, recordID).Scan(&count); err != nil {
			t.Fatalf("count intervention_attempt rows: %v", err)
		}
		if count != 1 {
			t.Errorf("intervention_attempt rows = %d, want exactly 1 after duplicate Kafka delivery", count)
		}
	})

	t.Run("duplicate Executor gRPC retry creates one attempt", func(t *testing.T) {
		recordID, _ := seedIdempotencyRecord(ctx, t, pool)
		conn, err := grpc.NewClient(s.executorAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatalf("dial Executor: %v", err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		client := executorv1.NewExecutorServiceClient(conn)
		req := &executorv1.ExecuteRequest{
			RecordId: recordID, ActionType: commonv1.ActionType_ACTION_TYPE_RETRY,
			AttemptNumber: 1, AmountPaise: 10000,
		}

		first, err := client.Execute(ctx, req)
		if err != nil {
			t.Fatalf("first Execute: %v", err)
		}
		if first.GetAlreadyExecuted() {
			t.Fatal("first Execute reported already_executed")
		}
		second, err := client.Execute(ctx, req)
		if err != nil {
			t.Fatalf("second Execute: %v", err)
		}
		if !second.GetAlreadyExecuted() {
			t.Error("second Execute already_executed = false, want true")
		}
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM intervention_attempt WHERE record_id = $1 AND attempt_number = $2`, recordID, req.AttemptNumber).Scan(&count); err != nil {
			t.Fatalf("count intervention_attempt rows: %v", err)
		}
		if count != 1 {
			t.Errorf("intervention_attempt rows = %d, want exactly 1 after duplicate gRPC retry", count)
		}
	})
}

type rawEvent struct {
	RecordID      string    `json:"record_id"`
	BatchID       string    `json:"batch_id"`
	Type          string    `json:"type"`
	AmountPaise   int64     `json:"amount_paise"`
	Currency      string    `json:"currency"`
	FailureCode   string    `json:"failure_code"`
	InstrumentRef string    `json:"instrument_ref"`
	CreatedAt     time.Time `json:"created_at"`
}

func seedIdempotencyRecord(ctx context.Context, t *testing.T, pool *pgxpkg.Pool) (string, string) {
	t.Helper()
	batchID, recordID := uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO batch (id, source) VALUES ($1, 'e2e-idempotency')`, batchID); err != nil {
		t.Fatalf("seed batch: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO record (id, batch_id, type, amount_paise, failure_code) VALUES ($1, $2, 'RECORD_TYPE_PAYMENT', 10000, 'BANK_TIMEOUT')`, recordID, batchID); err != nil {
		t.Fatalf("seed record: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM audit_entry WHERE record_id = $1`, recordID)
		_, _ = pool.Exec(bg, `DELETE FROM intervention_attempt WHERE record_id = $1`, recordID)
		_, _ = pool.Exec(bg, `DELETE FROM record_state WHERE record_id = $1`, recordID)
		_, _ = pool.Exec(bg, `DELETE FROM record WHERE id = $1`, recordID)
		_, _ = pool.Exec(bg, `DELETE FROM batch WHERE id = $1`, batchID)
	})
	return recordID, batchID
}

func waitForRecordState(ctx context.Context, t *testing.T, pool *pgxpkg.Pool, recordID string) string {
	t.Helper()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		var state string
		err := pool.QueryRow(ctx, `SELECT current_state FROM record_state WHERE record_id = $1`, recordID).Scan(&state)
		if err == nil && settledStates[commonv1.RecordState(commonv1.RecordState_value[state])] {
			return state
		}
		select {
		case <-ctx.Done():
			t.Fatalf("record %s did not settle: last state %q, query error: %v", recordID, state, err)
		case <-ticker.C:
		}
	}
}
