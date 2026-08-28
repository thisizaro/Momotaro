package llm

import (
	"net/http"
	"testing"
	"time"
)

// Retry-After decides how long Unit D's breaker stays open, so a
// misparse either wedges a healthy provider out of the chain or hammers a
// throttled one. RFC 9110 allows two forms and providers use both.
func TestRetryAfterParsesBothWireForms(t *testing.T) {
	cases := map[string]struct {
		header string
		want   time.Duration
		exact  bool
	}{
		"delay seconds":       {header: "42", want: 42 * time.Second, exact: true},
		"zero seconds":        {header: "0", want: 0, exact: true},
		"absent":              {header: "", want: 0, exact: true},
		"garbage":             {header: "soon please", want: 0, exact: true},
		"negative is ignored": {header: "-5", want: 0, exact: true},
		// An HTTP-date in the past means "now", not a negative cooldown.
		"past http date": {header: time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat), want: 0, exact: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := http.Header{}
			if tc.header != "" {
				h.Set("Retry-After", tc.header)
			}
			if got := retryAfter(h); got != tc.want {
				t.Errorf("retryAfter(%q) = %s, want %s", tc.header, got, tc.want)
			}
		})
	}
}

func TestRetryAfterAcceptsAFutureHTTPDate(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", time.Now().Add(30*time.Second).UTC().Format(http.TimeFormat))
	got := retryAfter(h)
	// Second-granularity format plus clock movement, so assert a band.
	if got < 25*time.Second || got > 31*time.Second {
		t.Errorf("retryAfter(future date) = %s, want roughly 30s", got)
	}
}
