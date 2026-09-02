package config

import (
	"strings"
	"testing"
	"time"
)

// Minimum env for LoadCommon to succeed.
func validEnv(t *testing.T) {
	t.Setenv("POSTGRES_DSN", "postgres://localhost/momotaro")
	t.Setenv("REDIS_ADDR", "localhost:6379")
	t.Setenv("KAFKA_BROKERS", "localhost:9092")
}

func TestLoadCommonDefaults(t *testing.T) {
	validEnv(t)
	l := NewLoader()
	c := LoadCommon(l, "classifier")
	if err := l.Err(); err != nil {
		t.Fatalf("valid env rejected: %v", err)
	}
	if c.ServiceName != "classifier" || c.Env != "local" || c.LogLevel != "info" {
		t.Errorf("unexpected defaults: %+v", c)
	}
	if c.GRPCPort != 9090 || c.MetricsPort != 9091 {
		t.Errorf("unexpected default ports: %d %d", c.GRPCPort, c.MetricsPort)
	}
	if c.DemoTimeScale != 1 {
		t.Errorf("DemoTimeScale = %v, want 1", c.DemoTimeScale)
	}
}

// Fail fast: a missing required var must be an error, never a silent zero.
func TestLoadCommonRequiresCriticalVars(t *testing.T) {
	for _, missing := range []string{"POSTGRES_DSN", "REDIS_ADDR", "KAFKA_BROKERS"} {
		t.Run("missing_"+missing, func(t *testing.T) {
			validEnv(t)
			t.Setenv(missing, "")
			l := NewLoader()
			LoadCommon(l, "svc")
			err := l.Err()
			if err == nil {
				t.Fatalf("missing %s was accepted", missing)
			}
			if !strings.Contains(err.Error(), missing) {
				t.Errorf("error does not name %s: %v", missing, err)
			}
		})
	}
}

// All problems at once, so a misconfigured deploy needs one restart to
// diagnose rather than one per variable.
func TestLoaderAccumulatesAllErrors(t *testing.T) {
	t.Setenv("POSTGRES_DSN", "")
	t.Setenv("REDIS_ADDR", "")
	t.Setenv("KAFKA_BROKERS", "")
	l := NewLoader()
	LoadCommon(l, "svc")
	err := l.Err()
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"POSTGRES_DSN", "REDIS_ADDR", "KAFKA_BROKERS"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %s: %v", want, err)
		}
	}
}

func TestValidation(t *testing.T) {
	tests := []struct {
		name, key, val, wantIn string
	}{
		{"bad log level", "LOG_LEVEL", "verbose", "LOG_LEVEL"},
		{"zero time scale", "DEMO_TIME_SCALE", "0", "DEMO_TIME_SCALE"},
		{"negative time scale", "DEMO_TIME_SCALE", "-2", "DEMO_TIME_SCALE"},
		{"non-numeric time scale", "DEMO_TIME_SCALE", "fast", "DEMO_TIME_SCALE"},
		{"port out of range", "GRPC_PORT", "99999", "GRPC_PORT"},
		{"port not a number", "GRPC_PORT", "http", "GRPC_PORT"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			validEnv(t)
			t.Setenv(tc.key, tc.val)
			l := NewLoader()
			LoadCommon(l, "svc")
			err := l.Err()
			if err == nil {
				t.Fatalf("%s=%q was accepted", tc.key, tc.val)
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("error does not mention %s: %v", tc.wantIn, err)
			}
		})
	}
}

// Two servers cannot share a port. Catching this at startup beats debugging
// a bind error in a crash-looping pod.
func TestPortCollisionRejected(t *testing.T) {
	validEnv(t)
	t.Setenv("GRPC_PORT", "9090")
	t.Setenv("METRICS_PORT", "9090")
	l := NewLoader()
	LoadCommon(l, "svc")
	if l.Err() == nil {
		t.Fatal("identical grpc and metrics ports were accepted")
	}
}

func TestCSVParsing(t *testing.T) {
	tests := []struct {
		name, raw string
		want      int
	}{
		{"single", "a:9092", 1},
		{"multiple", "a:9092,b:9092,c:9092", 3},
		{"padded and trailing comma", " a:9092 , b:9092 ,", 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			validEnv(t)
			t.Setenv("KAFKA_BROKERS", tc.raw)
			l := NewLoader()
			c := LoadCommon(l, "svc")
			if err := l.Err(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(c.KafkaBrokers) != tc.want {
				t.Errorf("got %d brokers %v, want %d", len(c.KafkaBrokers), c.KafkaBrokers, tc.want)
			}
		})
	}
}

func TestCSVAllSeparatorsIsAnError(t *testing.T) {
	validEnv(t)
	t.Setenv("KAFKA_BROKERS", ",,,")
	l := NewLoader()
	LoadCommon(l, "svc")
	if l.Err() == nil {
		t.Fatal(`KAFKA_BROKERS=",,," was accepted`)
	}
}

// CSVDefault is the optional counterpart to CSV: unset must stay silent
// and produce nil, since callers use nil to mean "not configured, keep the
// safe default" (services/api-gateway's WS_ALLOWED_ORIGINS is the first
// caller: unset must leave the WebSocket route exactly as restrictive as
// it is today).
func TestCSVDefaultUnsetReturnsNil(t *testing.T) {
	l := NewLoader()
	got := l.CSVDefault("WS_ALLOWED_ORIGINS")
	if err := l.Err(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestCSVDefaultParsing(t *testing.T) {
	tests := []struct {
		name, raw string
		want      []string
	}{
		{"single", "http://localhost:5173", []string{"http://localhost:5173"}},
		{"multiple", "http://a,http://b", []string{"http://a", "http://b"}},
		{"padded and trailing comma", " http://a , http://b ,", []string{"http://a", "http://b"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WS_ALLOWED_ORIGINS", tc.raw)
			l := NewLoader()
			got := l.CSVDefault("WS_ALLOWED_ORIGINS")
			if err := l.Err(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// Unlike CSV, an all-separator value is not an error: it degrades to an
// empty (non-nil) list rather than failing startup, since this variable is
// optional and never required to carry a value.
func TestCSVDefaultAllSeparatorsIsEmptyNotError(t *testing.T) {
	t.Setenv("WS_ALLOWED_ORIGINS", ",,,")
	l := NewLoader()
	got := l.CSVDefault("WS_ALLOWED_ORIGINS")
	if err := l.Err(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

func TestScale(t *testing.T) {
	tests := []struct {
		name  string
		scale float64
		in    time.Duration
		want  time.Duration
	}{
		{"real time is identity", 1, 2 * time.Hour, 2 * time.Hour},
		{"60x compresses an hour to a minute", 60, time.Hour, time.Minute},
		{"3600x compresses an hour to a second", 3600, time.Hour, time.Second},
		{"below 1 stretches", 0.5, time.Second, 2 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := Common{DemoTimeScale: tc.scale}
			if got := c.Scale(tc.in); got != tc.want {
				t.Errorf("Scale(%v) with scale %v = %v, want %v", tc.in, tc.scale, got, tc.want)
			}
		})
	}
}

func TestOptionalHelpers(t *testing.T) {
	l := NewLoader()
	t.Setenv("SOME_DURATION", "15m")
	t.Setenv("SOME_BOOL", "yes")
	if got := l.Duration("SOME_DURATION", time.Second); got != 15*time.Minute {
		t.Errorf("Duration = %v, want 15m", got)
	}
	if got := l.Duration("UNSET_DURATION", 3*time.Second); got != 3*time.Second {
		t.Errorf("Duration default = %v, want 3s", got)
	}
	if !l.Bool("SOME_BOOL", false) {
		t.Error(`Bool("yes") = false`)
	}
	if err := l.Err(); err != nil {
		t.Errorf("unexpected errors: %v", err)
	}

	bad := NewLoader()
	t.Setenv("BAD_DURATION", "soon")
	bad.Duration("BAD_DURATION", time.Second)
	if bad.Err() == nil {
		t.Error(`Duration("soon") was accepted`)
	}
}
