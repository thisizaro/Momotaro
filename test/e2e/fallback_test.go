//go:build e2e

// Phase 3 Unit C's e2e half: PRD.md section 12's demo script promises "one
// record where the LLM call failed and fell back to rules." Every other
// test in this package runs the default rules-only chain, which never
// actually proves a fallback happened; this one does, against the real
// classifier binary and a real HTTP failure, not a fake Provider.
package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	auditv1 "github.com/thisizaro/Momotaro/proto/gen/audit/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestFallbackFromFailedLLMToRules(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// A fake Groq that always returns 500. GROQ_BASE_URL points the real
	// classifier binary at it, so this exercises the real HTTP client, the
	// real error taxonomy (services/classifier/internal/llm), and the real
	// chain.hopResultForError mapping, not a fake Provider standing in for
	// all of it.
	groqCalls := 0
	fakeGroq := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		groqCalls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer fakeGroq.Close()

	stack := startStackWithEnv(ctx, t, "3000000s",
		map[string]string{
			"LLM_PROVIDER_CHAIN": "groq,rules",
			"GROQ_API_KEY":       "e2e-fake-key",
			"GROQ_BASE_URL":      fakeGroq.URL,
			"GROQ_MODEL":         "e2e-fake-model",
		},
		// Two knobs now gate a live call, and both must be set or the
		// record never reaches groq at all (docs/DEMO_READINESS.md Unit
		// AI). LLM_SAMPLE_RATE=1.0 alone is not enough: BANK_TIMEOUT
		// classifies as TRANSIENT_BANK at confidence 0.90
		// (rules/actions.go), and routing only offers a record to a live
		// model when that confidence is below LLM_ROUTE_CONFIDENCE_THRESHOLD
		// (default 0.0, which no real confidence value is ever below). Set
		// it above 0.90 here so this specific record counts as ambiguous
		// and the rules-only "peek" call is followed by a real one to
		// groq, exactly what this test exists to prove happened.
		map[string]string{
			"LLM_SAMPLE_RATE":                "1.0",
			"LLM_ROUTE_CONFIDENCE_THRESHOLD": "0.95",
		},
	)
	gwHTTPAddr := stack.gatewayHTTP
	auditAddr := stack.auditAddr

	// --- Act: submit one record through the real HTTP contract. ---
	reqBody := `{
		"source": "e2e-fallback-test",
		"records": [
			{"type": "PAYMENT", "amount_paise": 75000, "currency": "INR", "failure_code": "BANK_TIMEOUT", "instrument_ref": "card_e2e_fallback"}
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
	if submitResp.AcceptedCount != 1 {
		t.Fatalf("accepted_count = %d, want 1", submitResp.AcceptedCount)
	}

	// --- Assert: the record still reaches RECOVERED. A failed LLM rung must
	// cost a fallback, not the record. ---
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
	// Units C/D), which requires a GROUND_TRUTH row. recovery_probability=1.0
	// for the correct action reproduces the old stub's "attempt 1 succeeds"
	// deterministically. See walking_skeleton_test.go for why seeding this
	// immediately after resolving recordID is safe against the scheduler's
	// first claim.
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

	if groqCalls == 0 {
		t.Fatal("the fake Groq server was never called: this test proves nothing about a fallback if the primary was never going to be asked")
	}

	// --- Assert: the audit trail shows a real fallback, not a rules-only
	// call that happened to look the same. This is the assertion Unit E made
	// possible: before it, this test could only reach the classifier
	// directly over gRPC and could not see the hop through the full pipeline.
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

	entries := auditResp.GetEntries()
	if len(entries) == 0 {
		t.Fatal("audit trail is empty")
	}
	classify := entries[0]

	// The point of this test: source is SOURCE_RULES_FALLBACK, which is only
	// true if Groq was actually tried and actually failed. Against a chain
	// where the primary was never going to answer (see "prove the test can
	// fail" in docs/PHASE3_IMPLEMENTATION.md Unit C), this line is the one
	// that must go red.
	if classify.GetSource() != commonv1.Source_SOURCE_RULES_FALLBACK {
		t.Errorf("classify entry Source = %v, want SOURCE_RULES_FALLBACK", classify.GetSource())
	}
	hops := classify.GetHops()
	if len(hops) != 2 {
		t.Fatalf("classify entry hops = %+v, want exactly two: groq tried and failed, rules answered", hops)
	}
	if hops[0].GetProvider() != "groq" || hops[0].GetResult() != "error" {
		t.Errorf("hops[0] = %s/%s, want groq/error (a 500 is not a rate limit, a timeout, or an open circuit)", hops[0].GetProvider(), hops[0].GetResult())
	}
	if hops[1].GetProvider() != "rules" || hops[1].GetResult() != "ok" {
		t.Errorf("hops[1] = %s/%s, want rules/ok", hops[1].GetProvider(), hops[1].GetResult())
	}
}
