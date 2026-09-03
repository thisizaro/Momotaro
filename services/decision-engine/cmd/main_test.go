package main

import (
	"testing"
	"time"
)

// validEnv is the minimum env loadConfig needs to succeed, so each test
// below only has to set the one variable it cares about.
func validEnv(t *testing.T) {
	t.Setenv("POSTGRES_DSN", "postgres://localhost/momotaro")
	t.Setenv("REDIS_ADDR", "localhost:6379")
	t.Setenv("KAFKA_BROKERS", "localhost:9092")
	t.Setenv("CLASSIFIER_ADDR", "localhost:9090")
	t.Setenv("EXECUTOR_ADDR", "localhost:9090")
}

func TestLoadConfigDefaultLLMSampleRateIsZero(t *testing.T) {
	validEnv(t)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.LLMSampleRate != 0 {
		t.Errorf("default LLMSampleRate = %v, want 0 (every existing deploy stays free of live LLM calls)", cfg.LLMSampleRate)
	}
}

// Out-of-range values must fail at startup, not at request time
// (docs/PHASE3_IMPLEMENTATION.md Unit H): a value of 3 intended as "3 in
// 10" would otherwise sample the entire batch instead of failing loudly.
func TestLoadConfigRejectsOutOfRangeLLMSampleRate(t *testing.T) {
	for _, bad := range []string{"3", "-0.1", "1.5"} {
		t.Run(bad, func(t *testing.T) {
			validEnv(t)
			t.Setenv("LLM_SAMPLE_RATE", bad)
			if _, err := loadConfig(); err == nil {
				t.Errorf("loadConfig accepted LLM_SAMPLE_RATE=%s, want a startup error", bad)
			}
		})
	}
}

func TestLoadConfigAcceptsBoundaryLLMSampleRates(t *testing.T) {
	for _, good := range []string{"0", "1", "0.15", "0.5"} {
		t.Run(good, func(t *testing.T) {
			validEnv(t)
			t.Setenv("LLM_SAMPLE_RATE", good)
			if _, err := loadConfig(); err != nil {
				t.Errorf("loadConfig rejected LLM_SAMPLE_RATE=%s: %v", good, err)
			}
		})
	}
}

func TestLoadConfigDefaultRouteConfidenceThresholdIsZero(t *testing.T) {
	validEnv(t)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.RouteConfidenceThreshold != 0 {
		t.Errorf("default RouteConfidenceThreshold = %v, want 0 (a threshold of 0 can never be satisfied by a real confidence value, so every existing deploy routes nothing to a live model)", cfg.RouteConfidenceThreshold)
	}
}

// Out-of-range values must fail at startup, same reasoning as
// LLM_SAMPLE_RATE above: this is compared against a confidence, always in
// [0,1], so anything else can only be a typo.
func TestLoadConfigRejectsOutOfRangeRouteConfidenceThreshold(t *testing.T) {
	for _, bad := range []string{"3", "-0.1", "1.5"} {
		t.Run(bad, func(t *testing.T) {
			validEnv(t)
			t.Setenv("LLM_ROUTE_CONFIDENCE_THRESHOLD", bad)
			if _, err := loadConfig(); err == nil {
				t.Errorf("loadConfig accepted LLM_ROUTE_CONFIDENCE_THRESHOLD=%s, want a startup error", bad)
			}
		})
	}
}

func TestLoadConfigAcceptsBoundaryRouteConfidenceThresholds(t *testing.T) {
	for _, good := range []string{"0", "1", "0.8", "0.5"} {
		t.Run(good, func(t *testing.T) {
			validEnv(t)
			t.Setenv("LLM_ROUTE_CONFIDENCE_THRESHOLD", good)
			if _, err := loadConfig(); err != nil {
				t.Errorf("loadConfig rejected LLM_ROUTE_CONFIDENCE_THRESHOLD=%s: %v", good, err)
			}
		})
	}
}

// The regression test for docs/INCIDENTS.md 2026-08-31: DEMO_TIME_SCALE must
// not shrink the recovery window.
//
// guardrails.go compares RecoveryWindow against a record's real elapsed age,
// so scaling it does not compress a wait, it just makes the window smaller
// than the pipeline's own processing latency. At the demo profile's 300000 the
// 7 day default collapsed to 2.016s and 73 of 100 records were escalated for
// "recovery window closed" before the economics scorer ever priced them.
//
// The value asserted here is the raw configured duration, deliberately, so
// that reinstating cfg.Scale() on this field turns the test red.
func TestRecoveryWindowIsNotScaledByDemoTimeScale(t *testing.T) {
	validEnv(t)
	t.Setenv("DEMO_TIME_SCALE", "300000")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	g := guardrailsFrom(cfg)

	if g.RecoveryWindow != cfg.RecoveryWindow {
		t.Errorf("RecoveryWindow = %s, want the unscaled %s; scaling it compares a compressed window against a real wall-clock age (docs/INCIDENTS.md 2026-08-31)",
			g.RecoveryWindow, cfg.RecoveryWindow)
	}

	// The concrete failure the incident describes: a record still being
	// processed a few real seconds after creation must remain inside its
	// window. Under the old scaled behaviour the window was ~2s and this
	// record was escalated instead of acted on.
	const realisticProcessingLatency = 10 * time.Second
	if g.RecoveryWindow <= realisticProcessingLatency {
		t.Errorf("RecoveryWindow = %s, which is under the %s a record can plausibly spend being classified, priced and scheduled; every such record would be escalated for window closure",
			g.RecoveryWindow, realisticProcessingLatency)
	}
}

// ContactCooldown, by contrast, IS a wait we schedule and must stay scaled, or
// a 24h cooldown never elapses inside a demo and no record is ever contacted
// twice. Asserted alongside the above so the asymmetry is deliberate and
// visible rather than looking like an oversight in either direction.
func TestContactCooldownIsStillScaled(t *testing.T) {
	validEnv(t)
	t.Setenv("DEMO_TIME_SCALE", "300000")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	g := guardrailsFrom(cfg)

	if g.ContactCooldown >= cfg.ContactCooldown {
		t.Errorf("ContactCooldown = %s, want it scaled below the configured %s; it is a wait we schedule, not a comparison against elapsed real time",
			g.ContactCooldown, cfg.ContactCooldown)
	}
}
