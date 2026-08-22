// Package config loads and validates service configuration from the
// environment, exactly once, at startup.
//
// The rule this package exists to enforce (docs/ENGINEERING.md section 5):
// parse everything up front, validate it, and exit non-zero if anything
// required is missing or malformed. Never start a service that will fail on
// its first request. In Kubernetes that produces a pod which looks healthy
// and silently black-holes work, which is far worse than a crash loop
// because nothing alerts on it.
//
// Usage: one Loader collects every problem, then you check it once, so a
// misconfigured deploy reports all its faults in a single restart.
//
//	l := config.NewLoader()
//	common := config.LoadCommon(l, "classifier")
//	timeout := l.Duration("LLM_TIMEOUT", 2*time.Second)
//	if err := l.Err(); err != nil {
//	    return err // main() logs and exits non-zero
//	}
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Common holds the settings every service needs. Embed it in a
// service-specific struct.
type Common struct {
	ServiceName string
	Env         string // "local", "ci", "k8s"
	LogLevel    string // debug|info|warn|error
	GRPCPort    int
	MetricsPort int

	PostgresDSN  string
	RedisAddr    string
	KafkaBrokers []string

	// Compresses every wall-clock wait (cooldowns, delayed outcomes) so a
	// live demo finishes in minutes rather than hours. 1 means real time.
	// See docs/ARCHITECTURE.md section 17.
	DemoTimeScale float64
}

// Loader accumulates errors so a misconfigured service reports everything
// wrong at once, rather than one variable per restart.
type Loader struct {
	errs []string
}

func NewLoader() *Loader { return &Loader{} }

// Err returns all accumulated problems, or nil.
func (l *Loader) Err() error {
	if len(l.errs) == 0 {
		return nil
	}
	return fmt.Errorf("invalid configuration:\n  - %s", strings.Join(l.errs, "\n  - "))
}

func (l *Loader) fail(format string, args ...any) {
	l.errs = append(l.errs, fmt.Sprintf(format, args...))
}

// Str returns a required string. Missing or empty is an error.
func (l *Loader) Str(key string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		l.fail("%s is required but not set", key)
	}
	return v
}

// StrDefault returns an optional string.
func (l *Loader) StrDefault(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// Int returns an optional int, failing if set but unparseable.
func (l *Loader) Int(key string, def int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		l.fail("%s must be an integer, got %q", key, raw)
		return def
	}
	return v
}

// Float returns an optional float, failing if set but unparseable.
func (l *Loader) Float(key string, def float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		l.fail("%s must be a number, got %q", key, raw)
		return def
	}
	return v
}

// Bool accepts 1/0, true/false, yes/no.
func (l *Loader) Bool(key string, def bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if raw == "" {
		return def
	}
	switch raw {
	case "1", "true", "yes", "y":
		return true
	case "0", "false", "no", "n":
		return false
	default:
		l.fail("%s must be a boolean, got %q", key, raw)
		return def
	}
}

// Duration accepts Go duration syntax, e.g. "2s", "15m".
func (l *Loader) Duration(key string, def time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		l.fail("%s must be a duration such as 2s or 15m, got %q", key, raw)
		return def
	}
	return v
}

// CSV returns a comma-separated list. Required: empty is an error.
func (l *Loader) CSV(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		l.fail("%s is required but not set", key)
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		l.fail("%s contained no usable values, got %q", key, raw)
	}
	return out
}

// Port validates that a value is a usable TCP port.
func (l *Loader) Port(key string, def int) int {
	v := l.Int(key, def)
	if v < 1 || v > 65535 {
		l.fail("%s must be a port between 1 and 65535, got %d", key, v)
	}
	return v
}

// LoadCommon reads the settings every service shares. Callers add their own
// fields with the same Loader, then check Err() once.
//
//	l := config.NewLoader()
//	common := config.LoadCommon(l, "classifier")
//	myTimeout := l.Duration("LLM_TIMEOUT", 2*time.Second)
//	if err := l.Err(); err != nil { return err }
func LoadCommon(l *Loader, serviceName string) Common {
	c := Common{
		ServiceName:   serviceName,
		Env:           l.StrDefault("ENV", "local"),
		LogLevel:      l.StrDefault("LOG_LEVEL", "info"),
		GRPCPort:      l.Port("GRPC_PORT", 9090),
		MetricsPort:   l.Port("METRICS_PORT", 9091),
		PostgresDSN:   l.Str("POSTGRES_DSN"),
		RedisAddr:     l.Str("REDIS_ADDR"),
		KafkaBrokers:  l.CSV("KAFKA_BROKERS"),
		DemoTimeScale: l.Float("DEMO_TIME_SCALE", 1),
	}

	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		l.fail("LOG_LEVEL must be one of debug, info, warn, error; got %q", c.LogLevel)
	}
	if c.DemoTimeScale <= 0 {
		l.fail("DEMO_TIME_SCALE must be greater than 0, got %v", c.DemoTimeScale)
	}
	if c.GRPCPort == c.MetricsPort {
		l.fail("GRPC_PORT and METRICS_PORT must differ, both are %d", c.GRPCPort)
	}
	return c
}

// Scale applies DemoTimeScale to a duration. Every wall-clock wait in the
// system goes through this, so one env var compresses the whole demo.
func (c Common) Scale(d time.Duration) time.Duration {
	if c.DemoTimeScale == 1 {
		return d
	}
	return time.Duration(float64(d) / c.DemoTimeScale)
}
