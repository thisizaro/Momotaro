package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testPayload() WebhookPayload {
	return WebhookPayload{
		Type:          "PAYMENT",
		AmountPaise:   50000,
		Currency:      "INR",
		FailureCode:   "BANK_TIMEOUT",
		InstrumentRef: "",
	}
}

func TestSendEventAcceptedOn202(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-API-Key")
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"record_id":"r1","batch_id":"b1","deduplicated":false}`))
	}))
	defer srv.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	result := sendEvent(context.Background(), client, srv.URL, "super-secret-key", testPayload())

	if !result.accepted {
		t.Fatalf("result.accepted = false, want true (status %d, err %v)", result.status, result.err)
	}
	if result.status != http.StatusAccepted {
		t.Errorf("status = %d, want 202", result.status)
	}
	if gotKey != "super-secret-key" {
		t.Errorf("server saw X-API-Key = %q, want the configured key", gotKey)
	}
}

func TestSendEventNotAcceptedOnErrorStatus(t *testing.T) {
	for _, code := range []int{400, 401, 500, 502} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
			w.Write([]byte(`{"error":{"code":"X","message":"nope"}}`))
		}))
		client := &http.Client{Timeout: 2 * time.Second}
		result := sendEvent(context.Background(), client, srv.URL, "k", testPayload())
		srv.Close()

		if result.accepted {
			t.Errorf("status %d: result.accepted = true, want false", code)
		}
		if result.status != code {
			t.Errorf("status %d: result.status = %d, want %d", code, result.status, code)
		}
	}
}

// TestSendEventGatewayDownIsReportedNotSilent proves a dead gateway comes
// back as a clear, non-nil error rather than a false "accepted" or a hang:
// "sane behaviour when the gateway is down: report it clearly, do not spin
// silently."
func TestSendEventGatewayDownIsReportedNotSilent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // now nothing is listening

	client := &http.Client{Timeout: 2 * time.Second}
	result := sendEvent(context.Background(), client, url, "k", testPayload())

	if result.accepted {
		t.Fatal("result.accepted = true against a closed server, want false")
	}
	if result.err == nil {
		t.Fatal("result.err = nil against a closed server, want a reported error")
	}
}

// TestSendEventNeverLeaksAPIKeyInError is the security requirement made
// concrete: whatever sendEvent returns when a request fails must not
// contain the API key anywhere in its error text.
func TestSendEventNeverLeaksAPIKeyInError(t *testing.T) {
	const secret = "sk-do-not-log-me-98765"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	result := sendEvent(context.Background(), client, url, secret, testPayload())

	if result.err != nil && strings.Contains(result.err.Error(), secret) {
		t.Fatalf("error message leaks the API key: %v", result.err)
	}
}

func TestSendEventHonoursContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	client := &http.Client{Timeout: 5 * time.Second}
	result := sendEvent(ctx, client, srv.URL, "k", testPayload())

	if result.accepted {
		t.Fatal("result.accepted = true with an already-cancelled context, want false")
	}
	if result.err == nil {
		t.Fatal("result.err = nil with an already-cancelled context, want an error")
	}
}
