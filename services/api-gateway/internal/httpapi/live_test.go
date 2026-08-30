package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	reportingv1 "github.com/thisizaro/Momotaro/proto/gen/reporting/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// fakeStreamClient implements grpc.ServerStreamingClient[StreamBatchUpdatesResponse]
// (Recv, plus grpc.ClientStream embedded for the rest, none of which
// liveUpdates calls). Feeds a fixed sequence of responses, then io.EOF
// unless a different terminal error is set.
type fakeStreamClient struct {
	grpc.ClientStream
	responses []*reportingv1.StreamBatchUpdatesResponse
	idx       int
	tailErr   error
}

func (f *fakeStreamClient) Recv() (*reportingv1.StreamBatchUpdatesResponse, error) {
	if f.idx < len(f.responses) {
		r := f.responses[f.idx]
		f.idx++
		return r, nil
	}
	if f.tailErr != nil {
		return nil, f.tailErr
	}
	return nil, io.EOF
}

func newLiveTestServer(t *testing.T, rep *fakeReporting) *httptest.Server {
	t.Helper()
	h := New(&fakeIngestion{}, rep, &fakeAudit{}, testAPIKey, 2*time.Second, 0, 0)
	srv := httptest.NewServer(h.Routes())
	t.Cleanup(srv.Close)
	return srv
}

func wsURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http")
}

func TestLiveUpdatesRelaysMessagesInDocumentedShape(t *testing.T) {
	rep := &fakeReporting{streamResp: &fakeStreamClient{responses: []*reportingv1.StreamBatchUpdatesResponse{
		{Update: &reportingv1.BatchUpdate{
			RecordId:            "rec-1",
			FromState:           commonv1.RecordState_RECORD_STATE_NUDGED,
			ToState:             commonv1.RecordState_RECORD_STATE_RECOVERED,
			Ts:                  timestamppb.New(time.Date(2026, 8, 29, 14, 12, 0, 0, time.UTC)),
			RecoveredDeltaPaise: 50000,
		}},
	}}}
	srv := newLiveTestServer(t, rep)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(srv.URL)+"/v1/batches/batch-1/live", &websocket.DialOptions{
		Subprotocols: []string{testAPIKey},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	var got map[string]any
	if err := wsjson.Read(ctx, conn, &got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if got["record_id"] != "rec-1" || got["from_state"] != "RECORD_STATE_NUDGED" || got["to_state"] != "RECORD_STATE_RECOVERED" {
		t.Errorf("message = %v", got)
	}
	if got["ts"] != "2026-08-29T14:12:00Z" {
		t.Errorf("ts = %v", got["ts"])
	}
	if got["recovered_delta_paise"] != float64(50000) {
		t.Errorf("recovered_delta_paise = %v, want 50000", got["recovered_delta_paise"])
	}

	if rep.gotStream.GetBatchId() != "batch-1" {
		t.Errorf("proxied batch_id = %q, want batch-1", rep.gotStream.GetBatchId())
	}
}

func TestLiveUpdatesRecoveredDeltaZeroIsPresentNotOmitted(t *testing.T) {
	rep := &fakeReporting{streamResp: &fakeStreamClient{responses: []*reportingv1.StreamBatchUpdatesResponse{
		{Update: &reportingv1.BatchUpdate{
			RecordId:  "rec-2",
			FromState: commonv1.RecordState_RECORD_STATE_NEW,
			ToState:   commonv1.RecordState_RECORD_STATE_SCORING,
			Ts:        timestamppb.New(time.Now()),
		}},
	}}}
	srv := newLiveTestServer(t, rep)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(srv.URL)+"/v1/batches/batch-1/live", &websocket.DialOptions{
		Subprotocols: []string{testAPIKey},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	_, raw, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(raw), `"recovered_delta_paise":0`) {
		t.Errorf("body = %s, want recovered_delta_paise present as 0, not omitted", raw)
	}
}

func TestLiveUpdatesClosesNormallyWhenUpstreamStreamEnds(t *testing.T) {
	rep := &fakeReporting{streamResp: &fakeStreamClient{}} // Recv returns io.EOF immediately
	srv := newLiveTestServer(t, rep)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(srv.URL)+"/v1/batches/batch-1/live", &websocket.DialOptions{
		Subprotocols: []string{testAPIKey},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	_, _, err = conn.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusNormalClosure {
		t.Errorf("close status = %v (err %v), want StatusNormalClosure", websocket.CloseStatus(err), err)
	}
}

func TestLiveUpdatesMissingSubprotocolIsUnauthorized(t *testing.T) {
	rep := &fakeReporting{}
	srv := newLiveTestServer(t, rep)

	resp, err := http.Get(srv.URL + "/v1/batches/batch-1/live")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := body["error"]; !ok {
		t.Errorf("body = %v, want an error envelope", body)
	}
}

func TestLiveUpdatesWrongSubprotocolIsUnauthorized(t *testing.T) {
	rep := &fakeReporting{}
	srv := newLiveTestServer(t, rep)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, wsURL(srv.URL)+"/v1/batches/batch-1/live", &websocket.DialOptions{
		Subprotocols: []string{"wrong-key"},
	})
	if err == nil {
		t.Fatal("dial succeeded, want a rejected handshake")
	}
	if resp != nil && resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestLiveUpdatesReportingUnavailableClosesWithInternalError(t *testing.T) {
	rep := &fakeReporting{streamErr: errors.New("reporting down")}
	srv := newLiveTestServer(t, rep)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(srv.URL)+"/v1/batches/batch-1/live", &websocket.DialOptions{
		Subprotocols: []string{testAPIKey},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	_, _, err = conn.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusInternalError {
		t.Errorf("close status = %v (err %v), want StatusInternalError", websocket.CloseStatus(err), err)
	}
}
