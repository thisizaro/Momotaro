package server

import "math/rand/v2"

// randSource is a source of uniform [0,1) draws, injected the same way
// clock.Clock is: production takes the real one, tests take a fixed
// sequence, so "recovery_probability 0.8, roll 0.79 succeeds, roll 0.81
// fails" is assertable instead of merely probable.
type randSource interface {
	Float64() float64
}

// realRand is the production source. math/rand/v2's package-level
// functions are auto-seeded and safe for concurrent use, so there is
// nothing to construct.
type realRand struct{}

func (realRand) Float64() float64 { return rand.Float64() }
