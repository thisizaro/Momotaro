package server

import (
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/clock"
	ingestionv1 "github.com/thisizaro/Momotaro/proto/gen/ingestion/v1"
)

// defaultCurrency is what a record gets when the caller leaves currency
// unset. This system only ever deals in Indian rail failures (docs/PRD.md).
const defaultCurrency = "INR"

// currencyOrDefault returns c, or defaultCurrency if c is empty.
func currencyOrDefault(c string) string {
	if c == "" {
		return defaultCurrency
	}
	return c
}

// resolveCreatedAt returns when the upstream failure happened: the caller's
// occurred_at if it set one, otherwise the current time. Always goes through
// the injected clock, never time.Now() directly (docs/ENGINEERING.md
// section 2), so "created_at defaults to receipt time" is actually testable.
func resolveCreatedAt(clk clock.Clock, nr *ingestionv1.NewRecord) time.Time {
	if ts := nr.GetOccurredAt(); ts != nil {
		return ts.AsTime()
	}
	return clk.Now()
}
