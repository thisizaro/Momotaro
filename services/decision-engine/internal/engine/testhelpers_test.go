//go:build integration

// Shared setup for every test in this package that talks to real Postgres
// and Kafka (docs/ENGINEERING.md section 1: "do not mock what you own").

package engine

import (
	"path/filepath"
	"runtime"

	"context"
	"encoding/json"
	"errors"
	"github.com/thisizaro/Momotaro/services/decision-engine/internal/economics"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
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

func brokers(t *testing.T) []string {
	t.Helper()
	if v := os.Getenv("KAFKA_BROKERS"); v != "" {
		return strings.Split(v, ",")
	}
	return []string{"localhost:9092"}
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

func uniqueTopic(t *testing.T) string {
	t.Helper()
	return "decision-engine-test-" + strings.ReplaceAll(uuid.NewString(), "-", "")
}

func testDLQ(t *testing.T) (*kafkax.Producer, string) {
	t.Helper()
	topic := uniqueTopic(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := kafkax.EnsureTopic(ctx, brokers(t), topic, 1); err != nil {
		t.Fatalf("EnsureTopic: %v", err)
	}
	p, err := kafkax.NewProducer(brokers(t))
	if err != nil {
		t.Fatalf("NewProducer: %v", err)
	}
	t.Cleanup(p.Close)
	return p, topic
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

// waitForDeadLetter consumes topic from the start and returns the first
// DeadLetter whose RecordID (or, for an unparseable payload, RawValue)
// matches want, or fails the test after timeout.
func waitForDeadLetter(t *testing.T, topic, want string, timeout time.Duration) DeadLetter {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	consumer, err := kafkax.NewConsumer(brokers(t), "decision-engine-dlq-test-"+uuid.NewString(), []string{topic})
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	defer consumer.Close()

	found := make(chan DeadLetter, 1)
	go func() {
		_ = consumer.Consume(ctx, func(ctx context.Context, m kafkax.Message) error {
			var dl DeadLetter
			if err := json.Unmarshal(m.Value, &dl); err == nil && (dl.RecordID == want || dl.RawValue == want) {
				found <- dl
			}
			return nil
		})
	}()

	select {
	case dl := <-found:
		return dl
	case <-ctx.Done():
		t.Fatalf("timed out waiting for a dead letter matching %q on %s", want, topic)
		return DeadLetter{}
	}
}

type fakeClassifier struct {
	resp  *classifierv1.ClassifyResponse
	err   error
	failN int32 // if err is set: fail exactly this many calls before succeeding; 0 means fail every call
	calls int32
}

func (f *fakeClassifier) Classify(ctx context.Context, in *classifierv1.ClassifyRequest, opts ...grpc.CallOption) (*classifierv1.ClassifyResponse, error) {
	n := atomic.AddInt32(&f.calls, 1)
	if f.err != nil && (f.failN == 0 || n <= f.failN) {
		return nil, f.err
	}
	return f.resp, nil
}
func (f *fakeClassifier) ComposeNudge(ctx context.Context, in *classifierv1.ComposeNudgeRequest, opts ...grpc.CallOption) (*classifierv1.ComposeNudgeResponse, error) {
	return nil, errors.New("not used in Phase 1")
}

type fakeExecutor struct {
	resp  *executorv1.ExecuteResponse
	err   error
	failN int32
	calls int32
}

func (f *fakeExecutor) Execute(ctx context.Context, in *executorv1.ExecuteRequest, opts ...grpc.CallOption) (*executorv1.ExecuteResponse, error) {
	n := atomic.AddInt32(&f.calls, 1)
	if f.err != nil && (f.failN == 0 || n <= f.failN) {
		return nil, f.err
	}
	return f.resp, nil
}

func retryClassifier() *fakeClassifier {
	return &fakeClassifier{resp: classifyResponseWithAction(commonv1.ActionType_ACTION_TYPE_RETRY)}
}

// classifyResponseWithAction builds an otherwise-valid ClassifyResponse
// recommending action, for tests that only care about the resulting state
// transition.
func classifyResponseWithAction(action commonv1.ActionType) *classifierv1.ClassifyResponse {
	return classifyResponseFor(commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, action)
}

// classifyResponseFor builds a classification with an explicit bucket. The
// bucket matters more than the recommendation now: the scorer prices actions
// from the prior table, which is keyed on it, so a test that wants a specific
// action chosen has to put the record in a bucket where that action actually
// wins on expected value.
func classifyResponseFor(bucket commonv1.RootCauseBucket, action commonv1.ActionType) *classifierv1.ClassifyResponse {
	return &classifierv1.ClassifyResponse{
		Bucket:            bucket,
		RecommendedAction: action,
		Rationale:         "test rationale",
		Confidence:        1,
		Source:            commonv1.Source_SOURCE_RULES_FALLBACK,
	}
}

// testEconomics loads the real checked-in cost model and priors rather than a
// fixture. The scorer's job is to price actions using those exact numbers, so
// a fixture here would test the arithmetic while leaving the thing that
// actually decides in production unexercised.
func testEconomics(t *testing.T) *economics.Model {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..", "..", "..")
	m, err := economics.Load(filepath.Join(root, "configs", "intervention_costs.yaml"), filepath.Join(root, "configs", "recovery_priors.yaml"))
	if err != nil {
		t.Fatalf("load economics config: %v", err)
	}
	return m
}
