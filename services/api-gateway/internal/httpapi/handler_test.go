package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/webhooksig"
	auditv1 "github.com/thisizaro/Momotaro/proto/gen/audit/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	ingestionv1 "github.com/thisizaro/Momotaro/proto/gen/ingestion/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const testAPIKey = "test-demo-key"

// testWebhookSecret is the fixed secret every test Handler in this package
// verifies X-Razorpay-Signature against (see newHandler/newDowntimeHandler),
// so a test body signed with signBody(testWebhookSecret, ...) is accepted
// exactly the way a real Gateway with WEBHOOK_SECRET set would.
const testWebhookSecret = "test-webhook-secret"

// signBody returns the X-Razorpay-Signature value for body under
// testWebhookSecret, computed with the exact same webhooksig.Sign the
// Gateway itself verifies against, never a hand-rolled second
// implementation that could quietly drift from it.
func signBody(body string) string {
	return webhooksig.Sign(testWebhookSecret, []byte(body))
}

type fakeIngestion struct {
	resp *ingestionv1.SubmitBatchResponse
	err  error
	got  *ingestionv1.SubmitBatchRequest

	eventResp *ingestionv1.SubmitEventResponse
	eventErr  error
	gotEvent  *ingestionv1.SubmitEventRequest

	listBatchesResp *ingestionv1.ListBatchesResponse
	listBatchesErr  error
	gotListBatches  *ingestionv1.ListBatchesRequest
}

func (f *fakeIngestion) SubmitBatch(ctx context.Context, in *ingestionv1.SubmitBatchRequest, opts ...grpc.CallOption) (*ingestionv1.SubmitBatchResponse, error) {
	f.got = in
	return f.resp, f.err
}
func (f *fakeIngestion) SubmitEvent(ctx context.Context, in *ingestionv1.SubmitEventRequest, opts ...grpc.CallOption) (*ingestionv1.SubmitEventResponse, error) {
	f.gotEvent = in
	return f.eventResp, f.eventErr
}

func (f *fakeIngestion) ListBatches(ctx context.Context, in *ingestionv1.ListBatchesRequest, opts ...grpc.CallOption) (*ingestionv1.ListBatchesResponse, error) {
	f.gotListBatches = in
	if f.listBatchesResp != nil || f.listBatchesErr != nil {
		return f.listBatchesResp, f.listBatchesErr
	}
	return &ingestionv1.ListBatchesResponse{}, nil
}

func newHandler(f *fakeIngestion) http.Handler {
	h := New(f, &fakeReporting{}, &fakeAudit{}, nil, testAPIKey, 2*time.Second, 0, 0)
	h.SetWebhookSecrets(testWebhookSecret, "")
	return h.Routes()
}

// fakeAudit implements auditv1.AuditServiceClient. Defined here so both
// report_test.go and audit_test.go can construct a Handler with all three
// backend clients.
type fakeAudit struct {
	recordAuditResp *auditv1.GetRecordAuditResponse
	recordAuditErr  error
	gotRecordAudit  *auditv1.GetRecordAuditRequest

	invariantsResp *auditv1.VerifyInvariantsResponse
	invariantsErr  error
	gotInvariants  *auditv1.VerifyInvariantsRequest
}

func (f *fakeAudit) GetRecordAudit(ctx context.Context, in *auditv1.GetRecordAuditRequest, opts ...grpc.CallOption) (*auditv1.GetRecordAuditResponse, error) {
	f.gotRecordAudit = in
	return f.recordAuditResp, f.recordAuditErr
}

func (f *fakeAudit) VerifyInvariants(ctx context.Context, in *auditv1.VerifyInvariantsRequest, opts ...grpc.CallOption) (*auditv1.VerifyInvariantsResponse, error) {
	f.gotInvariants = in
	return f.invariantsResp, f.invariantsErr
}

// notFoundErr builds a gRPC NotFound status error, the shape every
// downstream service returns for an unknown batch/record_id, so Gateway
// tests can exercise the "translate a real gRPC status into the right
// HTTP code" path without a real server.
func notFoundErr(msg string) error {
	return status.Error(codes.NotFound, msg)
}

// doRequest signs body with testWebhookSecret unconditionally: the two
// webhook routes require a valid X-Razorpay-Signature and every other route
// simply ignores the header, so setting it here means every existing
// caller of doRequest that posts to a webhook route keeps working without
// having to know about signing, while a test that specifically wants an
// unsigned or wrongly-signed request builds it by hand (see
// signature_test.go).
func doRequest(h http.Handler, method, path, apiKey, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	req.Header.Set("Content-Type", "application/json")
	if body != "" {
		req.Header.Set(razorpaySignatureHeader, signBody(body))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestCORSPreflightSucceedsWithoutAPIKey(t *testing.T) {
	h := newHandler(&fakeIngestion{})
	req := httptest.NewRequest(http.MethodOptions, "/v1/batches", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "X-API-Key, Content-Type")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (a preflight never carries X-API-Key, so this must not 401)", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Error("Access-Control-Allow-Headers is empty, want it to at least cover X-API-Key")
	}
}

func TestCORSHeaderPresentOnRealResponse(t *testing.T) {
	fake := &fakeIngestion{resp: &ingestionv1.SubmitBatchResponse{BatchId: "batch-1", AcceptedCount: 1}}
	h := newHandler(fake)
	rec := doRequest(h, http.MethodPost, "/v1/batches", testAPIKey, `{"records":[{"type":"PAYMENT","amount_paise":1,"failure_code":"X"}]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want * (a browser discards the response body without this, even on 200)", got)
	}
}

func TestSubmitBatchRequiresAPIKey(t *testing.T) {
	h := newHandler(&fakeIngestion{})
	rec := doRequest(h, http.MethodPost, "/v1/batches", "", `{"records":[]}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestSubmitBatchRejectsWrongAPIKey(t *testing.T) {
	h := newHandler(&fakeIngestion{})
	rec := doRequest(h, http.MethodPost, "/v1/batches", "wrong-key", `{"records":[]}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestSubmitBatchSuccess(t *testing.T) {
	fake := &fakeIngestion{resp: &ingestionv1.SubmitBatchResponse{BatchId: "batch-1", AcceptedCount: 1}}
	h := newHandler(fake)

	body := `{"source":"demo","records":[{"type":"PAYMENT","amount_paise":50000,"currency":"INR","failure_code":"BANK_TIMEOUT","instrument_ref":"card_1"}]}`
	rec := doRequest(h, http.MethodPost, "/v1/batches", testAPIKey, body)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	var resp submitBatchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.BatchID != "batch-1" {
		t.Errorf("BatchID = %q, want batch-1", resp.BatchID)
	}
	if resp.AcceptedCount != 1 {
		t.Errorf("AcceptedCount = %d, want 1", resp.AcceptedCount)
	}

	if fake.got == nil {
		t.Fatal("ingestion.SubmitBatch was not called")
	}
	if fake.got.Source != "demo" {
		t.Errorf("Source = %q, want demo", fake.got.Source)
	}
	if len(fake.got.Records) != 1 {
		t.Fatalf("Records = %d, want 1", len(fake.got.Records))
	}
	rc := fake.got.Records[0]
	if rc.Type != commonv1.RecordType_RECORD_TYPE_PAYMENT {
		t.Errorf("Type = %v, want RECORD_TYPE_PAYMENT", rc.Type)
	}
	if rc.AmountPaise != 50000 {
		t.Errorf("AmountPaise = %d, want 50000", rc.AmountPaise)
	}
	if rc.FailureCode != "BANK_TIMEOUT" {
		t.Errorf("FailureCode = %q, want BANK_TIMEOUT", rc.FailureCode)
	}
}

func TestSubmitBatchRejectsEmptyRecords(t *testing.T) {
	h := newHandler(&fakeIngestion{})
	rec := doRequest(h, http.MethodPost, "/v1/batches", testAPIKey, `{"source":"demo","records":[]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestSubmitBatchRejectsMalformedJSON(t *testing.T) {
	h := newHandler(&fakeIngestion{})
	rec := doRequest(h, http.MethodPost, "/v1/batches", testAPIKey, `not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestSubmitBatchPropagatesIngestionError(t *testing.T) {
	fake := &fakeIngestion{err: context.DeadlineExceeded}
	h := newHandler(fake)
	rec := doRequest(h, http.MethodPost, "/v1/batches", testAPIKey, `{"records":[{"type":"PAYMENT","amount_paise":1,"failure_code":"X"}]}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

func TestSubmitBatchPropagatesRejectedRecords(t *testing.T) {
	fake := &fakeIngestion{resp: &ingestionv1.SubmitBatchResponse{
		BatchId:       "batch-2",
		AcceptedCount: 0,
		Rejected:      map[int32]string{0: "amount_paise must be positive"},
	}}
	h := newHandler(fake)
	rec := doRequest(h, http.MethodPost, "/v1/batches", testAPIKey, `{"records":[{"type":"PAYMENT","amount_paise":0,"failure_code":"X"}]}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var resp submitBatchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Rejected["0"] != "amount_paise must be positive" {
		t.Errorf("Rejected = %v, want index 0 populated", resp.Rejected)
	}
}

func TestWebhookRequiresAPIKey(t *testing.T) {
	h := newHandler(&fakeIngestion{})
	body := `{"type":"PAYMENT","amount_paise":1000,"failure_code":"BANK_TIMEOUT"}`
	rec := doRequest(h, http.MethodPost, "/v1/webhooks/payment-failed", "", body)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestWebhookSuccess(t *testing.T) {
	fake := &fakeIngestion{eventResp: &ingestionv1.SubmitEventResponse{RecordId: "rec-1", BatchId: "batch-1"}}
	h := newHandler(fake)

	body := `{"type":"MANDATE","amount_paise":250000,"currency":"INR","failure_code":"INSUFFICIENT_FUNDS","instrument_ref":"mandate_1","idempotency_key":"evt-abc"}`
	rec := doRequest(h, http.MethodPost, "/v1/webhooks/payment-failed", testAPIKey, body)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202, body=%s", rec.Code, rec.Body.String())
	}

	var resp submitEventResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.RecordID != "rec-1" {
		t.Errorf("RecordID = %q, want rec-1", resp.RecordID)
	}

	if fake.gotEvent == nil {
		t.Fatal("ingestion.SubmitEvent was not called")
	}
	if fake.gotEvent.IdempotencyKey != "evt-abc" {
		t.Errorf("IdempotencyKey = %q, want evt-abc", fake.gotEvent.IdempotencyKey)
	}
	rc := fake.gotEvent.Record
	if rc.Type != commonv1.RecordType_RECORD_TYPE_MANDATE {
		t.Errorf("Type = %v, want RECORD_TYPE_MANDATE", rc.Type)
	}
	if rc.AmountPaise != 250000 {
		t.Errorf("AmountPaise = %d, want 250000", rc.AmountPaise)
	}
	if rc.InstrumentRef != "mandate_1" {
		t.Errorf("InstrumentRef = %q, want mandate_1", rc.InstrumentRef)
	}
}

// TestWebhookForwardsErrorTaxonomyFields proves the five additional fields
// (docs/PHASE5_5_IMPLEMENTATION.md Unit Z) reach Ingestion's NewRecord
// alongside failure_code, using the exact values from Razorpay's own
// documented payment.failed example
// (https://razorpay.com/docs/webhooks/payloads/payments/).
func TestWebhookForwardsErrorTaxonomyFields(t *testing.T) {
	fake := &fakeIngestion{eventResp: &ingestionv1.SubmitEventResponse{RecordId: "rec-1", BatchId: "batch-1"}}
	h := newHandler(fake)

	body := `{
		"type":"PAYMENT","amount_paise":50000,"currency":"INR",
		"failure_code":"payment_failed","instrument_ref":"card_ref_1",
		"error_code":"BAD_REQUEST_ERROR",
		"error_description":"Payment failed",
		"error_source":"bank",
		"error_step":"payment_authorization",
		"error_reason":"payment_failed"
	}`
	rec := doRequest(h, http.MethodPost, "/v1/webhooks/payment-failed", testAPIKey, body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202, body=%s", rec.Code, rec.Body.String())
	}

	if fake.gotEvent == nil {
		t.Fatal("ingestion.SubmitEvent was not called")
	}
	rc := fake.gotEvent.Record
	if rc.ErrorCode != "BAD_REQUEST_ERROR" {
		t.Errorf("ErrorCode = %q, want BAD_REQUEST_ERROR", rc.ErrorCode)
	}
	if rc.ErrorDescription != "Payment failed" {
		t.Errorf("ErrorDescription = %q, want %q", rc.ErrorDescription, "Payment failed")
	}
	if rc.ErrorSource != "bank" {
		t.Errorf("ErrorSource = %q, want bank", rc.ErrorSource)
	}
	if rc.ErrorStep != "payment_authorization" {
		t.Errorf("ErrorStep = %q, want payment_authorization", rc.ErrorStep)
	}
	if rc.ErrorReason != "payment_failed" {
		t.Errorf("ErrorReason = %q, want payment_failed", rc.ErrorReason)
	}
}

// TestWebhookAcceptsAnUnrecognisedErrorSource proves error_source/error_step
// are genuinely open vocabularies at the Gateway layer: a value never seen
// in Razorpay's documented examples must still be accepted and forwarded
// verbatim, never rejected as invalid (docs/PHASE5_5_IMPLEMENTATION.md Unit
// Z: "treat these as open string vocabularies... handle an unrecognised
// value without crashing or discarding the record").
func TestWebhookAcceptsAnUnrecognisedErrorSource(t *testing.T) {
	fake := &fakeIngestion{eventResp: &ingestionv1.SubmitEventResponse{RecordId: "rec-1", BatchId: "batch-1"}}
	h := newHandler(fake)

	body := `{
		"type":"PAYMENT","amount_paise":50000,"failure_code":"payment_failed",
		"error_source":"upi_intent_app_never_documented",
		"error_step":"a_future_step_this_gateway_has_never_seen"
	}`
	rec := doRequest(h, http.MethodPost, "/v1/webhooks/payment-failed", testAPIKey, body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 for an unrecognised error_source/error_step, body=%s", rec.Code, rec.Body.String())
	}
	if fake.gotEvent.Record.ErrorSource != "upi_intent_app_never_documented" {
		t.Errorf("ErrorSource = %q, want it forwarded verbatim, not rejected", fake.gotEvent.Record.ErrorSource)
	}
}

func TestWebhookRejectsMalformedJSON(t *testing.T) {
	h := newHandler(&fakeIngestion{})
	rec := doRequest(h, http.MethodPost, "/v1/webhooks/payment-failed", testAPIKey, `not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestWebhookRejectsMissingFailureCode(t *testing.T) {
	h := newHandler(&fakeIngestion{})
	body := `{"type":"PAYMENT","amount_paise":1000}`
	rec := doRequest(h, http.MethodPost, "/v1/webhooks/payment-failed", testAPIKey, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestWebhookPropagatesIngestionError(t *testing.T) {
	fake := &fakeIngestion{eventErr: context.DeadlineExceeded}
	h := newHandler(fake)
	body := `{"type":"PAYMENT","amount_paise":1000,"failure_code":"BANK_TIMEOUT"}`
	rec := doRequest(h, http.MethodPost, "/v1/webhooks/payment-failed", testAPIKey, body)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

func TestWebhookDeduplicated(t *testing.T) {
	fake := &fakeIngestion{eventResp: &ingestionv1.SubmitEventResponse{RecordId: "rec-1", Deduplicated: true}}
	h := newHandler(fake)
	body := `{"type":"PAYMENT","amount_paise":1000,"failure_code":"BANK_TIMEOUT","idempotency_key":"evt-abc"}`
	rec := doRequest(h, http.MethodPost, "/v1/webhooks/payment-failed", testAPIKey, body)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 even when deduplicated, body=%s", rec.Code, rec.Body.String())
	}
	var resp submitEventResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.RecordID != "rec-1" {
		t.Errorf("RecordID = %q, want rec-1", resp.RecordID)
	}
}

func TestRateLimitAllowsWithinBurst(t *testing.T) {
	fake := &fakeIngestion{resp: &ingestionv1.SubmitBatchResponse{BatchId: "b1", AcceptedCount: 1}}
	h := New(fake, &fakeReporting{}, &fakeAudit{}, nil, testAPIKey, 2*time.Second, 100, 2).Routes()

	body := `{"records":[{"type":"PAYMENT","amount_paise":1,"failure_code":"X"}]}`
	for i := 0; i < 2; i++ {
		rec := doRequest(h, http.MethodPost, "/v1/batches", testAPIKey, body)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200, body=%s", i, rec.Code, rec.Body.String())
		}
	}
}

func TestRateLimitRejectsOverBurst(t *testing.T) {
	fake := &fakeIngestion{resp: &ingestionv1.SubmitBatchResponse{BatchId: "b1", AcceptedCount: 1}}
	// A tiny sustained rate with a burst of 1: the first request consumes the
	// only token, the second must be rejected before the bucket refills.
	h := New(fake, &fakeReporting{}, &fakeAudit{}, nil, testAPIKey, 2*time.Second, 1, 1).Routes()

	body := `{"records":[{"type":"PAYMENT","amount_paise":1,"failure_code":"X"}]}`
	rec1 := doRequest(h, http.MethodPost, "/v1/batches", testAPIKey, body)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request: status = %d, want 200, body=%s", rec1.Code, rec1.Body.String())
	}
	rec2 := doRequest(h, http.MethodPost, "/v1/batches", testAPIKey, body)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: status = %d, want 429, body=%s", rec2.Code, rec2.Body.String())
	}
}

func TestRateLimitZeroDisables(t *testing.T) {
	fake := &fakeIngestion{resp: &ingestionv1.SubmitBatchResponse{BatchId: "b1", AcceptedCount: 1}}
	h := newHandler(fake) // rps=0, burst=0 means disabled

	body := `{"records":[{"type":"PAYMENT","amount_paise":1,"failure_code":"X"}]}`
	for i := 0; i < 5; i++ {
		rec := doRequest(h, http.MethodPost, "/v1/batches", testAPIKey, body)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200 (limiter disabled), body=%s", i, rec.Code, rec.Body.String())
		}
	}
}
