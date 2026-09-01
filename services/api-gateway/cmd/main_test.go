package main

import "testing"

// validEnv is the minimum env loadConfig needs to succeed, so each test
// below only has to set the one variable it cares about.
func validEnv(t *testing.T) {
	t.Setenv("POSTGRES_DSN", "postgres://localhost/momotaro")
	t.Setenv("REDIS_ADDR", "localhost:6379")
	t.Setenv("KAFKA_BROKERS", "localhost:9092")
	t.Setenv("INGESTION_ADDR", "localhost:9090")
	t.Setenv("REPORTING_ADDR", "localhost:9090")
	t.Setenv("AUDIT_ADDR", "localhost:9090")
	t.Setenv("API_KEY", "test-key")
}

func TestLoadConfigDemoControlsDefaultFalse(t *testing.T) {
	validEnv(t)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.DemoControlsEnabled {
		t.Error("DemoControlsEnabled defaulted to true, want false: demo controls must not exist in a production deployment by default")
	}
}

// The fail-fast contract (docs/ENGINEERING.md section 5): a service must
// not start in a configuration that will fail on its first request. Demo
// controls enabled with no address to dial is exactly that.
func TestLoadConfigRequiresWorldSimulatorAddrWhenDemoControlsEnabled(t *testing.T) {
	validEnv(t)
	t.Setenv("DEMO_CONTROLS_ENABLED", "true")
	if _, err := loadConfig(); err == nil {
		t.Fatal("loadConfig accepted DEMO_CONTROLS_ENABLED=true with no WORLD_SIMULATOR_ADDR, want a startup error")
	}
}

func TestLoadConfigAcceptsDemoControlsEnabledWithAddr(t *testing.T) {
	validEnv(t)
	t.Setenv("DEMO_CONTROLS_ENABLED", "true")
	t.Setenv("WORLD_SIMULATOR_ADDR", "localhost:9202")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if !cfg.DemoControlsEnabled {
		t.Error("DemoControlsEnabled = false, want true")
	}
	if cfg.WorldSimulatorAddr != "localhost:9202" {
		t.Errorf("WorldSimulatorAddr = %q, want localhost:9202", cfg.WorldSimulatorAddr)
	}
}

// WORLD_SIMULATOR_ADDR is only required once the flag is on: a production
// deployment (the default, flag off) must never be forced to know World
// Simulator exists.
func TestLoadConfigDoesNotRequireWorldSimulatorAddrWhenDisabled(t *testing.T) {
	validEnv(t)
	if _, err := loadConfig(); err != nil {
		t.Fatalf("loadConfig with demo controls left at their default (disabled): %v", err)
	}
}
