package server

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
)

// randSource is a source of uniform [0,1) draws, injected the same way
// clock.Clock is: production takes the real one, tests take a fixed
// sequence, so "recovery_probability 0.8, roll 0.79 succeeds, roll 0.81
// fails" is assertable instead of merely probable.
type randSource interface {
	Float64() float64
}

// realRand is the production source when no seed has been set
// (docs/DEMO_READINESS.md Unit AD; see Server.randFor). math/rand/v2's
// package-level functions are auto-seeded and safe for concurrent use, so
// there is nothing to construct.
type realRand struct{}

func (realRand) Float64() float64 { return rand.Float64() }

// seededRand is the seeded source, once POST /v1/demo/batches has resolved
// a seed (Server.randFor). Its Float64 derives one deterministic draw from
// a batch seed, a roll key and an attempt number, rather than advancing a
// shared PRNG sequence.
//
// SimulateOutcome runs behind gRPC, so calls for different records
// interleave concurrently; a shared sequential stream, even one guarded by
// a mutex, would still be nondeterministic in practice, because which
// record consumes which draw from the stream depends on goroutine
// scheduling order, not on the request itself. Two runs with the same
// seed would then roll the same set of outcomes but hand them to
// different records depending on timing, which is not reproducible in any
// sense that matters for a demo. Keying the draw off
// (seed, roll_key, attempt_number) instead makes every record's roll
// reproducible on its own terms: replaying the same seed against the same
// roll key and attempt always produces the same draw, no matter what else
// was in flight, or in what order, when it happened. That is the only
// form of "seeded" that survives concurrency, which is why it is used
// here instead of a mutex-guarded *rand.Rand.
//
// rollKey is deliberately not the record's own id. A record's id must stay
// a fresh uuid.NewString() every run, or two batches seeded with the same
// seed would mint the identical id twice and collide on GROUND_TRUTH's
// primary key. rollKey is instead a value SeedBatch chooses at generation
// time from (seed, ordinal index within the batch) and stores in
// GROUND_TRUTH (docs/DEMO_READINESS.md Unit AD, seed.go), so it is the one
// that actually repeats, by construction, across two runs of the same
// seed -- which is the entire point of "seeded" here.
//
// Each value is used for exactly one Float64() call in production
// (rollOutcome rolls a single Bernoulli trial per SimulateOutcome), so
// there is no shared mutable state here to guard: every draw seeds and
// discards its own generator, which needs no mutex.
type seededRand struct {
	seed          int64
	rollKey       string
	attemptNumber int32
}

func (s seededRand) Float64() float64 {
	h := fnv.New128a()
	// ":" never appears in a roll key (decimal digits, or a UUID
	// record_id on the pre-migration fallback path) or a formatted int64,
	// so this is a safe, simple delimiter (same reasoning queue.go's
	// delayedOutcome.member gives for its own use of ':').
	fmt.Fprintf(h, "%d:%s:%d", s.seed, s.rollKey, s.attemptNumber)
	sum := h.Sum(nil)
	seed1 := binary.BigEndian.Uint64(sum[0:8])
	seed2 := binary.BigEndian.Uint64(sum[8:16])
	return rand.New(rand.NewPCG(seed1, seed2)).Float64()
}
