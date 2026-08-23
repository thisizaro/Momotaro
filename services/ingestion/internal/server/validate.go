package server

import (
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	ingestionv1 "github.com/thisizaro/Momotaro/proto/gen/ingestion/v1"
)

// validateNewRecord returns a human-readable rejection reason, or "" if nr
// is acceptable. Amount must be positive to satisfy record's own CHECK
// constraint without the caller ever seeing a raw SQL error.
func validateNewRecord(nr *ingestionv1.NewRecord) string {
	if nr.GetType() == commonv1.RecordType_RECORD_TYPE_UNSPECIFIED {
		return "type is required"
	}
	if nr.GetAmountPaise() <= 0 {
		return "amount_paise must be positive"
	}
	if nr.GetFailureCode() == "" {
		return "failure_code is required"
	}
	return ""
}
