package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/clock"
)

func testLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, nil))
}

func TestRunSendsExactlyRequestedCount(t *testing.T) {
	var accepted int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accepted++
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	cfg := runConfig{
		url:        srv.URL,
		apiKey:     "k",
		rate:       1000, // fast: this test only checks counting, not pacing
		count:      5,
		httpClient: &http.Client{Timeout: 2 * time.Second},
		clk:        clock.New(),
		logger:     testLogger(&buf),
	}

	summary, err := run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if summary.Sent != 5 {
		t.Errorf("Sent = %d, want 5", summary.Sent)
	}
	if summary.Accepted != 5 {
		t.Errorf("Accepted = %d, want 5", summary.Accepted)
	}
	if summary.Failed != 0 {
		t.Errorf("Failed = %d, want 0", summary.Failed)
	}
	if accepted != 5 {
		t.Errorf("server saw %d requests, want 5", accepted)
	}
}

// TestRunStopsPromptlyOnContextCancellation is the graceful-shutdown proof:
// a long duration bound must not stop SIGINT (a cancelled context here)
// from returning quickly, well before that duration elapses.
func TestRunStopsPromptlyOnContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	var buf bytes.Buffer
	cfg := runConfig{
		url:        srv.URL,
		apiKey:     "k",
		rate:       5, // 200ms apart, slow enough that cancellation lands mid-run
		duration:   10 * time.Second,
		httpClient: &http.Client{Timeout: 2 * time.Second},
		clk:        clock.New(),
		logger:     testLogger(&buf),
	}

	start := time.Now()
	summary, err := run(ctx, cfg)
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Fatalf("run() took %v to return after cancellation, want well under the 10s duration bound", elapsed)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("run() err = %v, want context.Canceled", err)
	}
	if summary == nil {
		t.Fatal("run() returned a nil summary on cancellation, want the partial summary so far")
	}
}

// TestRunReportsGatewayDownClearly is "sane behaviour when the gateway is
// down: report it clearly, do not spin silently" made concrete: pointed at
// a server that isn't listening, run() must still return (bounded by
// -count, not hang), account every attempt as failed, and log something a
// human would notice.
func TestRunReportsGatewayDownClearly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening at this URL anymore

	var buf bytes.Buffer
	cfg := runConfig{
		url:        url,
		apiKey:     "k",
		rate:       1000,
		count:      5,
		httpClient: &http.Client{Timeout: 500 * time.Millisecond},
		clk:        clock.New(),
		logger:     testLogger(&buf),
	}

	done := make(chan struct{})
	var summary *Summary
	go func() {
		summary, _ = run(context.Background(), cfg)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run() did not return against a dead gateway; it must be bounded by -count, not spin forever")
	}

	if summary.Sent != 5 {
		t.Errorf("Sent = %d, want 5", summary.Sent)
	}
	if summary.Failed != 5 {
		t.Errorf("Failed = %d, want 5 (gateway down, every attempt fails)", summary.Failed)
	}
	if summary.Accepted != 0 {
		t.Errorf("Accepted = %d, want 0", summary.Accepted)
	}
	logged := strings.ToLower(buf.String())
	if !strings.Contains(logged, "unreachable") && !strings.Contains(logged, "gateway") {
		t.Errorf("log output = %q, want a clear mention that the gateway looks down, not silence", buf.String())
	}
}

// TestRunRejectsBothOrNeitherOfCountAndDuration mirrors the repo's own
// "exactly one of X or Y" validation convention (POST /v1/batches,
// docs/API_GATEWAY.md), applied to -count/-duration here.
func TestRunRejectsBothOrNeitherOfCountAndDuration(t *testing.T) {
	var buf bytes.Buffer
	base := runConfig{
		url:        "http://example.invalid",
		apiKey:     "k",
		rate:       10,
		httpClient: &http.Client{},
		clk:        clock.New(),
		logger:     testLogger(&buf),
	}

	neither := base
	if _, err := run(context.Background(), neither); err == nil {
		t.Error("run() with neither -count nor -duration set: want an error")
	}

	both := base
	both.count = 5
	both.duration = time.Second
	if _, err := run(context.Background(), both); err == nil {
		t.Error("run() with both -count and -duration set: want an error")
	}
}
