//go:build e2e

// Package e2e holds the walking-skeleton integration test: one record,
// through the real api-gateway, ingestion, decision-engine, classifier,
// executor and audit binaries, reaching RECORD_STATE_RECOVERED with a
// complete audit trail (docs/PLAN.md Phase 0, "Walking skeleton").
//
// This does not import any service's internal package: doing so would be a
// compile error by construction (docs/ARCHITECTURE.md section 2a, Go's
// internal/ rule). Instead it builds and runs the real service binaries as
// subprocesses against the docker-compose infra (Postgres, Kafka), talks to
// them exactly as a real client would (HTTP to the Gateway, gRPC to Audit),
// and reads Postgres directly to correlate the record.
//
// Requires the docker-compose stack (`make up`) and migrations applied
// (`make migrate-up`) to be running already. Not part of `go test ./...`;
// run explicitly with `go test -tags e2e ./test/e2e/...` (see AGENTS.md
// "Testing conventions": this is the heavy test that runs on merge to main,
// not on every PR).
package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thisizaro/Momotaro/internal/platform/kafkax"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	auditv1 "github.com/thisizaro/Momotaro/proto/gen/audit/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	apiKey        = "e2e-test-key"
	postgresDSN   = "postgres://momotaro:momotaro@localhost:5432/momotaro?sslmode=disable"
	kafkaBrokers  = "localhost:9092"
	redisAddr     = "localhost:6379"
	startupWindow = 20 * time.Second
	pipelineWait  = 30 * time.Second
)

// repoRoot locates the module root from this test file's own path, so the
// test works regardless of the working directory `go test` was invoked
// from.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// test/e2e/walking_skeleton_test.go -> repo root is two levels up.
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// buildBinary compiles services/<name>/cmd into dir and returns its path.
func buildBinary(t *testing.T, root, dir, name string) string {
	t.Helper()
	out := filepath.Join(dir, name)
	cmd := exec.Command("go", "build", "-o", out, "./services/"+name+"/cmd")
	cmd.Dir = root
	cmd.Env = os.Environ()
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", name, err, output)
	}
	return out
}

// freePort asks the OS for an unused TCP port, then releases it. There is a
// small unavoidable race until the child process binds it, standard for
// this kind of test.
func freePort(t *testing.T) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate free port: %v", err)
	}
	defer lis.Close()
	return lis.Addr().(*net.TCPAddr).Port
}

// process wraps a running service subprocess.
type process struct {
	name string
	cmd  *exec.Cmd
}

// startProcess launches bin with env (merged over a minimal base
// environment) and streams its output to the test log, prefixed by name.
func startProcess(t *testing.T, name, bin string, env map[string]string) *process {
	t.Helper()

	cmd := exec.Command(bin)
	envList := []string{"PATH=" + os.Getenv("PATH")}
	for k, v := range env {
		envList = append(envList, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.Env = envList

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe for %s: %v", name, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("stderr pipe for %s: %v", name, err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", name, err)
	}

	logLines := func(r io.Reader) {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			t.Logf("[%s] %s", name, scanner.Text())
		}
	}
	go logLines(stdout)
	go logLines(stderr)

	return &process{name: name, cmd: cmd}
}

// stop sends SIGTERM (exercising every service's graceful-shutdown path,
// docs/ENGINEERING.md section 6) and waits briefly before escalating.
func (p *process) stop(t *testing.T) {
	t.Helper()
	if p.cmd.Process == nil {
		return
	}
	_ = p.cmd.Process.Signal(os.Interrupt)

	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Logf("%s did not exit after SIGTERM, killing", p.name)
		_ = p.cmd.Process.Kill()
		<-done
	}
}

// waitForTCP polls addr until it accepts a connection or ctx is done.
func waitForTCP(ctx context.Context, addr string) error {
	for {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for %s: %w", addr, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func commonEnv(grpcPort, metricsPort int) map[string]string {
	return map[string]string{
		"ENV":             "ci",
		"LOG_LEVEL":       "info",
		"GRPC_PORT":       strconv.Itoa(grpcPort),
		"METRICS_PORT":    strconv.Itoa(metricsPort),
		"POSTGRES_DSN":    postgresDSN,
		"REDIS_ADDR":      redisAddr,
		"KAFKA_BROKERS":   kafkaBrokers,
		"DEMO_TIME_SCALE": "1",
	}
}

func merge(base map[string]string, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func TestWalkingSkeletonReachesRecovered(t *testing.T) {
	root := repoRoot(t)
	binDir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Isolated topic + consumer group: earlier unit test runs have already
	// published unrelated messages onto the shared raw.events topic for
	// records that no longer exist in Postgres. A fresh decision-engine
	// consumer group on that topic would replay them and fail on the very
	// first one (record_state's foreign key to a deleted record), before
	// ever reaching this test's message. See docs/INCIDENTS.md.
	topic := "e2e-raw-events-" + uuid.NewString()
	group := "e2e-decision-engine-" + uuid.NewString()
	if err := kafkax.EnsureTopic(ctx, []string{kafkaBrokers}, topic, 1); err != nil {
		t.Fatalf("EnsureTopic: %v", err)
	}

	classifierBin := buildBinary(t, root, binDir, "classifier")
	executorBin := buildBinary(t, root, binDir, "executor")
	auditBin := buildBinary(t, root, binDir, "audit")
	ingestionBin := buildBinary(t, root, binDir, "ingestion")
	decisionEngineBin := buildBinary(t, root, binDir, "decision-engine")
	apiGatewayBin := buildBinary(t, root, binDir, "api-gateway")

	classifierPort, classifierMetrics := freePort(t), freePort(t)
	executorPort, executorMetrics := freePort(t), freePort(t)
	auditPort, auditMetrics := freePort(t), freePort(t)
	ingestionPort, ingestionMetrics := freePort(t), freePort(t)
	deGRPCPort, deMetrics := freePort(t), freePort(t) // unused but required config
	gwPort, gwMetrics := freePort(t), freePort(t)
	gwHTTPPort := freePort(t)

	var procs []*process
	stopAll := func() {
		for i := len(procs) - 1; i >= 0; i-- {
			procs[i].stop(t)
		}
	}
	defer stopAll()

	classifierAddr := fmt.Sprintf("127.0.0.1:%d", classifierPort)
	executorAddr := fmt.Sprintf("127.0.0.1:%d", executorPort)
	auditAddr := fmt.Sprintf("127.0.0.1:%d", auditPort)
	ingestionAddr := fmt.Sprintf("127.0.0.1:%d", ingestionPort)
	gwHTTPAddr := fmt.Sprintf("127.0.0.1:%d", gwHTTPPort)

	procs = append(procs, startProcess(t, "classifier", classifierBin, commonEnv(classifierPort, classifierMetrics)))
	procs = append(procs, startProcess(t, "executor", executorBin, commonEnv(executorPort, executorMetrics)))
	procs = append(procs, startProcess(t, "audit", auditBin, commonEnv(auditPort, auditMetrics)))
	procs = append(procs, startProcess(t, "ingestion", ingestionBin, merge(commonEnv(ingestionPort, ingestionMetrics), map[string]string{
		"RAW_EVENTS_TOPIC": topic,
	})))

	readyCtx, readyCancel := context.WithTimeout(ctx, startupWindow)
	defer readyCancel()
	for _, addr := range []string{classifierAddr, executorAddr, auditAddr, ingestionAddr} {
		if err := waitForTCP(readyCtx, addr); err != nil {
			t.Fatalf("service did not become ready: %v", err)
		}
	}

	procs = append(procs, startProcess(t, "decision-engine", decisionEngineBin, merge(commonEnv(deGRPCPort, deMetrics), map[string]string{
		"CLASSIFIER_ADDR":           classifierAddr,
		"EXECUTOR_ADDR":             executorAddr,
		"CALL_TIMEOUT":              "5s",
		"RAW_EVENTS_TOPIC":          topic,
		"RAW_EVENTS_CONSUMER_GROUP": group,
	})))

	procs = append(procs, startProcess(t, "api-gateway", apiGatewayBin, merge(commonEnv(gwPort, gwMetrics), map[string]string{
		"INGESTION_ADDR": ingestionAddr,
		"API_KEY":        apiKey,
		"HTTP_PORT":      strconv.Itoa(gwHTTPPort),
		"CALL_TIMEOUT":   "5s",
	})))

	if err := waitForTCP(readyCtx, gwHTTPAddr); err != nil {
		t.Fatalf("api-gateway did not become ready: %v", err)
	}

	// --- Act: submit one batch of one record through the real HTTP contract. ---
	reqBody := `{
		"source": "e2e-test",
		"records": [
			{"type": "PAYMENT", "amount_paise": 75000, "currency": "INR", "failure_code": "BANK_TIMEOUT", "instrument_ref": "card_e2e"}
		]
	}`
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+gwHTTPAddr+"/v1/batches", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-API-Key", apiKey)

	httpResp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("POST /v1/batches: %v", err)
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(httpResp.Body)
		t.Fatalf("POST /v1/batches status = %d, body: %s", httpResp.StatusCode, body)
	}

	var submitResp struct {
		BatchID       string `json:"batch_id"`
		AcceptedCount int32  `json:"accepted_count"`
	}
	if err := json.NewDecoder(httpResp.Body).Decode(&submitResp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if submitResp.BatchID == "" {
		t.Fatal("batch_id is empty")
	}
	if submitResp.AcceptedCount != 1 {
		t.Fatalf("accepted_count = %d, want 1", submitResp.AcceptedCount)
	}
	t.Logf("submitted batch %s", submitResp.BatchID)

	// --- Assert: the record reaches RECOVERED, with a complete audit trail. ---
	poolCtx, poolCancel := context.WithTimeout(ctx, 5*time.Second)
	pool, err := pgxpkg.NewPool(poolCtx, postgresDSN)
	poolCancel()
	if err != nil {
		t.Fatalf("connect to postgres: %v", err)
	}
	defer pool.Close()

	var recordID string
	if err := pool.QueryRow(ctx, `SELECT id FROM record WHERE batch_id = $1`, submitResp.BatchID).Scan(&recordID); err != nil {
		t.Fatalf("find ingested record: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM audit_entry WHERE batch_id = $1`, submitResp.BatchID)
		_, _ = pool.Exec(bg, `DELETE FROM intervention_attempt WHERE record_id = $1`, recordID)
		_, _ = pool.Exec(bg, `DELETE FROM record_state WHERE record_id = $1`, recordID)
		_, _ = pool.Exec(bg, `DELETE FROM record WHERE id = $1`, recordID)
		_, _ = pool.Exec(bg, `DELETE FROM batch WHERE id = $1`, submitResp.BatchID)
	})

	deadline := time.Now().Add(pipelineWait)
	var state string
	for {
		err := pool.QueryRow(ctx, `SELECT current_state FROM record_state WHERE record_id = $1`, recordID).Scan(&state)
		if err == nil && state == commonv1.RecordState_RECORD_STATE_RECOVERED.String() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("record %s did not reach RECOVERED within %s (last state: %q, err: %v)", recordID, pipelineWait, state, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Logf("record %s reached RECOVERED", recordID)

	// The Executor's own table: proof the action actually ran, exactly once,
	// through the insert-before-execute idempotency guard.
	var attemptOutcome string
	var attemptCount int
	if err := pool.QueryRow(ctx, `SELECT count(*), max(outcome) FROM intervention_attempt WHERE record_id = $1`, recordID).
		Scan(&attemptCount, &attemptOutcome); err != nil {
		t.Fatalf("query intervention_attempt: %v", err)
	}
	if attemptCount != 1 {
		t.Errorf("intervention_attempt rows = %d, want 1", attemptCount)
	}
	if attemptOutcome != commonv1.Outcome_OUTCOME_SUCCESS.String() {
		t.Errorf("intervention_attempt outcome = %q, want SUCCESS", attemptOutcome)
	}

	// The Audit Service's own gRPC contract: query it live rather than only
	// checking Postgres, so this test actually exercises GetRecordAudit.
	auditConn, err := grpc.NewClient(auditAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial audit: %v", err)
	}
	defer auditConn.Close()
	auditClient := auditv1.NewAuditServiceClient(auditConn)

	auditCtx, auditCancel := context.WithTimeout(ctx, 5*time.Second)
	defer auditCancel()
	auditResp, err := auditClient.GetRecordAudit(auditCtx, &auditv1.GetRecordAuditRequest{RecordId: recordID})
	if err != nil {
		t.Fatalf("GetRecordAudit: %v", err)
	}

	if auditResp.GetCurrentState() != commonv1.RecordState_RECORD_STATE_RECOVERED {
		t.Errorf("audit CurrentState = %v, want RECOVERED", auditResp.GetCurrentState())
	}
	if !auditResp.GetTrailComplete() {
		t.Error("audit TrailComplete = false")
	}
	entries := auditResp.GetEntries()
	if len(entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.GetToState() != commonv1.RecordState_RECORD_STATE_RECOVERED {
		t.Errorf("audit entry ToState = %v, want RECOVERED", entry.GetToState())
	}
	if entry.GetSource() != commonv1.Source_SOURCE_RULES_FALLBACK {
		t.Errorf("audit entry Source = %v, want SOURCE_RULES_FALLBACK", entry.GetSource())
	}
	if entry.GetRationale() == "" {
		t.Error("audit entry Rationale is empty")
	}
	if entry.GetAttemptNumber() != 1 {
		t.Errorf("audit entry AttemptNumber = %d, want 1", entry.GetAttemptNumber())
	}
	if auditResp.GetRecord().GetAmountPaise() != 75000 {
		t.Errorf("audit Record.AmountPaise = %d, want 75000", auditResp.GetRecord().GetAmountPaise())
	}
}
