package engine

import "time"

// RawEvent mirrors services/ingestion/internal/server.RawEvent, the
// raw.events wire payload. Kept structurally in sync by hand; see that
// type's doc comment for why there is no shared package for it.
type RawEvent struct {
	RecordID      string    `json:"record_id"`
	BatchID       string    `json:"batch_id"`
	Type          string    `json:"type"`
	AmountPaise   int64     `json:"amount_paise"`
	Currency      string    `json:"currency"`
	FailureCode   string    `json:"failure_code"`
	InstrumentRef string    `json:"instrument_ref"`
	CreatedAt     time.Time `json:"created_at"`

	// Razorpay's four-field error taxonomy (docs/PHASE5_5_IMPLEMENTATION.md
	// Unit Z), mirroring services/ingestion/internal/server.RawEvent's own
	// fields of the same names. All optional, all open strings.
	ErrorCode        string `json:"error_code,omitempty"`
	ErrorDescription string `json:"error_description,omitempty"`
	ErrorSource      string `json:"error_source,omitempty"`
	ErrorStep        string `json:"error_step,omitempty"`
	ErrorReason      string `json:"error_reason,omitempty"`
}

// DeadLetter is the raw.events.dlq wire payload (docs/ARCHITECTURE.md
// section 8b). RawValue keeps the original message bytes so a record is
// still traceable even when the payload itself could not be parsed as a
// RawEvent, RecordID/BatchID are only populated when the record ever got
// far enough to be known.
type DeadLetter struct {
	RecordID      string    `json:"record_id,omitempty"`
	BatchID       string    `json:"batch_id,omitempty"`
	RawValue      string    `json:"raw_value"`
	FailureReason string    `json:"failure_reason"`
	FailedAt      time.Time `json:"failed_at"`
}
