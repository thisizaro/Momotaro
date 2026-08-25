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
	gatewayHTTP string // host:port for the public HTTP API
	auditAddr   string // host:port for Audit's gRPC
	topic       string // this stack's isolated raw.events topic
	dlqTopic    string
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

	root := repoRoot(t)
	binDir := t.TempDir()

	// Isolated topics and consumer group per stack. Earlier runs have already
	// published messages for records that no longer exist in Postgres, and a
	// fresh consumer group on the shared topic would replay them and fail on
	// record_state's foreign key before reaching this test's own message.
	// See docs/INCIDENTS.md.
	s := &stack{
		topic:    "e2e-raw-events-" + uuid.NewString(),
		dlqTopic: "e2e-raw-events-dlq-" + uuid.NewString(),
	}
	group := "e2e-decision-engine-" + uuid.NewString()
	for _, topic := range []string{s.topic, s.dlqTopic} {
		if err := kafkax.EnsureTopic(ctx, []string{kafkaBrokers}, topic, 1); err != nil {
			t.Fatalf("EnsureTopic %s: %v", topic, err)
		}
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
	t.Cleanup(func() {
		for i := len(procs) - 1; i >= 0; i-- {
			procs[i].stop(t)
		}
	})

	classifierAddr := fmt.Sprintf("127.0.0.1:%d", classifierPort)
	executorAddr := fmt.Sprintf("127.0.0.1:%d", executorPort)
	ingestionAddr := fmt.Sprintf("127.0.0.1:%d", ingestionPort)
	s.auditAddr = fmt.Sprintf("127.0.0.1:%d", auditPort)
	s.gatewayHTTP = fmt.Sprintf("127.0.0.1:%d", gwHTTPPort)

	procs = append(procs, startProcess(t, "classifier", classifierBin, commonEnv(classifierPort, classifierMetrics)))
	procs = append(procs, startProcess(t, "executor", executorBin, commonEnv(executorPort, executorMetrics)))
	procs = append(procs, startProcess(t, "audit", auditBin, commonEnv(auditPort, auditMetrics)))
	procs = append(procs, startProcess(t, "ingestion", ingestionBin, merge(commonEnv(ingestionPort, ingestionMetrics), map[string]string{
		"RAW_EVENTS_TOPIC": s.topic,
	})))

	readyCtx, readyCancel := context.WithTimeout(ctx, startupWindow)
	defer readyCancel()
	for _, addr := range []string{classifierAddr, executorAddr, s.auditAddr, ingestionAddr} {
		if err := waitForTCP(readyCtx, addr); err != nil {
			t.Fatalf("service did not become ready: %v", err)
		}
	}

	procs = append(procs, startProcess(t, "decision-engine", decisionEngineBin, merge(commonEnv(deGRPCPort, deMetrics), map[string]string{
		"CLASSIFIER_ADDR":           classifierAddr,
		"EXECUTOR_ADDR":             executorAddr,
		"CALL_TIMEOUT":              "5s",
		"RAW_EVENTS_TOPIC":          s.topic,
		"RAW_EVENTS_CONSUMER_GROUP": group,
		"RAW_EVENTS_DLQ_TOPIC":      s.dlqTopic,
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
	})))

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
