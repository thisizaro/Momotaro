//go:build e2e

// Proves Phase 5 Unit G end to end: the Gateway's report/records/list/audit/
// invariants HTTP routes and its WebSocket live relay, all against the real
// running stack, not fakes. TestWalkingSkeletonReachesRecovered already
// proves the pipeline itself reaches RECOVERED with a complete audit trail;
// this test's job is narrower and specifically Unit G's own surface: does
// the Gateway correctly translate what Reporting/Ingestion/Audit already
// know into the documented wire shapes (docs/API_GATEWAY.md), and does the
// WebSocket relay actually deliver a live update end to end.
package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// getJSON issues an authenticated GET, decodes the JSON body into v when the
// response is 200, and returns the status code. Callers that poll for a
// not-yet-ready response inspect the status directly rather than treating a
// decode failure as fatal.
func getJSON(ctx context.Context, t *testing.T, url string, v any) int {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build request for %s: %v", url, err)
	}
	req.Header.Set("X-API-Key", apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode body from %s: %v", url, err)
	}
	return resp.StatusCode
}

func TestGatewayReportRoutesAndLiveRelay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	stack := startStack(ctx, t, "3000000s")
	gwHTTPAddr := stack.gatewayHTTP

	// The live route is keyed by batch_id, which does not exist until the
	// batch below is submitted, so the WS subscription happens right after
	// that -- but still well before ground_truth is seeded, which is the
	// earliest point the scheduler could possibly drive a transition. The
	// Hub has no replay buffer by design (docs/DECISIONS.md), so subscribing
	// any later would risk missing the RECOVERED transition entirely.
	wsCtx, wsCancel := context.WithTimeout(ctx, pipelineWait+startupWindow)
	defer wsCancel()

	// --- Act: submit one batch of one record through the real HTTP contract. ---
	reqBody := `{
		"source": "e2e-gateway-report-test",
		"records": [
			{"type": "PAYMENT", "amount_paise": 75000, "currency": "INR", "failure_code": "BANK_TIMEOUT", "instrument_ref": "card_e2e_report"}
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
	var submitResp struct {
		BatchID       string `json:"batch_id"`
		AcceptedCount int32  `json:"accepted_count"`
	}
	if err := json.NewDecoder(httpResp.Body).Decode(&submitResp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	httpResp.Body.Close()
	if submitResp.BatchID == "" || submitResp.AcceptedCount != 1 {
		t.Fatalf("unexpected submit response: %+v", submitResp)
	}
	t.Logf("submitted batch %s", submitResp.BatchID)

	// Now that batch_id is known, subscribe to its live feed. Still ahead of
	// the transition that matters (RECOVERED), since ground_truth is not
	// seeded yet and the scheduler cannot claim anything without it.
	wsConn, _, err := websocket.Dial(wsCtx, "ws://"+gwHTTPAddr+"/v1/batches/"+submitResp.BatchID+"/live", &websocket.DialOptions{
		Subprotocols: []string{apiKey},
	})
	if err != nil {
		t.Fatalf("dial live WS: %v", err)
	}
	defer wsConn.CloseNow()

	// --- Seed ground truth so the record deterministically reaches
	// RECOVERED, same technique as TestWalkingSkeletonReachesRecovered. ---
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

	// --- Assert: the live feed actually delivers the RECOVERED transition. ---
	var sawRecovered bool
	for !sawRecovered {
		var msg map[string]any
		if err := wsjson.Read(wsCtx, wsConn, &msg); err != nil {
			t.Fatalf("read live update: %v", err)
		}
		if msg["record_id"] != recordID {
			continue
		}
		if msg["to_state"] == commonv1.RecordState_RECORD_STATE_RECOVERED.String() {
			if msg["recovered_delta_paise"] != float64(75000) {
				t.Errorf("recovered_delta_paise = %v, want 75000", msg["recovered_delta_paise"])
			}
			sawRecovered = true
		}
	}
	t.Log("live feed delivered the RECOVERED transition")

	// --- Assert: GET /v1/batches lists this batch. ---
	var listResp struct {
		Batches []struct {
			BatchID string `json:"batch_id"`
		} `json:"batches"`
	}
	getJSON(ctx, t, "http://"+gwHTTPAddr+"/v1/batches?limit=50", &listResp)
	found := false
	for _, b := range listResp.Batches {
		if b.BatchID == submitResp.BatchID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("GET /v1/batches did not list %s among %d batches", submitResp.BatchID, len(listResp.Batches))
	}

	// --- Assert: GET /v1/batches/{id}/report reflects the recovery, once the
	// report catches up (it reads Postgres directly, no polling wait needed
	// beyond the RECOVERED state already confirmed above via the live feed,
	// but a short retry loop keeps this robust against any residual lag).
	var reportResp struct {
		RecoveredPaise int64 `json:"recovered_paise"`
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		status := getJSON(ctx, t, "http://"+gwHTTPAddr+"/v1/batches/"+submitResp.BatchID+"/report", &reportResp)
		if status == http.StatusOK && reportResp.RecoveredPaise == 75000 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET report: recovered_paise = %d, want 75000 (status %d)", reportResp.RecoveredPaise, status)
		}
		time.Sleep(200 * time.Millisecond)
	}

	// --- Assert: GET /v1/batches/{id}/records shows the one record. ---
	var recordsResp struct {
		Records []struct {
			RecordID     string `json:"record_id"`
			AmountPaise  int64  `json:"amount_paise"`
			CurrentState string `json:"current_state"`
		} `json:"records"`
		TotalCount int32 `json:"total_count"`
	}
	getJSON(ctx, t, "http://"+gwHTTPAddr+"/v1/batches/"+submitResp.BatchID+"/records", &recordsResp)
	if len(recordsResp.Records) != 1 || recordsResp.Records[0].RecordID != recordID {
		t.Fatalf("records = %+v, want exactly recordID %s", recordsResp.Records, recordID)
	}
	if recordsResp.Records[0].CurrentState != commonv1.RecordState_RECORD_STATE_RECOVERED.String() {
		t.Errorf("record current_state = %q, want RECOVERED", recordsResp.Records[0].CurrentState)
	}

	// --- Assert: GET /v1/records/{id}/audit shows a complete trail ending
	// in RECOVERED, translated field for field. ---
	var auditResp struct {
		CurrentState  string `json:"current_state"`
		TrailComplete bool   `json:"trail_complete"`
		Entries       []struct {
			FromState string `json:"from_state"`
			ToState   string `json:"to_state"`
		} `json:"entries"`
	}
	getJSON(ctx, t, "http://"+gwHTTPAddr+"/v1/records/"+recordID+"/audit", &auditResp)
	if auditResp.CurrentState != commonv1.RecordState_RECORD_STATE_RECOVERED.String() {
		t.Errorf("audit current_state = %q, want RECOVERED", auditResp.CurrentState)
	}
	if !auditResp.TrailComplete {
		t.Error("audit trail_complete = false, want true")
	}
	if len(auditResp.Entries) == 0 {
		t.Error("audit entries is empty, want at least one transition")
	}

	// --- Assert: GET /v1/invariants shows zero violations system-wide. ---
	var invariantsResp struct {
		StoppingRuleViolations int64 `json:"stopping_rule_violations"`
		IncompleteAuditTrails  int64 `json:"incomplete_audit_trails"`
		ImpossibleTransitions  int64 `json:"impossible_transitions"`
	}
	getJSON(ctx, t, "http://"+gwHTTPAddr+"/v1/invariants", &invariantsResp)
	if invariantsResp.StoppingRuleViolations != 0 || invariantsResp.IncompleteAuditTrails != 0 || invariantsResp.ImpossibleTransitions != 0 {
		t.Errorf("invariants = %+v, want all zero", invariantsResp)
	}
}
