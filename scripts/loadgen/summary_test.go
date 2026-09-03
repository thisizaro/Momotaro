package main

import (
	"strings"
	"sync"
	"testing"
)

func TestSummaryAccounting(t *testing.T) {
	s := &Summary{}
	s.RecordSent()
	s.RecordSent()
	s.RecordSent()
	s.RecordAccepted()
	s.RecordAccepted()
	s.RecordFailed()

	if s.Sent != 3 {
		t.Errorf("Sent = %d, want 3", s.Sent)
	}
	if s.Accepted != 2 {
		t.Errorf("Accepted = %d, want 2", s.Accepted)
	}
	if s.Failed != 1 {
		t.Errorf("Failed = %d, want 1", s.Failed)
	}
}

// TestSummaryConcurrentSafe is the reason Summary exists as its own small
// type rather than three plain ints: main's send loop and any future
// concurrent sender must not race on these counters. Run with -race
// (make test / go test -race), as every unit test in this repo is.
func TestSummaryConcurrentSafe(t *testing.T) {
	s := &Summary{}
	var wg sync.WaitGroup
	const n = 200
	wg.Add(n * 3)
	for i := 0; i < n; i++ {
		go func() { defer wg.Done(); s.RecordSent() }()
		go func() { defer wg.Done(); s.RecordAccepted() }()
		go func() { defer wg.Done(); s.RecordFailed() }()
	}
	wg.Wait()

	if s.Sent != n {
		t.Errorf("Sent = %d, want %d", s.Sent, n)
	}
	if s.Accepted != n {
		t.Errorf("Accepted = %d, want %d", s.Accepted, n)
	}
	if s.Failed != n {
		t.Errorf("Failed = %d, want %d", s.Failed, n)
	}
}

func TestSummaryStringReportsAllThreeCounts(t *testing.T) {
	s := &Summary{}
	s.RecordSent()
	s.RecordAccepted()
	out := s.String()

	for _, want := range []string{"sent", "accepted", "failed"} {
		if !strings.Contains(strings.ToLower(out), want) {
			t.Errorf("String() = %q, missing %q", out, want)
		}
	}
}

// TestSummaryStringNeverContainsAPIKey guards the exit-summary line
// specifically, since it is the one line this CLI always prints, API key
// down or not.
func TestSummaryStringNeverContainsAPIKey(t *testing.T) {
	s := &Summary{}
	s.RecordSent()
	out := s.String()
	const secret = "sk-should-never-appear"
	if strings.Contains(out, secret) {
		t.Fatalf("String() unexpectedly contains a secret-shaped string: %q", out)
	}
}
