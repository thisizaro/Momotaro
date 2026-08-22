package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
)

// capture returns a logger writing JSON into buf, so tests assert on the
// actual emitted structure rather than on formatting.
func capture(t *testing.T) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	l := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})).
		With(KeyService, "test-svc")
	return l, &buf
}

func decode(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("output was not valid JSON: %v\n%s", err, buf.String())
	}
	return m
}

func TestNewTagsService(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(slog.NewJSONHandler(&buf, nil)).With(KeyService, "classifier")
	l.Info("hello")
	if got := decode(t, &buf)[KeyService]; got != "classifier" {
		t.Errorf("%s = %v, want classifier", KeyService, got)
	}
}

// The correlation guarantee: a record-scoped logger must attach record_id to
// every line without the call site repeating it.
func TestForRecordAttachesCorrelationFields(t *testing.T) {
	l, buf := capture(t)
	ForRecord(l, "rec-1", "batch-9").Info("classified")

	m := decode(t, buf)
	if m[KeyRecordID] != "rec-1" {
		t.Errorf("%s = %v, want rec-1", KeyRecordID, m[KeyRecordID])
	}
	if m[KeyBatchID] != "batch-9" {
		t.Errorf("%s = %v, want batch-9", KeyBatchID, m[KeyBatchID])
	}
}

func TestForRecordOmitsEmptyBatchID(t *testing.T) {
	l, buf := capture(t)
	ForRecord(l, "rec-1", "").Info("ingested")

	m := decode(t, buf)
	if _, present := m[KeyBatchID]; present {
		t.Errorf("%s should be absent when empty, got %v", KeyBatchID, m[KeyBatchID])
	}
	if m[KeyRecordID] != "rec-1" {
		t.Errorf("%s = %v, want rec-1", KeyRecordID, m[KeyRecordID])
	}
}

func TestContextRoundTrip(t *testing.T) {
	l, buf := capture(t)
	ctx := Into(context.Background(), ForRecord(l, "rec-7", "batch-2"))

	From(ctx).Info("deep in a call chain")

	m := decode(t, buf)
	if m[KeyRecordID] != "rec-7" {
		t.Errorf("logger did not survive the context: %v", m)
	}
}

// From must never return nil, so callers never nil-check.
func TestFromWithoutLoggerReturnsUsableDefault(t *testing.T) {
	if From(context.Background()) == nil {
		t.Fatal("From returned nil for a bare context")
	}
	if From(Into(context.Background(), nil)) == nil {
		t.Fatal("From returned nil when a nil logger was stored")
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		in   string
		want slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
		{"", slog.LevelInfo},        // unset falls back
		{"verbose", slog.LevelInfo}, // unknown falls back, never panics
	}
	for _, tc := range tests {
		if got := parseLevel(tc.in); got != tc.want {
			t.Errorf("parseLevel(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	l.Info("should be dropped")
	if buf.Len() != 0 {
		t.Errorf("info emitted at warn level: %s", buf.String())
	}
	l.Warn("should appear")
	if buf.Len() == 0 {
		t.Error("warn was dropped at warn level")
	}
}
