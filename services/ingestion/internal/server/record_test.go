package server

import (
	"testing"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/clock"
	ingestionv1 "github.com/thisizaro/Momotaro/proto/gen/ingestion/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestCurrencyOrDefault(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty defaults to INR", in: "", want: "INR"},
		{name: "preserves given value", in: "USD", want: "USD"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := currencyOrDefault(tt.in); got != tt.want {
				t.Errorf("currencyOrDefault(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestResolveCreatedAt(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	occurred := time.Date(2026, 8, 20, 9, 30, 0, 0, time.UTC)

	t.Run("falls back to the clock when occurred_at is unset", func(t *testing.T) {
		fake := clock.NewFake(now)
		got := resolveCreatedAt(fake, &ingestionv1.NewRecord{})
		if !got.Equal(now) {
			t.Errorf("resolveCreatedAt() = %v, want %v", got, now)
		}
	})

	t.Run("uses occurred_at when the caller set one", func(t *testing.T) {
		fake := clock.NewFake(now)
		nr := &ingestionv1.NewRecord{OccurredAt: timestamppb.New(occurred)}
		got := resolveCreatedAt(fake, nr)
		if !got.Equal(occurred) {
			t.Errorf("resolveCreatedAt() = %v, want %v", got, occurred)
		}
	})
}
