package main

import "testing"

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
