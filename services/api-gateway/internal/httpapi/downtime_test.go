package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	decisionenginev1 "github.com/thisizaro/Momotaro/proto/gen/decisionengine/v1"
	"google.golang.org/grpc"
)

// fakeDecisionEngine implements decisionenginev1.DecisionEngineServiceClient,
// standing in for a real gRPC connection to the Decision Engine, the same
// pattern fakeIngestion/fakeReporting/fakeAudit already use in this package.
type fakeDecisionEngine struct {
	resp *decisionenginev1.ReportDowntimeEventResponse
	err  error
	got  *decisionenginev1.ReportDowntimeEventRequest

	// configResp/configErr back GetAgentConfig (docs/DEMO_READINESS.md Unit
	// AM), used by demo_config_test.go. Separate from resp/err above since
	// the two RPCs return unrelated response types.
	configResp *decisionenginev1.GetAgentConfigResponse
	configErr  error
}

func (f *fakeDecisionEngine) ReportDelayedOutcome(ctx context.Context, in *decisionenginev1.ReportDelayedOutcomeRequest, opts ...grpc.CallOption) (*decisionenginev1.ReportDelayedOutcomeResponse, error) {
	return nil, nil // unused by this route
}

func (f *fakeDecisionEngine) ReportDowntimeEvent(ctx context.Context, in *decisionenginev1.ReportDowntimeEventRequest, opts ...grpc.CallOption) (*decisionenginev1.ReportDowntimeEventResponse, error) {
	f.got = in
	if f.resp != nil || f.err != nil {
		return f.resp, f.err
	}
	return &decisionenginev1.ReportDowntimeEventResponse{Applied: true}, nil
}

func (f *fakeDecisionEngine) GetAgentConfig(ctx context.Context, in *decisionenginev1.GetAgentConfigRequest, opts ...grpc.CallOption) (*decisionenginev1.GetAgentConfigResponse, error) {
	if f.configResp != nil || f.configErr != nil {
		return f.configResp, f.configErr
	}
	return &decisionenginev1.GetAgentConfigResponse{}, nil
}

func newDowntimeHandler(f *fakeDecisionEngine) http.Handler {
	h := New(&fakeIngestion{}, &fakeReporting{}, &fakeAudit{}, f, testAPIKey, 2*time.Second, 0, 0)
	h.SetWebhookSecrets(testWebhookSecret, "")
	return h.Routes()
}

func postDowntime(h http.Handler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/payment-downtime", strings.NewReader(body))
	req.Header.Set("X-API-Key", testAPIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(razorpaySignatureHeader, signBody(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// razorpayDowntimeStartedPayload is fetched verbatim from Razorpay's own
// docs (https://razorpay.com/docs/webhooks/payloads/payments/), matched
// exactly (docs/PHASE5_5_IMPLEMENTATION.md Unit Y).
const razorpayDowntimeStartedPayload = `{
  "entity": "event",
  "account_id": "acc_CWX291oykl9aZA",
  "event": "payment.downtime.started",
  "contains": ["payment.downtime"],
  "payload": {
    "payment.downtime": {
      "entity": {
        "id": "down_F1Zppa6lcVheSE",
        "entity": "payment.downtime",
        "method": "netbanking",
        "begin": 1591935238,
        "end": null,
        "status": "started",
        "scheduled": false,
        "severity": "high",
        "instrument": { "bank": "VIJB" },
        "instrument_schema": ["bank"],
        "created_at": 1591935238,
        "updated_at": 1591935238
      }
    }
  },
  "created_at": 1591935238
}`

func TestSubmitDowntimeEventAcceptsRazorpaysRealStartedPayload(t *testing.T) {
	fake := &fakeDecisionEngine{}
	h := newDowntimeHandler(fake)

	rec := postDowntime(h, razorpayDowntimeStartedPayload)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200", rec.Code, rec.Body.String())
	}

	if fake.got == nil {
		t.Fatal("decision engine was never called")
	}
	if fake.got.DowntimeId != "down_F1Zppa6lcVheSE" {
		t.Errorf("DowntimeId = %q", fake.got.DowntimeId)
	}
	if fake.got.Method != "netbanking" {
		t.Errorf("Method = %q", fake.got.Method)
	}
	if fake.got.Status != "started" {
		t.Errorf("Status = %q", fake.got.Status)
	}
	if fake.got.Scheduled {
		t.Error("Scheduled = true, want false")
	}
	if fake.got.Severity != "high" {
		t.Errorf("Severity = %q", fake.got.Severity)
	}
	if fake.got.BeginUnix != 1591935238 {
		t.Errorf("BeginUnix = %d, want 1591935238 (UNIX SECONDS, not milliseconds)", fake.got.BeginUnix)
	}
	if fake.got.InstrumentKey != "VIJB" {
		t.Errorf("InstrumentKey = %q, want %q extracted from instrument.bank via instrument_schema", fake.got.InstrumentKey, "VIJB")
	}

	var resp submitDowntimeEventResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.DowntimeID != "down_F1Zppa6lcVheSE" {
		t.Errorf("response DowntimeID = %q", resp.DowntimeID)
	}
	if !resp.Applied {
		t.Error("response Applied = false, want true")
	}
}

// end: null while a downtime is ongoing must become HasEnd=false, never a
// zero/garbage EndUnix (docs/PHASE5_5_IMPLEMENTATION.md Unit Y: "do not
// model it as a non-nullable timestamp").
func TestSubmitDowntimeEventLeavesEndUnsetWhenNull(t *testing.T) {
	fake := &fakeDecisionEngine{}
	h := newDowntimeHandler(fake)

	postDowntime(h, razorpayDowntimeStartedPayload)

	if fake.got.HasEnd {
		t.Errorf("HasEnd = true, want false: entity.end was null")
	}
}

// A resolved event with a real end timestamp must set HasEnd/EndUnix
// correctly, in UNIX SECONDS.
func TestSubmitDowntimeEventForwardsAConcreteEnd(t *testing.T) {
	fake := &fakeDecisionEngine{}
	h := newDowntimeHandler(fake)

	body := `{
		"entity": "event", "account_id": "acc_1", "event": "payment.downtime.resolved",
		"contains": ["payment.downtime"],
		"payload": {"payment.downtime": {"entity": {
			"id": "down_F1Zppa6lcVheSE", "entity": "payment.downtime", "method": "netbanking",
			"begin": 1591935238, "end": 1591938838, "status": "resolved", "scheduled": false,
			"severity": "high", "instrument": {"bank": "VIJB"}, "instrument_schema": ["bank"],
			"created_at": 1591935238, "updated_at": 1591938838
		}}},
		"created_at": 1591938838
	}`
	rec := postDowntime(h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !fake.got.HasEnd {
		t.Fatal("HasEnd = false, want true: entity.end was a real timestamp")
	}
	if fake.got.EndUnix != 1591938838 {
		t.Errorf("EndUnix = %d, want 1591938838", fake.got.EndUnix)
	}
	if fake.got.Status != "resolved" {
		t.Errorf("Status = %q, want resolved", fake.got.Status)
	}
}

// An unrecognised severity must be accepted, not rejected: the documented
// list (high/medium) is not exhaustive (docs/PHASE5_5_IMPLEMENTATION.md Unit
// Y: "do not assume that list is exhaustive; handle an unknown value
// without crashing").
func TestSubmitDowntimeEventAcceptsAnUnrecognisedSeverity(t *testing.T) {
	fake := &fakeDecisionEngine{}
	h := newDowntimeHandler(fake)

	body := `{
		"entity": "event", "account_id": "acc_1", "event": "payment.downtime.started",
		"contains": ["payment.downtime"],
		"payload": {"payment.downtime": {"entity": {
			"id": "down_1", "entity": "payment.downtime", "method": "netbanking",
			"begin": 1591935238, "end": null, "status": "started", "scheduled": false,
			"severity": "critical", "instrument": {"bank": "VIJB"}, "instrument_schema": ["bank"],
			"created_at": 1591935238, "updated_at": 1591935238
		}}},
		"created_at": 1591935238
	}`
	rec := postDowntime(h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s, want 200 for an unrecognised (but present) severity", rec.Code, rec.Body.String())
	}
	if fake.got.Severity != "critical" {
		t.Errorf("Severity = %q, want it forwarded verbatim, not rejected or normalised", fake.got.Severity)
	}
}

// A card issuer-shaped instrument: {"issuer": "SBIN", "type": "credit"}.
// "type" is a qualifier, never itself a matchable identity.
func TestSubmitDowntimeEventExtractsInstrumentKeyFromACardIssuerShape(t *testing.T) {
	fake := &fakeDecisionEngine{}
	h := newDowntimeHandler(fake)

	body := `{
		"entity": "event", "account_id": "acc_1", "event": "payment.downtime.started",
		"contains": ["payment.downtime"],
		"payload": {"payment.downtime": {"entity": {
			"id": "down_2", "entity": "payment.downtime", "method": "card",
			"begin": 1591935238, "end": null, "status": "started", "scheduled": false,
			"severity": "high", "instrument": {"issuer": "SBIN", "type": "credit"},
			"instrument_schema": ["issuer", "type"],
			"created_at": 1591935238, "updated_at": 1591935238
		}}},
		"created_at": 1591935238
	}`
	rec := postDowntime(h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if fake.got.InstrumentKey != "SBIN" {
		t.Errorf("InstrumentKey = %q, want %q", fake.got.InstrumentKey, "SBIN")
	}
}

// A card network-shaped instrument: {"network": "MC", "type": "credit"}.
// Different shape again, still no hardcoding of a single instrument layout.
func TestSubmitDowntimeEventExtractsInstrumentKeyFromACardNetworkShape(t *testing.T) {
	fake := &fakeDecisionEngine{}
	h := newDowntimeHandler(fake)

	body := `{
		"entity": "event", "account_id": "acc_1", "event": "payment.downtime.started",
		"contains": ["payment.downtime"],
		"payload": {"payment.downtime": {"entity": {
			"id": "down_3", "entity": "payment.downtime", "method": "card",
			"begin": 1591935238, "end": null, "status": "started", "scheduled": false,
			"severity": "high", "instrument": {"network": "MC", "type": "credit"},
			"instrument_schema": ["network", "type"],
			"created_at": 1591935238, "updated_at": 1591935238
		}}},
		"created_at": 1591935238
	}`
	rec := postDowntime(h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if fake.got.InstrumentKey != "MC" {
		t.Errorf("InstrumentKey = %q, want %q", fake.got.InstrumentKey, "MC")
	}
}

func TestSubmitDowntimeEventScheduledFieldIsForwarded(t *testing.T) {
	fake := &fakeDecisionEngine{}
	h := newDowntimeHandler(fake)

	body := `{
		"entity": "event", "account_id": "acc_1", "event": "payment.downtime.started",
		"contains": ["payment.downtime"],
		"payload": {"payment.downtime": {"entity": {
			"id": "down_4", "entity": "payment.downtime", "method": "netbanking",
			"begin": 1591935238, "end": 1591999999, "status": "started", "scheduled": true,
			"severity": "medium", "instrument": {"bank": "VIJB"}, "instrument_schema": ["bank"],
			"created_at": 1591935238, "updated_at": 1591935238
		}}},
		"created_at": 1591935238
	}`
	rec := postDowntime(h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !fake.got.Scheduled {
		t.Error("Scheduled = false, want true: this is planned maintenance, not an unplanned outage")
	}
}

func TestSubmitDowntimeEventRejectsMalformedJSON(t *testing.T) {
	h := newDowntimeHandler(&fakeDecisionEngine{})
	rec := postDowntime(h, `{not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestSubmitDowntimeEventRejectsMissingDowntimeID(t *testing.T) {
	h := newDowntimeHandler(&fakeDecisionEngine{})
	body := `{"payload": {"payment.downtime": {"entity": {
		"method": "netbanking", "begin": 1591935238, "status": "started", "instrument": {"bank": "VIJB"}, "instrument_schema": ["bank"]
	}}}}`
	rec := postDowntime(h, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s, want 400", rec.Code, rec.Body.String())
	}
}

func TestSubmitDowntimeEventRejectsMissingMethod(t *testing.T) {
	h := newDowntimeHandler(&fakeDecisionEngine{})
	body := `{"payload": {"payment.downtime": {"entity": {
		"id": "down_1", "begin": 1591935238, "status": "started", "instrument": {"bank": "VIJB"}, "instrument_schema": ["bank"]
	}}}}`
	rec := postDowntime(h, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s, want 400", rec.Code, rec.Body.String())
	}
}

func TestSubmitDowntimeEventRejectsAnUnrecognisedStatus(t *testing.T) {
	h := newDowntimeHandler(&fakeDecisionEngine{})
	body := `{"payload": {"payment.downtime": {"entity": {
		"id": "down_1", "method": "netbanking", "begin": 1591935238, "status": "cancelled",
		"instrument": {"bank": "VIJB"}, "instrument_schema": ["bank"]
	}}}}`
	rec := postDowntime(h, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s, want 400: status must be started/updated/resolved", rec.Code, rec.Body.String())
	}
}

func TestSubmitDowntimeEventRejectsMissingBegin(t *testing.T) {
	h := newDowntimeHandler(&fakeDecisionEngine{})
	body := `{"payload": {"payment.downtime": {"entity": {
		"id": "down_1", "method": "netbanking", "status": "started",
		"instrument": {"bank": "VIJB"}, "instrument_schema": ["bank"]
	}}}}`
	rec := postDowntime(h, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s, want 400", rec.Code, rec.Body.String())
	}
}

func TestSubmitDowntimeEventReturns502WhenDecisionEngineIsUnavailable(t *testing.T) {
	fake := &fakeDecisionEngine{err: context.DeadlineExceeded}
	h := newDowntimeHandler(fake)

	rec := postDowntime(h, razorpayDowntimeStartedPayload)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

// A body larger than the cap must be rejected cleanly (400), never read
// without bound (docs/PHASE5_5_IMPLEMENTATION.md Unit Y: "cap the body
// read"), and the cap is enforced before signature verification even runs
// (signature.go), so this deliberately sends no signature at all: the
// oversized body must still be the reason for the 400, not a side effect of
// also being unsigned.
func TestSubmitDowntimeEventRejectsAnOversizedBody(t *testing.T) {
	h := newDowntimeHandler(&fakeDecisionEngine{})

	huge := bytes.Repeat([]byte(" "), maxWebhookBodyBytes+1)
	body := `{"payload":` + string(huge) + `}`
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/payment-downtime", strings.NewReader(body))
	req.Header.Set("X-API-Key", testAPIKey)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an oversized body", rec.Code)
	}
}

func TestSubmitDowntimeEventRequiresAPIKey(t *testing.T) {
	h := newDowntimeHandler(&fakeDecisionEngine{})
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/payment-downtime", strings.NewReader(razorpayDowntimeStartedPayload))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without X-API-Key", rec.Code)
	}
}
