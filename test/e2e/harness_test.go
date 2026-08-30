//go:build e2e

// Shared bring-up for every end-to-end test in this package: build the real
// service binaries, start them as subprocesses against the docker-compose
// infra, and hand back the addresses a client would use.
//
// Extracted so a second test does not mean a second copy of a hundred lines
// of process wiring (docs/ENGINEERING.md section 14). Each test gets its own
// stack with its own Kafka topics and consumer group, rather than sharing
// one: the Decision Engine's scheduler polls RECORD_STATE system-wide by
// design, so two tests running against one stack would be able to claim each
// other's records. That exact class of cross-test interference has already
// cost this repo real time twice (docs/INCIDENTS.md), and `go build` caches
// compilation so the second bring-up is cheap.
package e2e

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/thisizaro/Momotaro/internal/platform/kafkax"
)

// stack is a running pipeline: the addresses a test needs, and nothing else.
type stack struct {
	gatewayHTTP  string // host:port for the public HTTP API
	auditAddr    string // host:port for Audit's gRPC
	executorAddr string // host:port for Executor's gRPC
	topic        string // this stack's isolated raw.events topic
	dlqTopic     string
	auditTopic   string // this stack's isolated audit.events topic

	// Decision Engine restart support (Unit K, docs/PHASE2_IMPLEMENTATION.md):
	// everything needed to kill the running process and start an
	// identically-configured replacement. Every other field above is
	// sufficient for every test but K; these two exist only for
	// restartDecisionEngine below.
	decisionEngineBin string
	decisionEngineEnv map[string]string
	decisionEngine    *process
}

// restartDecisionEngine hard-kills the running Decision Engine (SIGKILL, not
// the graceful SIGTERM stop() sends -- a clean shutdown exercises the
// graceful path, which is not the one Unit K exists to prove) and starts a
// fresh process with identical configuration, including the same Kafka
// consumer group and topic, so it resumes from the last committed offset
// rather than replaying or skipping anything.
//
// There is no readiness port this harness waits on for it specifically:
// the Decision Engine IS a real gRPC server too (services/decision-engine/
// cmd/main.go registers DecisionEngineServiceServer on GRPC_PORT, to answer
// ReportDelayedOutcome, Phase 5 Unit A), but nothing in this stack needs to
// dial it synchronously at startup the way Executor dials World Simulator,
// so callers rely on their own polling of record_state/Audit for the
// process actually resuming work, the same as they would for any other
// asynchronous effect.
func (s *stack) restartDecisionEngine(t *testing.T) {
	t.Helper()
	s.decisionEngine.kill(t)

	p := startProcess(t, "decision-engine", s.decisionEngineBin, s.decisionEngineEnv)
	// The replacement isn't in startStack's procs slice, so it needs its own
	// cleanup registration -- otherwise a restarted Decision Engine leaks
	// past the end of the test.
	t.Cleanup(func() { p.stop(t) })
	s.decisionEngine = p
}

// startStack builds and starts all six services and returns once every one of
// them is accepting connections. Everything it starts is stopped via t.Cleanup
// in reverse order, so a test never has to remember to tear down.
//
// retryDelay is how long a scheduled retry waits before the scheduler claims
// it. Tests pass something well under pipelineWait; production's default is
// 30s and its real cause-aware timing is Phase 2 (docs/ARCHITECTURE.md
// section 5a).
func startStack(ctx context.Context, t *testing.T, retryDelay string) *stack {
	t.Helper()
	return startStackWithEnv(ctx, t, retryDelay, nil, nil)
}

// startStackWithEnv is startStack plus extra env vars merged into the
// classifier's and/or the Decision Engine's process, for a test that needs
// to point the live provider chain at a fake endpoint rather than the
// rules-only default (Phase 3 Unit C: proving the fallback path with the
// real classifier binary and a real HTTP failure, not a fake rung). Both
// maps matter: the classifier's LLM_PROVIDER_CHAIN decides which rungs
// EXIST, but the Decision Engine's LLM_SAMPLE_RATE decides whether any given
// record is even allowed to reach them (ClassifyRequest.force_rules_only,
// Phase 3 Unit H) -- a test that only overrides the classifier's env still
// gets force_rules_only=true at the default sample rate 0.0, and the LLM
// rung is filtered out before it is ever called, same as a request that
// never named it. A new function rather than parameters on startStack
// itself, so the other seven existing callers need no change.
func startStackWithEnv(ctx context.Context, t *testing.T, retryDelay string, classifierEnv, decisionEngineEnv map[string]string) *stack {
	t.Helper()

	root := repoRoot(t)
	binDir := t.TempDir()

	// Isolated topics and consumer group per stack. Earlier runs have already
	// published messages for records that no longer exist in Postgres, and a
	// fresh consumer group on the shared topic would replay them and fail on
	// record_state's foreign key before reaching this test's own message.
	// See docs/INCIDENTS.md.
	s := &stack{
		topic:      "e2e-raw-events-" + uuid.NewString(),
		dlqTopic:   "e2e-raw-events-dlq-" + uuid.NewString(),
		auditTopic: "e2e-audit-events-" + uuid.NewString(),
	}
	group := "e2e-decision-engine-" + uuid.NewString()
	for _, topic := range []string{s.topic, s.dlqTopic, s.auditTopic} {
		if err := kafkax.EnsureTopic(ctx, []string{kafkaBrokers}, topic, 1); err != nil {
			t.Fatalf("EnsureTopic %s: %v", topic, err)
		}
	}

	classifierBin := buildBinary(t, root, binDir, "services", "classifier")
	executorBin := buildBinary(t, root, binDir, "services", "executor")
	auditBin := buildBinary(t, root, binDir, "services", "audit")
	ingestionBin := buildBinary(t, root, binDir, "services", "ingestion")
	decisionEngineBin := buildBinary(t, root, binDir, "services", "decision-engine")
	apiGatewayBin := buildBinary(t, root, binDir, "services", "api-gateway")
	// Phase 5 Units C/D: Executor no longer has an in-process stub for
	// either port, it dials these two for real, so the harness must run
	// them too or Executor's own required config fails fast at startup.
	// Both live under demo/, not services/ (docs/ARCHITECTURE.md section
	// 2a: demo-only components are never part of the main app).
	worldSimulatorBin := buildBinary(t, root, binDir, "demo", "world-simulator")
	notificationSimulatorBin := buildBinary(t, root, binDir, "demo", "notification-simulator")

	classifierPort, classifierMetrics := freePort(t), freePort(t)
	executorPort, executorMetrics := freePort(t), freePort(t)
	auditPort, auditMetrics := freePort(t), freePort(t)
	ingestionPort, ingestionMetrics := freePort(t), freePort(t)
	deGRPCPort, deMetrics := freePort(t), freePort(t) // also World Simulator's ReportDelayedOutcome callback target
	gwPort, gwMetrics := freePort(t), freePort(t)
	gwHTTPPort := freePort(t)
	worldSimPort, worldSimMetrics := freePort(t), freePort(t)
	notificationSimPort, notificationSimMetrics := freePort(t), freePort(t)

	var procs []*process
	t.Cleanup(func() {
		for i := len(procs) - 1; i >= 0; i-- {
			procs[i].stop(t)
		}
	})

	classifierAddr := fmt.Sprintf("127.0.0.1:%d", classifierPort)
	executorAddr := fmt.Sprintf("127.0.0.1:%d", executorPort)
	ingestionAddr := fmt.Sprintf("127.0.0.1:%d", ingestionPort)
	worldSimAddr := fmt.Sprintf("127.0.0.1:%d", worldSimPort)
	notificationSimAddr := fmt.Sprintf("127.0.0.1:%d", notificationSimPort)
	s.auditAddr = fmt.Sprintf("127.0.0.1:%d", auditPort)
	s.executorAddr = executorAddr
	s.gatewayHTTP = fmt.Sprintf("127.0.0.1:%d", gwHTTPPort)

	procs = append(procs, startProcess(t, "classifier", classifierBin, merge(commonEnv(classifierPort, classifierMetrics), classifierEnv)))
	// World Simulator and Notification Simulator start before Executor:
	// not strictly required (grpc.NewClient dials lazily), but it keeps
	// the dependency order in this list honest.
	procs = append(procs, startProcess(t, "world-simulator", worldSimulatorBin, merge(commonEnv(worldSimPort, worldSimMetrics), map[string]string{
		"DECISION_ENGINE_ADDR": fmt.Sprintf("127.0.0.1:%d", deGRPCPort),
	})))
	procs = append(procs, startProcess(t, "notification-simulator", notificationSimulatorBin, commonEnv(notificationSimPort, notificationSimMetrics)))
	procs = append(procs, startProcess(t, "executor", executorBin, merge(commonEnv(executorPort, executorMetrics), map[string]string{
		"WORLD_SIMULATOR_ADDR":        worldSimAddr,
		"NOTIFICATION_SIMULATOR_ADDR": notificationSimAddr,
	})))
	procs = append(procs, startProcess(t, "audit", auditBin, commonEnv(auditPort, auditMetrics)))
	procs = append(procs, startProcess(t, "ingestion", ingestionBin, merge(commonEnv(ingestionPort, ingestionMetrics), map[string]string{
		"RAW_EVENTS_TOPIC": s.topic,
	})))

	readyCtx, readyCancel := context.WithTimeout(ctx, startupWindow)
	defer readyCancel()
	for _, addr := range []string{classifierAddr, worldSimAddr, notificationSimAddr, executorAddr, s.auditAddr, ingestionAddr} {
		if err := waitForTCP(readyCtx, addr); err != nil {
			t.Fatalf("service did not become ready: %v", err)
		}
	}

	deEnv := merge(commonEnv(deGRPCPort, deMetrics), map[string]string{
		"CLASSIFIER_ADDR":           classifierAddr,
		"EXECUTOR_ADDR":             executorAddr,
		"CALL_TIMEOUT":              "5s",
		"RAW_EVENTS_TOPIC":          s.topic,
		"RAW_EVENTS_CONSUMER_GROUP": group,
		"RAW_EVENTS_DLQ_TOPIC":      s.dlqTopic,
		"AUDIT_EVENTS_TOPIC":        s.auditTopic,
		// Short and isolated: production's real cause-aware timing
		// (ARCHITECTURE.md section 5a) is Phase 2 work, and Phase 1's fixed
		// delay defaults to 30s, which is not under pipelineWait.
		// Absolute paths: the binaries run from a temp directory, so the
		// service's default relative path finds no configs/ and the Decision
		// Engine refuses to start. It is right to refuse (an engine that
		// cannot price its actions would close every record as uneconomic
		// while looking healthy), so the harness supplies real paths rather
		// than the service being made lenient.
		"INTERVENTION_COSTS_PATH": filepath.Join(root, "configs", "intervention_costs.yaml"),
		"RECOVERY_PRIORS_PATH":    filepath.Join(root, "configs", "recovery_priors.yaml"),
		"RETRY_DELAY":             retryDelay,
		"NUDGE_DELAY":             retryDelay,
		"SCHEDULER_POLL_INTERVAL": "300ms",
	})
	deEnv = merge(deEnv, decisionEngineEnv)
	deProc := startProcess(t, "decision-engine", decisionEngineBin, deEnv)
	procs = append(procs, deProc)
	s.decisionEngineBin = decisionEngineBin
	s.decisionEngineEnv = deEnv
	s.decisionEngine = deProc

	procs = append(procs, startProcess(t, "api-gateway", apiGatewayBin, merge(commonEnv(gwPort, gwMetrics), map[string]string{
		"INGESTION_ADDR": ingestionAddr,
		"API_KEY":        apiKey,
		"HTTP_PORT":      strconv.Itoa(gwHTTPPort),
		"CALL_TIMEOUT":   "5s",
	})))

	if err := waitForTCP(readyCtx, s.gatewayHTTP); err != nil {
		t.Fatalf("api-gateway did not become ready: %v", err)
	}
	return s
}
