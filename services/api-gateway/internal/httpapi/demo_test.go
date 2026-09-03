package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	worldsimv1 "github.com/thisizaro/Momotaro/proto/gen/worldsim/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// fakeWorldSimulator implements worldsimv1.WorldSimulatorServiceClient.
type fakeWorldSimulator struct {
	seedResp *worldsimv1.SeedBatchResponse
	seedErr  error
	gotSeed  *worldsimv1.SeedBatchRequest

	scenariosResp *worldsimv1.ListScenariosResponse
	scenariosErr  error

	worldStateResp *worldsimv1.GetWorldStateResponse
	worldStateErr  error

	poisonResp *worldsimv1.InjectPoisonResponse
	poisonErr  error
}

func (f *fakeWorldSimulator) SimulateOutcome(ctx context.Context, in *worldsimv1.SimulateOutcomeRequest, opts ...grpc.CallOption) (*worldsimv1.SimulateOutcomeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not used by the gateway")
}

func (f *fakeWorldSimulator) SeedBatch(ctx context.Context, in *worldsimv1.SeedBatchRequest, opts ...grpc.CallOption) (*worldsimv1.SeedBatchResponse, error) {
	f.gotSeed = in
	return f.seedResp, f.seedErr
}

func (f *fakeWorldSimulator) ListScenarios(ctx context.Context, in *worldsimv1.ListScenariosRequest, opts ...grpc.CallOption) (*worldsimv1.ListScenariosResponse, error) {
	if f.scenariosResp != nil || f.scenariosErr != nil {
		return f.scenariosResp, f.scenariosErr
	}
	return &worldsimv1.ListScenariosResponse{}, nil
}

func (f *fakeWorldSimulator) GetWorldState(ctx context.Context, in *worldsimv1.GetWorldStateRequest, opts ...grpc.CallOption) (*worldsimv1.GetWorldStateResponse, error) {
	if f.worldStateResp != nil || f.worldStateErr != nil {
		return f.worldStateResp, f.worldStateErr
	}
	return &worldsimv1.GetWorldStateResponse{}, nil
}

func (f *fakeWorldSimulator) InjectPoison(ctx context.Context, in *worldsimv1.InjectPoisonRequest, opts ...grpc.CallOption) (*worldsimv1.InjectPoisonResponse, error) {
	return f.poisonResp, f.poisonErr
}

func newHandlerWithDemo(f *fakeWorldSimulator) *Handler {
	h := New(&fakeIngestion{}, &fakeReporting{}, &fakeAudit{}, nil, testAPIKey, 2*time.Second, 0, 0)
	h.EnableDemoControls(f)
	return h
}

// The flag-gate contract (docs/PHASE5_5_IMPLEMENTATION.md Unit W): when
// demo controls are not enabled, EnableDemoControls is never called, so
// these routes must not exist at all, a 404, not a 401/403. A caller must
// not be able to tell the surface exists and is merely locked.
func TestDemoRoutesNotRegisteredWhenDisabled(t *testing.T) {
	h := New(&fakeIngestion{}, &fakeReporting{}, &fakeAudit{}, nil, testAPIKey, 2*time.Second, 0, 0).Routes()

	cases := []struct {
		method, path string
	}{
		{http.MethodPost, "/v1/demo/batches"},
		{http.MethodGet, "/v1/demo/scenarios"},
		{http.MethodGet, "/v1/demo/world"},
		{http.MethodPost, "/v1/demo/inject-poison"},
	}
	for _, tc := range cases {
		rec := doRequest(h, tc.method, tc.path, testAPIKey, `{}`)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s with demo controls disabled: status = %d, want 404", tc.method, tc.path, rec.Code)
		}
	}
}

func TestDemoRoutesRegisteredWhenEnabledStillRequireAPIKey(t *testing.T) {
	h := newHandlerWithDemo(&fakeWorldSimulator{}).Routes()

	cases := []struct {
		method, path string
	}{
		{http.MethodPost, "/v1/demo/batches"},
		{http.MethodGet, "/v1/demo/scenarios"},
		{http.MethodGet, "/v1/demo/world"},
		{http.MethodPost, "/v1/demo/inject-poison"},
	}
	for _, tc := range cases {
		rec := doRequest(h, tc.method, tc.path, "", `{}`)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s with no API key: status = %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
}

func TestSeedDemoBatchSuccess(t *testing.T) {
	fake := &fakeWorldSimulator{seedResp: &worldsimv1.SeedBatchResponse{BatchId: "batch-1", GeneratedCount: 20, Seed: 42}}
	h := newHandlerWithDemo(fake).Routes()

	rec := doRequest(h, http.MethodPost, "/v1/demo/batches", testAPIKey, `{"scenario":"dead-cards","count":20,"seed":42}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	var resp seedDemoBatchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.BatchID != "batch-1" || resp.GeneratedCount != 20 || resp.Seed != 42 {
		t.Errorf("resp = %+v, want batch-1/20/42", resp)
	}
	if fake.gotSeed == nil {
		t.Fatal("worldsim.SeedBatch was not called")
	}
	if fake.gotSeed.Scenario != "dead-cards" || fake.gotSeed.Count != 20 || fake.gotSeed.Seed != 42 {
		t.Errorf("gotSeed = %+v, want scenario=dead-cards count=20 seed=42", fake.gotSeed)
	}
}

func TestSeedDemoBatchDefaultsScenarioAndSeedWhenOmitted(t *testing.T) {
	fake := &fakeWorldSimulator{seedResp: &worldsimv1.SeedBatchResponse{BatchId: "batch-2", GeneratedCount: 80}}
	h := newHandlerWithDemo(fake).Routes()

	rec := doRequest(h, http.MethodPost, "/v1/demo/batches", testAPIKey, `{"count":80}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if fake.gotSeed.Scenario != "" {
		t.Errorf("gotSeed.Scenario = %q, want empty (server defaults to normal)", fake.gotSeed.Scenario)
	}
	if fake.gotSeed.Seed != 0 {
		t.Errorf("gotSeed.Seed = %d, want 0 (server picks one)", fake.gotSeed.Seed)
	}
}

func TestSeedDemoBatchRejectsMalformedJSON(t *testing.T) {
	h := newHandlerWithDemo(&fakeWorldSimulator{}).Routes()
	rec := doRequest(h, http.MethodPost, "/v1/demo/batches", testAPIKey, `not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestSeedDemoBatchPropagatesInvalidArgumentAs400(t *testing.T) {
	fake := &fakeWorldSimulator{seedErr: status.Error(codes.InvalidArgument, "unknown scenario \"bogus\"")}
	h := newHandlerWithDemo(fake).Routes()
	rec := doRequest(h, http.MethodPost, "/v1/demo/batches", testAPIKey, `{"scenario":"bogus","count":5}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

func TestSeedDemoBatchPropagatesUnavailableAs502(t *testing.T) {
	fake := &fakeWorldSimulator{seedErr: context.DeadlineExceeded}
	h := newHandlerWithDemo(fake).Routes()
	rec := doRequest(h, http.MethodPost, "/v1/demo/batches", testAPIKey, `{"scenario":"normal","count":5}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

func TestListDemoScenariosSuccess(t *testing.T) {
	fake := &fakeWorldSimulator{scenariosResp: &worldsimv1.ListScenariosResponse{
		Scenarios: []*worldsimv1.ScenarioPreset{
			{Name: "normal", Description: "the default mix"},
			{Name: "bank-outage", Description: "one bank, one code, one short window"},
		},
	}}
	h := newHandlerWithDemo(fake).Routes()

	rec := doRequest(h, http.MethodGet, "/v1/demo/scenarios", testAPIKey, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var resp listDemoScenariosResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Scenarios) != 2 {
		t.Fatalf("Scenarios = %+v, want 2", resp.Scenarios)
	}
	if resp.Scenarios[0].Name != "normal" || resp.Scenarios[0].Description != "the default mix" {
		t.Errorf("Scenarios[0] = %+v, want normal/the default mix", resp.Scenarios[0])
	}
}

func TestGetDemoWorldStateSuccess(t *testing.T) {
	dueAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	fake := &fakeWorldSimulator{worldStateResp: &worldsimv1.GetWorldStateResponse{
		Pending: []*worldsimv1.PendingOutcome{
			{RecordId: "rec-1", AttemptNumber: 2, Outcome: 1, DueAt: timestamppb.New(dueAt)},
		},
	}}
	h := newHandlerWithDemo(fake).Routes()

	rec := doRequest(h, http.MethodGet, "/v1/demo/world", testAPIKey, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var resp getDemoWorldStateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Pending) != 1 {
		t.Fatalf("Pending = %+v, want 1", resp.Pending)
	}
	got := resp.Pending[0]
	if got.RecordID != "rec-1" || got.AttemptNumber != 2 {
		t.Errorf("Pending[0] = %+v, want rec-1/2", got)
	}
	if got.DueAt != "2026-09-01T12:00:00Z" {
		t.Errorf("DueAt = %q, want RFC3339", got.DueAt)
	}
}

func TestInjectDemoPoisonSuccess(t *testing.T) {
	fake := &fakeWorldSimulator{poisonResp: &worldsimv1.InjectPoisonResponse{RecordId: "rec-poison", BatchId: "batch-poison"}}
	h := newHandlerWithDemo(fake).Routes()

	rec := doRequest(h, http.MethodPost, "/v1/demo/inject-poison", testAPIKey, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var resp injectDemoPoisonResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.RecordID != "rec-poison" || resp.BatchID != "batch-poison" {
		t.Errorf("resp = %+v, want rec-poison/batch-poison", resp)
	}
}

func TestInjectDemoPoisonPropagatesUnavailableAs502(t *testing.T) {
	fake := &fakeWorldSimulator{poisonErr: context.DeadlineExceeded}
	h := newHandlerWithDemo(fake).Routes()
	rec := doRequest(h, http.MethodPost, "/v1/demo/inject-poison", testAPIKey, "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}
