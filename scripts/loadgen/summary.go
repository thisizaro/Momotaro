package main

import (
	"fmt"
	"sync"
)

// Summary accumulates the run's counts: how many events were sent, how many
// the Gateway accepted (202), and how many failed (a non-202 response or a
// transport error). A pointer to one is shared between the send loop and,
// eventually, the SIGINT handler printing the final line, so every counter
// is mutex-guarded rather than a plain int.
type Summary struct {
	mu       sync.Mutex
	Sent     int
	Accepted int
	Failed   int
}

func (s *Summary) RecordSent() {
	s.mu.Lock()
	s.Sent++
	s.mu.Unlock()
}

func (s *Summary) RecordAccepted() {
	s.mu.Lock()
	s.Accepted++
	s.mu.Unlock()
}

func (s *Summary) RecordFailed() {
	s.mu.Lock()
	s.Failed++
	s.mu.Unlock()
}

// String is the exit summary line: sent, accepted, failed, and nothing
// else. In particular, never the API key or any request/response detail
// that might carry it.
func (s *Summary) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fmt.Sprintf("sent=%d accepted=%d failed=%d", s.Sent, s.Accepted, s.Failed)
}
