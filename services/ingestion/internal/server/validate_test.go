package server

import (
	"testing"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	ingestionv1 "github.com/thisizaro/Momotaro/proto/gen/ingestion/v1"
)

func TestValidateNewRecord(t *testing.T) {
	valid := func() *ingestionv1.NewRecord {
		return &ingestionv1.NewRecord{
			Type:        commonv1.RecordType_RECORD_TYPE_PAYMENT,
			AmountPaise: 50000,
			FailureCode: "BANK_TIMEOUT",
		}
	}

	tests := []struct {
		name    string
		record  func() *ingestionv1.NewRecord
		wantErr string // "" means accepted
	}{
		{
			name:   "valid record is accepted",
			record: valid,
		},
		{
			name: "missing type is rejected",
			record: func() *ingestionv1.NewRecord {
				r := valid()
				r.Type = commonv1.RecordType_RECORD_TYPE_UNSPECIFIED
				return r
			},
			wantErr: "type is required",
		},
		{
			name: "zero amount is rejected",
			record: func() *ingestionv1.NewRecord {
				r := valid()
				r.AmountPaise = 0
				return r
			},
			wantErr: "amount_paise must be positive",
		},
		{
			name: "negative amount is rejected",
			record: func() *ingestionv1.NewRecord {
				r := valid()
				r.AmountPaise = -1
				return r
			},
			wantErr: "amount_paise must be positive",
		},
		{
			name: "missing failure code is rejected",
			record: func() *ingestionv1.NewRecord {
				r := valid()
				r.FailureCode = ""
				return r
			},
			wantErr: "failure_code is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validateNewRecord(tt.record())
			if got != tt.wantErr {
				t.Errorf("validateNewRecord() = %q, want %q", got, tt.wantErr)
			}
		})
	}
}
