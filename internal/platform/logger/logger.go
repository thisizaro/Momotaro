// Package logger provides the one structured logger every service uses.
//
// Rules this enforces (docs/ENGINEERING.md section 9): structured JSON only,
// no fmt.Println, and every line inside a record's lifecycle carries
// record_id (and batch_id where known). Correlation is the entire point: a
// log you cannot filter by record_id is useless when tracing one payment
// across seven services.
package logger

import (
	"context"
	"log/slog"
	"os"
)

// Field names are constants so seven services cannot invent seven spellings
// of the same key. Filtering by record_id only works if everyone agrees.
const (
	KeyService   = "service"
	KeyRecordID  = "record_id"
	KeyBatchID   = "batch_id"
	KeyTraceID   = "trace_id"
	KeyAttempt   = "attempt_number"
	KeyState     = "state"
	KeyAction    = "action"
	KeyOutcome   = "outcome"
	KeyBucket    = "root_cause_bucket"
	KeySource    = "source"
	KeyCostPaise = "cost_paise"
	KeyProvider  = "provider"
	KeyError     = "err"
)

// New returns a JSON logger tagged with the service name.
//
// level is validated by the config package, so an unrecognised value here
// falls back to info rather than failing: logging is not worth crashing over
// once config validation has already passed.
func New(serviceName, level string) *slog.Logger {
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLevel(level),
	})
	return slog.New(h).With(KeyService, serviceName)
}

// Discard returns a logger that writes nothing. For tests that do not assert
// on log output.
func Discard() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.NewFile(0, os.DevNull), nil))
}

func parseLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// ForRecord returns a logger pre-tagged with a record's identifiers. Prefer
// this over re-passing record_id at every call site, which is how a log line
// eventually ends up missing it.
func ForRecord(log *slog.Logger, recordID, batchID string) *slog.Logger {
	l := log.With(KeyRecordID, recordID)
	if batchID != "" {
		l = l.With(KeyBatchID, batchID)
	}
	return l
}

type ctxKey struct{}

// Into stores a logger on the context so handlers deep in a call chain can
// log with the request's correlation fields already attached.
func Into(ctx context.Context, log *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, log)
}

// From retrieves a logger placed by Into. Returns slog.Default() when absent,
// so a caller never has to nil-check.
func From(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}
