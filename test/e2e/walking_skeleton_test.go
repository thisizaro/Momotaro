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
	"sync"
	"testing"
	"time"

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

// buildBinary compiles <pkgDir>/<name>/cmd into dir and returns its path.
// pkgDir is "services" for every long-running service or "demo" for the
// Phase 5 simulators (docs/ARCHITECTURE.md section 2a's repo layout).
func buildBinary(t *testing.T, root, dir, pkgDir, name string) string {
	t.Helper()
	out := filepath.Join(dir, name)
	cmd := exec.Command("go", "build", "-o", out, "./"+pkgDir+"/"+name+"/cmd")
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
// issuedPorts remembers every port freePort has handed out in this process.
//
// Asking the kernel for :0 and immediately closing the listener leaves the
// port genuinely free, so a later call can be given the SAME one. A stack
// needs thirteen ports, and a service that receives its own metrics port for
// gRPC refuses to start with "GRPC_PORT and METRICS_PORT must differ", which
// then surfaces as an unrelated-looking readiness timeout. That collision
// really happened in CI (docs/INCIDENTS.md).
var (
	issuedPortsMu sync.Mutex
	issuedPorts   = map[int]bool{}
)

func freePort(t *testing.T) int {
	t.Helper()

	issuedPortsMu.Lock()
	defer issuedPortsMu.Unlock()

	// Hold every listener until a fresh port appears, so the kernel cannot
	// offer the same one twice within this attempt, then release them all.
	var held []net.Listener
	defer func() {
		for _, lis := range held {
			lis.Close()
		}
	}()

	for attempt := 0; attempt < 64; attempt++ {
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("allocate free port: %v", err)
		}
		held = append(held, lis)

		port := lis.Addr().(*net.TCPAddr).Port
		if !issuedPorts[port] {
			issuedPorts[port] = true
			return port
		}
	}
	t.Fatal("could not find an unused port in 64 attempts")
	return 0
}

// process wraps a running service subprocess. waitOnce guards cmd.Wait(),
// which panics if called twice concurrently -- needed because Unit K's
// restartDecisionEngine (harness_test.go) can kill a process directly while
// t.Cleanup will also call stop() on whatever startStack's procs slice still
// references.
type process struct {
	name    string
	cmd     *exec.Cmd
	wait    sync.Once
	waitErr error
}

// waitOnce calls cmd.Wait() exactly once no matter how many callers ask,
// returning the same result to all of them.
func (p *process) waitOnce() error {
	p.wait.Do(func() { p.waitErr = p.cmd.Wait() })
	return p.waitErr
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
	go func() { done <- p.waitOnce() }()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Logf("%s did not exit after SIGTERM, killing", p.name)
		_ = p.cmd.Process.Kill()
		<-done
	}
}

// kill sends SIGKILL and waits for exit -- a hard crash, not the graceful
// shutdown stop() exercises. Unit K deliberately uses this rather than stop:
// a clean shutdown proves the graceful path is safe, which is not the claim
// the transactional write and contiguous-prefix Kafka commits exist to back
// (docs/PHASE2_IMPLEMENTATION.md Unit K).
func (p *process) kill(t *testing.T) {
	t.Helper()
	if p.cmd.Process == nil {
		return
	}
	_ = p.cmd.Process.Kill()
	_ = p.waitOnce()
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
		"ENV":           "ci",
		"LOG_LEVEL":     "info",
		"GRPC_PORT":     strconv.Itoa(grpcPort),
		"METRICS_PORT":  strconv.Itoa(metricsPort),
		"POSTGRES_DSN":  postgresDSN,
		"REDIS_ADDR":    redisAddr,
		"KAFKA_BROKERS": kafkaBrokers,
		// Must compress real wall-clock waits, not just the fixed
		// RETRY_DELAY/NUDGE_DELAY tests already override to something
		// short. Cause-aware retry timing (schedule.go's retryDueAt,
		// PHASE2_IMPLEMENTATION.md Unit F) can schedule an
		// INSUFFICIENT_FUNDS retry up to ~31 real days out (the next
		// salary window); at scale 1 that genuinely waits weeks and every
		// test asserting that bucket reaches a terminal state within
		// pipelineWait/startupWindow times out. 300000 compresses the
		// worst case (~31 days) to under 9 seconds, comfortably inside
		// pipelineWait, while a scheduler poll interval of a few hundred
		// milliseconds still bounds how fast the compressed wait is
		// actually observed.
		"DEMO_TIME_SCALE": "300000",
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	stack := startStack(ctx, t, "3000000s")
	gwHTTPAddr := stack.gatewayHTTP
	auditAddr := stack.auditAddr

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
	// BANK_TIMEOUT classifies as TRANSIENT_BANK -> RETRY (buckets.go), and
	// Executor now calls the real World Simulator for that action (Phase 5
	// Units C/D), which requires a GROUND_TRUTH row to answer at all.
	// recovery_probability=1.0 for the correct action reproduces the old
	// stub's "attempt 1 succeeds" script deterministically. Seeded
	// immediately after resolving recordID, before Decision Engine's
	// scheduler can possibly claim it: ingestion's HTTP handler already
	// wrote the record row synchronously (that is what the query above just
	// read), and everything after it -- Kafka publish, classify, score,
	// schedule -- is strictly slower than this one synchronous insert.
	if _, err := pool.Exec(ctx, `
		INSERT INTO ground_truth (record_id, true_bucket, recovery_probability, wrong_action_probability, response_delay_seconds)
		VALUES ($1, 'ROOT_CAUSE_BUCKET_TRANSIENT_BANK', 1.0, 0.0, 0)`, recordID); err != nil {
		t.Fatalf("seed ground_truth: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM ground_truth WHERE record_id = $1`, recordID)
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
	// Four transitions now: NEW -> SCORING (classified, guardrails applied),
	// SCORING -> RETRY_SCHEDULED (the economics gate chose a retry),
	// RETRY_SCHEDULED -> RETRYING (the scheduler claiming the due record),
	// RETRYING -> RECOVERED (the execute outcome). Every transition is its
	// own AUDIT_ENTRY row by design (docs/ARCHITECTURE.md section 7), so the
	// trail reads as a replay of the state diagram rather than a summary of
	// where the record ended up. The walking skeleton's single collapsed step
	// was the starting point, not the final shape, and the Scoring hop is the
	// Phase 2 economics gate every record now passes through.
	entries := auditResp.GetEntries()
	if len(entries) != 4 {
		t.Fatalf("audit entries = %d, want 4 (classify, score, claim, outcome)", len(entries))
	}
	if got := entries[1].GetFromState(); got != commonv1.RecordState_RECORD_STATE_SCORING {
		t.Errorf("entry[1] FromState = %v, want SCORING: every record passes through the economics gate", got)
	}

	classify := entries[0]
	if classify.GetSource() != commonv1.Source_SOURCE_RULES_FALLBACK {
		t.Errorf("classify entry Source = %v, want SOURCE_RULES_FALLBACK", classify.GetSource())
	}
	if classify.GetRationale() == "" {
		t.Error("classify entry Rationale is empty")
	}
	// The provider hops survive the whole trip: chain -> gRPC response ->
	// decision-engine -> audit_entry.provider_hops -> GetRecordAudit
	// (Phase 3 Unit E). The default chain is rules-only, so exactly one hop,
	// and it answered. This is the assertion that would catch the column
	// being written but never selected, or selected but never decoded.
	hops := classify.GetHops()
	if len(hops) != 1 {
		t.Fatalf("classify entry hops = %+v, want exactly one for the rules-only default chain", hops)
	}
	if hops[0].GetProvider() != "rules" || hops[0].GetResult() != "ok" {
		t.Errorf("classify entry hop = %s/%s, want rules/ok", hops[0].GetProvider(), hops[0].GetResult())
	}
	// A claim or an outcome transition involves no classification, so it must
	// carry no hops. NULL and "tried nothing" are different facts.
	if got := entries[len(entries)-1].GetHops(); len(got) != 0 {
		t.Errorf("outcome entry hops = %+v, want none: no provider was called for an execute outcome", got)
	}

	outcome := entries[len(entries)-1]
	if outcome.GetToState() != commonv1.RecordState_RECORD_STATE_RECOVERED {
		t.Errorf("final entry ToState = %v, want RECOVERED", outcome.GetToState())
	}
	if outcome.GetAttemptNumber() != 1 {
		t.Errorf("final entry AttemptNumber = %d, want 1", outcome.GetAttemptNumber())
	}
	if auditResp.GetRecord().GetAmountPaise() != 75000 {
		t.Errorf("audit Record.AmountPaise = %d, want 75000", auditResp.GetRecord().GetAmountPaise())
	}
}
