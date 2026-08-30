//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	auditv1 "github.com/thisizaro/Momotaro/proto/gen/audit/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// smokeCase is one record submitted through the public API, and the state the
// pipeline is expected to settle it in.
//
// The expectations are derived, not guessed: the Classifier maps failure_code
// to a bucket and one action (services/classifier/internal/rules), the
// Decision Engine turns that action into a state
// (decideAfterClassify/decideAfterExecute), and the Executor's scripted stub
// decides what executing it does. Each row below names that whole chain, so a
// failure says which link broke rather than just "wrong state".
type smokeCase struct {
	name        string
	recordType  string
	failureCode string
	amountPaise int64
	// wantState is where this record should come to rest.
	wantState commonv1.RecordState
	// wantSettled is false for a state the pipeline parks in while waiting on
	// something Phase 1 does not have yet.
	wantTerminal bool
	// why documents the expected path, so a broken link is diagnosable.
	why string
	// recordID is filled in after submission.
	recordID string

	// groundTruthBucket is the RootCauseBucket string World Simulator's
	// GROUND_TRUTH row must carry for this case (Phase 5 Units C/D:
	// Executor now calls the real World Simulator for RETRY/NUDGE, which
	// requires one to exist). Empty means this case escalates at classify
	// time and never reaches Executor at all, so no row is seeded.
	groundTruthBucket      string
	recoveryProbability    float64
	wrongActionProbability float64
	// responseDelaySeconds only matters for a NUDGE case. neverResolves
	// (below) is used for both NUDGE cases here: World Simulator always
	// answers PENDING for a nudge whose scaled delay is still positive,
	// regardless of the roll (server.go), so this guarantees the record
	// stays parked in NUDGED for the lifetime of any conceivable test
	// run, matching settledStates' claim that NUDGED is genuinely at
	// rest here, not "at rest until this specific delay happens to fire".
	responseDelaySeconds int32
}

// neverResolves scales down to ~55 minutes under DEMO_TIME_SCALE=300000,
// far past pipelineWait (30s): large enough that a NUDGE case's parked
// state cannot flip to something else mid-assertion, without being an
// obviously-wrong sentinel like MaxInt32.
const neverResolves int32 = 999999999

func smokeCases() []smokeCase {
	return []smokeCase{
		{
			name: "transient bank failure recovers on retry", recordType: "PAYMENT",
			failureCode: "BANK_TIMEOUT", amountPaise: 75000,
			wantState: commonv1.RecordState_RECORD_STATE_RECOVERED, wantTerminal: true,
			why:                 "TRANSIENT_BANK -> RETRY -> scheduled -> claimed -> World Simulator succeeds on attempt 1",
			groundTruthBucket:   commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK.String(),
			recoveryProbability: 1.0,
		},
		{
			name: "insufficient funds also retries", recordType: "MANDATE",
			failureCode: "INSUFFICIENT_FUNDS", amountPaise: 250000,
			wantState: commonv1.RecordState_RECORD_STATE_RECOVERED, wantTerminal: true,
			why:                 "INSUFFICIENT_FUNDS -> RETRY (salary-window timing is Phase 2) -> succeeds on attempt 1",
			groundTruthBucket:   commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS.String(),
			recoveryProbability: 1.0,
		},
		{
			name: "rail congestion recovers on retry", recordType: "PAYMENT",
			failureCode: "RAIL_CONGESTION", amountPaise: 12000,
			wantState: commonv1.RecordState_RECORD_STATE_RECOVERED, wantTerminal: true,
			why:                 "TRANSIENT_BANK -> RETRY -> succeeds on attempt 1",
			groundTruthBucket:   commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK.String(),
			recoveryProbability: 1.0,
		},
		{
			name: "risk hold is escalated, never auto-actioned", recordType: "PAYMENT",
			failureCode: "RISK_HOLD", amountPaise: 500000,
			wantState: commonv1.RecordState_RECORD_STATE_ESCALATED, wantTerminal: true,
			why: "RISK_HOLD -> ESCALATE at classify time; ARCHITECTURE.md 5a forbids auto-retrying a risk decision" +
				" (never reaches Executor, so no GROUND_TRUTH row is needed)",
		},
		{
			name: "unrecognised failure code is escalated for review", recordType: "PAYMENT",
			failureCode: "SOMETHING_WE_HAVE_NEVER_SEEN", amountPaise: 9900,
			wantState: commonv1.RecordState_RECORD_STATE_ESCALATED, wantTerminal: true,
			why: "unknown code on a PAYMENT -> UNSPECIFIED bucket -> ESCALATE rather than a guess" +
				" (never reaches Executor, so no GROUND_TRUTH row is needed)",
		},
		{
			name: "dead instrument is nudged, not retried", recordType: "PAYMENT",
			failureCode: "EXPIRED_INSTRUMENT", amountPaise: 33000,
			wantState: commonv1.RecordState_RECORD_STATE_NUDGED, wantTerminal: false,
			why: "HARD_DECLINE -> NUDGE_METHOD_UPDATE (a retry cannot fix a dead card) -> sent -> PENDING," +
				" parked awaiting the delayed-outcome callback (Phase 5 Unit C), which this test does not wait for",
			groundTruthBucket: commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE.String(),
			// [ASSUMPTION] matching scripts/batchgen/profile.go's HARD_DECLINE
			// profile; irrelevant to this test's own assertions (only that it
			// parks in NUDGED, not what the eventual answer would have been).
			recoveryProbability: 0.15, wrongActionProbability: 0.02,
			responseDelaySeconds: neverResolves,
		},
		{
			name: "abandoned checkout is reminded", recordType: "CHECKOUT",
			failureCode: "CHECKOUT_ABANDONED", amountPaise: 45000,
			wantState: commonv1.RecordState_RECORD_STATE_NUDGED, wantTerminal: false,
			why:                 "ABANDONMENT -> NUDGE_REMINDER -> sent -> PENDING, parked as above",
			groundTruthBucket:   commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_ABANDONMENT.String(),
			recoveryProbability: 0.80, wrongActionProbability: 0.05,
			responseDelaySeconds: neverResolves,
		},
	}
}

// TestSmokeBatchReachesExpectedTerminalStates is docs/PLAN.md Phase 1's final
// item: a handful of records through the real pipeline, each reaching the state
// its own failure code implies, with a complete audit trail and zero invariant
// violations across the whole batch.
//
// What makes this different from the walking-skeleton test is coverage of the
// branches: that test proves one record can reach Recovered, this one proves
// Recovered, Escalated and the parked Nudged path are all reachable from the
// public API, and that the Audit service agrees the history is sound.
func TestSmokeBatchReachesExpectedTerminalStates(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	stack := startStack(ctx, t, "3000000s")
	cases := smokeCases()

	batchID := submitSmokeBatch(ctx, t, stack.gatewayHTTP, cases)
	t.Logf("submitted batch %s with %d records", batchID, len(cases))

	pool := connectPool(ctx, t)
	resolveRecordIDs(ctx, t, pool, batchID, cases)
	waitForSettled(ctx, t, pool, cases)

	auditClient := dialAudit(ctx, t, stack.auditAddr)

	for i := range cases {
		tc := &cases[i]
		t.Run(tc.name, func(t *testing.T) {
			assertRecordState(ctx, t, pool, tc)
			assertAuditTrail(ctx, t, auditClient, tc)
		})
	}

	// The batch-wide correctness claim, checked by the service whose job it is
	// rather than by this test re-deriving it (docs/ARCHITECTURE.md section
	// 10a). Scoped to this batch so it cannot be perturbed by anything else
	// in the database.
	assertNoInvariantViolations(ctx, t, auditClient, batchID, len(cases))

	assertInterventionSpendRecorded(ctx, t, pool, cases)
}

// --- submission -------------------------------------------------------------

func submitSmokeBatch(ctx context.Context, t *testing.T, gatewayAddr string, cases []smokeCase) string {
	t.Helper()

	var records []string
	for i, tc := range cases {
		records = append(records, fmt.Sprintf(
			`{"type":%q,"amount_paise":%d,"currency":"INR","failure_code":%q,"instrument_ref":"card_smoke_%d"}`,
			tc.recordType, tc.amountPaise, tc.failureCode, i))
	}
	body := fmt.Sprintf(`{"source":"e2e-smoke","records":[%s]}`, strings.Join(records, ","))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+gatewayAddr+"/v1/batches", strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/batches: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /v1/batches status = %d, body: %s", resp.StatusCode, raw)
	}

	var decoded struct {
		BatchID       string `json:"batch_id"`
		AcceptedCount int32  `json:"accepted_count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.BatchID == "" {
		t.Fatal("response carried no batch_id")
	}
	if int(decoded.AcceptedCount) != len(cases) {
		t.Fatalf("accepted_count = %d, want %d: the Gateway rejected a record this test depends on",
			decoded.AcceptedCount, len(cases))
	}
	return decoded.BatchID
}

// resolveRecordIDs maps each submitted case to the record id Ingestion
// assigned it. Correlated on failure_code, which is unique per case here.
func resolveRecordIDs(ctx context.Context, t *testing.T, pool *pgxpkg.Pool, batchID string, cases []smokeCase) {
	t.Helper()
	for i := range cases {
		tc := &cases[i]
		var id string
		err := pool.QueryRow(ctx,
			`SELECT id FROM record WHERE batch_id = $1 AND failure_code = $2`,
			batchID, tc.failureCode).Scan(&id)
		if err != nil {
			t.Fatalf("find record for %s: %v", tc.failureCode, err)
		}
		tc.recordID = id

		if tc.groundTruthBucket != "" {
			if _, err := pool.Exec(ctx, `
				INSERT INTO ground_truth (record_id, true_bucket, recovery_probability, wrong_action_probability, response_delay_seconds)
				VALUES ($1, $2, $3, $4, $5)`,
				tc.recordID, tc.groundTruthBucket, tc.recoveryProbability, tc.wrongActionProbability, tc.responseDelaySeconds); err != nil {
				t.Fatalf("seed ground_truth for %s: %v", tc.failureCode, err)
			}
		}
	}
	t.Cleanup(func() {
		bg := context.Background()
		for _, tc := range cases {
			_, _ = pool.Exec(bg, `DELETE FROM ground_truth WHERE record_id = $1`, tc.recordID)
			_, _ = pool.Exec(bg, `DELETE FROM audit_entry WHERE record_id = $1`, tc.recordID)
			_, _ = pool.Exec(bg, `DELETE FROM intervention_attempt WHERE record_id = $1`, tc.recordID)
			_, _ = pool.Exec(bg, `DELETE FROM record_state WHERE record_id = $1`, tc.recordID)
			_, _ = pool.Exec(bg, `DELETE FROM record WHERE id = $1`, tc.recordID)
		}
		_, _ = pool.Exec(bg, `DELETE FROM batch WHERE id = $1`, batchID)
	})
}

// --- waiting ----------------------------------------------------------------

// terminalStates are exactly the states common.proto marks terminal. Nudged
// is deliberately absent: it is where a record waits for the Phase 5 delayed
// outcome, which is at rest but not finished.
var terminalStates = map[commonv1.RecordState]bool{
	commonv1.RecordState_RECORD_STATE_RECOVERED:         true,
	commonv1.RecordState_RECORD_STATE_ESCALATED:         true,
	commonv1.RecordState_RECORD_STATE_CLOSED_UNECONOMIC: true,
}

// settledStates are the states a Phase 1 record stops moving in. Nudged is
// included because the callback that would move it on
// (DecisionEngine.ReportDelayedOutcome) has no caller until Phase 5, so a
// nudged record is genuinely at rest even though it is not terminal.
var settledStates = map[commonv1.RecordState]bool{
	commonv1.RecordState_RECORD_STATE_RECOVERED:         true,
	commonv1.RecordState_RECORD_STATE_ESCALATED:         true,
	commonv1.RecordState_RECORD_STATE_NUDGED:            true,
	commonv1.RecordState_RECORD_STATE_CLOSED_UNECONOMIC: true,
}

func waitForSettled(ctx context.Context, t *testing.T, p *pgxpkg.Pool, cases []smokeCase) {
	t.Helper()
	deadline := time.Now().Add(pipelineWait)
	for {
		states := currentStates(ctx, t, p, cases)
		pending := 0
		for _, s := range states {
			if !settledStates[s] {
				pending++
			}
		}
		if pending == 0 {
			return
		}
		if time.Now().After(deadline) {
			for i, tc := range cases {
				t.Logf("  %-46s %s", tc.failureCode, states[i])
			}
			t.Fatalf("%d of %d records had not settled within %s", pending, len(cases), pipelineWait)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func currentStates(ctx context.Context, t *testing.T, p *pgxpkg.Pool, cases []smokeCase) []commonv1.RecordState {
	t.Helper()
	out := make([]commonv1.RecordState, len(cases))
	for i, tc := range cases {
		var s string
		err := p.QueryRow(ctx, `SELECT current_state FROM record_state WHERE record_id = $1`, tc.recordID).Scan(&s)
		if err != nil {
			// No row yet: the Decision Engine has not consumed it. Normal
			// while the pipeline is still working.
			out[i] = commonv1.RecordState_RECORD_STATE_UNSPECIFIED
			continue
		}
		out[i] = commonv1.RecordState(commonv1.RecordState_value[s])
	}
	return out
}

// --- assertions -------------------------------------------------------------

func assertRecordState(ctx context.Context, t *testing.T, p *pgxpkg.Pool, tc *smokeCase) {
	t.Helper()
	var state string
	var attemptCount int
	var bucket *string
	err := p.QueryRow(ctx,
		`SELECT current_state, attempt_count, root_cause_bucket FROM record_state WHERE record_id = $1`,
		tc.recordID).Scan(&state, &attemptCount, &bucket)
	if err != nil {
		t.Fatalf("query record_state: %v", err)
	}

	got := commonv1.RecordState(commonv1.RecordState_value[state])
	if got != tc.wantState {
		t.Errorf("state = %v, want %v\n  expected path: %s", got, tc.wantState, tc.why)
	}
	if bucket == nil || *bucket == commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_UNSPECIFIED.String() {
		// Only the unknown-code case may legitimately have no bucket.
		if tc.failureCode != "SOMETHING_WE_HAVE_NEVER_SEEN" {
			t.Errorf("root_cause_bucket = %v, want a real bucket: the classification was not persisted", bucket)
		}
	}

	// Cross-check this test's own expectation against the contract: the three
	// states common.proto marks terminal are the only ones that count as
	// terminal, so a drift between the two shows up here rather than in a
	// misleading pass.
	if terminalStates[tc.wantState] != tc.wantTerminal {
		t.Errorf("test expects wantTerminal=%v for %v, but common.proto says terminal=%v",
			tc.wantTerminal, tc.wantState, terminalStates[tc.wantState])
	}

	// Nothing settled may still be scheduled for future work: a leftover
	// due_at means the scheduler would pick the record up again.
	var dueAt *time.Time
	if err := p.QueryRow(ctx, `SELECT due_at FROM record_state WHERE record_id = $1`, tc.recordID).Scan(&dueAt); err != nil {
		t.Fatalf("query due_at: %v", err)
	}
	if dueAt != nil {
		t.Errorf("due_at = %v on a settled record: the scheduler would claim it again", *dueAt)
	}

	// An escalation decided at classify time never executed anything, so its
	// attempt count must still be zero. Anything else means an action ran
	// that the state machine did not intend.
	if tc.wantState == commonv1.RecordState_RECORD_STATE_ESCALATED && attemptCount != 0 {
		t.Errorf("attempt_count = %d for a classify-time escalation, want 0: something executed an action it should not have", attemptCount)
	}
	if tc.wantState == commonv1.RecordState_RECORD_STATE_RECOVERED && attemptCount != 1 {
		t.Errorf("attempt_count = %d, want 1", attemptCount)
	}
}

func assertAuditTrail(ctx context.Context, t *testing.T, client auditv1.AuditServiceClient, tc *smokeCase) {
	t.Helper()
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	resp, err := client.GetRecordAudit(callCtx, &auditv1.GetRecordAuditRequest{RecordId: tc.recordID})
	if err != nil {
		t.Fatalf("GetRecordAudit: %v", err)
	}

	// trail_complete is a real computation now, so this assertion means
	// something: it re-runs the same checks VerifyInvariants aggregates.
	if !resp.GetTrailComplete() {
		t.Errorf("TrailComplete = false: the trail has a gap or an impossible transition")
	}
	if resp.GetCurrentState() != tc.wantState {
		t.Errorf("audit CurrentState = %v, want %v", resp.GetCurrentState(), tc.wantState)
	}
	if resp.GetRecord().GetAmountPaise() != tc.amountPaise {
		t.Errorf("audit Record.AmountPaise = %d, want %d", resp.GetRecord().GetAmountPaise(), tc.amountPaise)
	}

	entries := resp.GetEntries()
	if len(entries) == 0 {
		t.Fatal("no audit entries at all: every state change must be recorded")
	}

	// The first entry is always the classification, and it is the one that
	// carries the reasoning a human would read.
	first := entries[0]
	if first.GetFromState() != commonv1.RecordState_RECORD_STATE_NEW {
		t.Errorf("first entry FromState = %v, want NEW", first.GetFromState())
	}
	if first.GetSource() != commonv1.Source_SOURCE_RULES_FALLBACK {
		t.Errorf("first entry Source = %v, want SOURCE_RULES_FALLBACK (no LLM is wired until Phase 3)", first.GetSource())
	}
	if first.GetRationale() == "" {
		t.Error("first entry Rationale is empty: the reasoning is what makes this auditable")
	}

	// The trail must chain, and it must end where the record actually is.
	for i := 1; i < len(entries); i++ {
		if entries[i].GetFromState() != entries[i-1].GetToState() {
			t.Errorf("entry %d starts at %v but the previous ended at %v: the trail has a gap",
				i, entries[i].GetFromState(), entries[i-1].GetToState())
		}
	}
	if last := entries[len(entries)-1]; last.GetToState() != tc.wantState {
		t.Errorf("last entry ToState = %v, want %v", last.GetToState(), tc.wantState)
	}
}

func assertNoInvariantViolations(ctx context.Context, t *testing.T, client auditv1.AuditServiceClient, batchID string, wantRecords int) {
	t.Helper()
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	resp, err := client.VerifyInvariants(callCtx, &auditv1.VerifyInvariantsRequest{BatchId: batchID})
	if err != nil {
		t.Fatalf("VerifyInvariants: %v", err)
	}

	if int(resp.GetRecordsChecked()) != wantRecords {
		t.Errorf("RecordsChecked = %d, want %d", resp.GetRecordsChecked(), wantRecords)
	}
	// PRD.md sections 9 and 10: these are the numbers the project claims, and
	// a non-zero one is a bug rather than a business outcome.
	if resp.GetIncompleteAuditTrails() != 0 {
		t.Errorf("IncompleteAuditTrails = %d, want 0; examples: %v", resp.GetIncompleteAuditTrails(), resp.GetExamples())
	}
	if resp.GetImpossibleTransitions() != 0 {
		t.Errorf("ImpossibleTransitions = %d, want 0; examples: %v", resp.GetImpossibleTransitions(), resp.GetExamples())
	}
	if resp.GetStoppingRuleViolations() != 0 {
		t.Errorf("StoppingRuleViolations = %d, want 0; examples: %v", resp.GetStoppingRuleViolations(), resp.GetExamples())
	}
}

// assertInterventionSpendRecorded checks that every record which actually
// executed something logged what it cost. This is what makes "net recovered"
// a measurement rather than an estimate (docs/ARCHITECTURE.md section 10).
func assertInterventionSpendRecorded(ctx context.Context, t *testing.T, p *pgxpkg.Pool, cases []smokeCase) {
	t.Helper()
	for _, tc := range cases {
		if tc.wantState == commonv1.RecordState_RECORD_STATE_ESCALATED {
			continue // decided at classify time, nothing was executed
		}
		var attempts int
		var totalCost int64
		err := p.QueryRow(ctx,
			`SELECT count(*), coalesce(sum(cost_paise), 0) FROM intervention_attempt WHERE record_id = $1`,
			tc.recordID).Scan(&attempts, &totalCost)
		if err != nil {
			t.Fatalf("query intervention_attempt for %s: %v", tc.failureCode, err)
		}
		if attempts != 1 {
			t.Errorf("%s: intervention_attempt rows = %d, want 1", tc.failureCode, attempts)
		}
		if totalCost <= 0 {
			t.Errorf("%s: logged spend = %d, want > 0: an executed intervention is never free", tc.failureCode, totalCost)
		}
	}
}

// --- plumbing ---------------------------------------------------------------

func dialAudit(ctx context.Context, t *testing.T, addr string) auditv1.AuditServiceClient {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial audit: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return auditv1.NewAuditServiceClient(conn)
}

func connectPool(ctx context.Context, t *testing.T) *pgxpkg.Pool {
	t.Helper()
	poolCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	pool, err := pgxpkg.NewPool(poolCtx, postgresDSN)
	if err != nil {
		t.Fatalf("connect to postgres: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
