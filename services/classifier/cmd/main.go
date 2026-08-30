// Command classifier is the entrypoint for the classifier service.
//
// Diagnoses root cause and composes nudge text. Owns all LLM access.
//
// See docs/ARCHITECTURE.md for where this sits in the system, and
// docs/ENGINEERING.md for the rules this service must follow (fail-fast
// config, graceful shutdown, injected clock, context deadlines).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/thisizaro/Momotaro/internal/platform/clock"
	"github.com/thisizaro/Momotaro/internal/platform/config"
	"github.com/thisizaro/Momotaro/internal/platform/interceptors"
	"github.com/thisizaro/Momotaro/internal/platform/logger"
	"github.com/thisizaro/Momotaro/internal/platform/metrics"
	"github.com/thisizaro/Momotaro/internal/platform/shutdown"
	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	"github.com/thisizaro/Momotaro/services/classifier/internal/llm"
	"github.com/thisizaro/Momotaro/services/classifier/internal/provider"
	"github.com/thisizaro/Momotaro/services/classifier/internal/rules"
	"github.com/thisizaro/Momotaro/services/classifier/internal/server"
	"google.golang.org/grpc"
)

const serviceName = "classifier"

// defaultProviderChain stays rules-only even though the providers are now
// decided (groq,gemini,rules, docs/DECISIONS.md 2026-08-28).
//
// That is deliberate, not leftover. Both free tiers are capped per day (Groq
// gives 1,000 requests/day on gpt-oss-20b, org-wide rather than per key), and
// the whole test suite plus every e2e run would otherwise spend that quota on
// records whose answers nobody reads. The live chain is opt-in through
// configs/demo.env (Phase 3 Unit H). One accidental loop against a live chain
// burns the day, and it will happen at the worst possible moment.
const defaultProviderChain = "rules"

// defaultLLMTimeout caps any single non-terminal rung.
//
// MEASURED, not guessed. 16 live Groq calls against gpt-oss-20b at
// reasoning_effort:low: min 237ms, p50 ~570ms, max 688ms. 2s is roughly three
// times the observed maximum, and the worst case for the default groq,rules
// chain is 2s + CHAIN_RESERVE = 2.15s, inside the Decision Engine's 5s
// CALL_TIMEOUT and inside PRD.md section 10's 3s p95 target.
//
// The value is unchanged from the pre-Unit-B placeholder; its status is not.
// It was a guess that happened to be right, and the published 3.05s
// time-to-first-token figure for this model (which is the HIGH reasoning
// variant) said it was wrong. Only the measurement settled it.
//
// Note this does not fit Gemini, measured at p50 3.0s and max 6.19s. See
// docs/DECISIONS.md 2026-08-28 for why that keeps Gemini out of the default
// chain rather than raising this number.
const defaultLLMTimeout = 2 * time.Second

// defaultChainReserve is held back from the caller's deadline so the terminal
// rules rung and the response marshal always fit inside it, however badly the
// rungs above it behave. See provider/budget.go for why a per-rung timeout on
// its own is not enough. 150ms is generous for a pure-CPU table lookup plus a
// protobuf marshal, and cheap against a 5s CALL_TIMEOUT.
const defaultChainReserve = 150 * time.Millisecond

// Circuit breaker defaults (Phase 3 Unit D). Five consecutive failures is a
// clear signal rather than a blip; thirty seconds is long enough that a real
// outage is not re-probed every request and short enough that a recovered
// provider rejoins within a demo. A 429 ignores the threshold entirely and
// prefers the provider's own Retry-After (breaker.go).
const (
	defaultBreakerThreshold = 5
	defaultBreakerCooldown  = 30 * time.Second
)

// serviceConfig adds this service's own settings to the shared ones.
type serviceConfig struct {
	config.Common
	ProviderChain []string
	LLMTimeout    time.Duration
	ChainReserve  time.Duration

	BreakerThreshold int
	BreakerCooldown  time.Duration

	GroqAPIKey    string
	GroqBaseURL   string
	GroqModel     string
	GeminiAPIKey  string
	GeminiBaseURL string
	GeminiModel   string
}

func loadConfig() (serviceConfig, error) {
	l := config.NewLoader()
	cfg := serviceConfig{
		Common:        config.LoadCommon(l, serviceName),
		ProviderChain: splitCSV(l.StrDefault("LLM_PROVIDER_CHAIN", defaultProviderChain)),
		LLMTimeout:    l.Duration("LLM_TIMEOUT", defaultLLMTimeout),
		ChainReserve:  l.Duration("CHAIN_RESERVE", defaultChainReserve),

		BreakerThreshold: l.Int("LLM_BREAKER_THRESHOLD", defaultBreakerThreshold),
		BreakerCooldown:  l.Duration("LLM_BREAKER_COOLDOWN", defaultBreakerCooldown),

		// Keys are read but never required here: a chain that does not name a
		// provider must not need its key. The requirement is enforced in
		// buildRegistry, where we know which rungs are actually in play.
		GroqAPIKey:    l.StrDefault("GROQ_API_KEY", ""),
		GroqBaseURL:   l.StrDefault("GROQ_BASE_URL", llm.DefaultGroqBaseURL),
		GroqModel:     l.StrDefault("GROQ_MODEL", llm.DefaultGroqModel),
		GeminiAPIKey:  l.StrDefault("GEMINI_API_KEY", ""),
		GeminiBaseURL: l.StrDefault("GEMINI_BASE_URL", llm.DefaultGeminiBaseURL),
		GeminiModel:   l.StrDefault("GEMINI_MODEL", llm.DefaultGeminiModel),
	}
	return cfg, l.Err()
}

// splitCSV parses a comma-separated list, trimming whitespace and dropping
// empty entries. Local to this service so LLM_PROVIDER_CHAIN can have a
// default (config.Loader.CSV is required-only); see loadConfig.
func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func main() {
	// Root context cancelled on SIGTERM/SIGINT so shutdown is graceful.
	// ENGINEERING.md section 6: drain in-flight work, commit offsets, exit.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	cfg, err := loadConfig()
	if err != nil {
		slogFallback().Error("fatal", "err", err)
		os.Exit(1)
	}

	log := logger.New(cfg.ServiceName, cfg.LogLevel)

	if err := run(ctx, cfg, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
	log.Info("shutdown complete")
}

// slogFallback is used only if config loading itself fails, before the real
// logger (which needs the validated log level) can be built.
func slogFallback() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", serviceName)
}

func run(ctx context.Context, cfg serviceConfig, log *slog.Logger) error {
	// Real clock in production; the breaker takes it injected so its cooldown
	// is drivable from a test (ENGINEERING.md section 2). Note budget.go's
	// rungCtx deliberately does NOT use this: it compares against a real
	// context deadline, which is wall-clock by construction.
	clk := clock.New()

	registry, err := buildRegistry(cfg, clk, log)
	if err != nil {
		return err
	}
	chainCfg := provider.Config{RungTimeout: cfg.LLMTimeout, Reserve: cfg.ChainReserve}
	chain, err := provider.NewChain(cfg.ProviderChain, registry, chainCfg, log)
	if err != nil {
		return fmt.Errorf("build provider chain: %w", err)
	}
	// The budget is worth stating at startup rather than leaving to be
	// reconstructed from two env vars during an incident: worst case is every
	// non-terminal rung burning its full timeout before the rules rung
	// answers, and that sum has to fit inside the caller's deadline
	// (CALL_TIMEOUT, 5s by default in the Decision Engine).
	worstCase := time.Duration(len(cfg.ProviderChain)-1)*cfg.LLMTimeout + cfg.ChainReserve
	log.Info("provider chain built",
		"chain", strings.Join(cfg.ProviderChain, ","),
		"rung_timeout", cfg.LLMTimeout.String(),
		"chain_reserve", cfg.ChainReserve.String(),
		"worst_case_before_terminal_rung", worstCase.String(),
	)

	nudgeRegistry, err := buildNudgeRegistry(cfg, clk, log)
	if err != nil {
		return err
	}
	nudgeChain, err := provider.NewNudgeChain(cfg.ProviderChain, nudgeRegistry, chainCfg, log)
	if err != nil {
		return fmt.Errorf("build nudge chain: %w", err)
	}

	m := metrics.New()
	llmFallback := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "llm_fallback_total",
		Help: "Classifications answered by the deterministic rules engine rather than a live LLM rung.",
	})
	m.Registry().MustRegister(llmFallback)
	nudgeFallback := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "nudge_fallback_total",
		Help: "Nudges composed from the static Hinglish template rather than a live LLM rung.",
	})
	m.Registry().MustRegister(nudgeFallback)
	metricsServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.MetricsPort),
		Handler:           m.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Info("metrics server listening", "port", cfg.MetricsPort)
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("metrics server", "err", err)
		}
	}()

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.GRPCPort))
	if err != nil {
		return fmt.Errorf("listen on grpc port %d: %w", cfg.GRPCPort, err)
	}

	grpcServer := grpc.NewServer(grpc.ChainUnaryInterceptor(
		interceptors.UnaryServerRecovery(),
		interceptors.UnaryServerRequireDeadline(),
		interceptors.UnaryServerMetrics(m),
	))
	classifierv1.RegisterClassifierServiceServer(grpcServer, server.New(chain, nudgeChain, log, llmFallback, nudgeFallback))

	serveErr := make(chan error, 1)
	go func() {
		log.Info("grpc server listening", "port", cfg.GRPCPort)
		serveErr <- grpcServer.Serve(lis)
	}()

	select {
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("grpc server: %w", err)
		}
		return nil
	case <-ctx.Done():
	}

	return shutdown.Close(10*time.Second,
		func(ctx context.Context) error {
			grpcServer.GracefulStop()
			return nil
		},
		func(ctx context.Context) error {
			return metricsServer.Shutdown(ctx)
		},
	)
}

// buildRegistry constructs only the rungs the configured chain actually names.
//
// Two properties worth stating, because both are easy to lose in a refactor.
//
// A provider named in LLM_PROVIDER_CHAIN without its key is a startup failure,
// not a rung that fails every request (docs/ENGINEERING.md section 5). And a
// provider NOT named costs nothing: no client, no key requirement, no
// possibility of an outbound call. That second property is what makes the
// rules-only default genuinely free rather than merely unused, and it is what
// keeps CI, which has no keys and no guaranteed egress, from ever depending on
// a provider.
func buildRegistry(cfg serviceConfig, clk clock.Clock, log *slog.Logger) (map[string]provider.Provider, error) {
	named := make(map[string]bool, len(cfg.ProviderChain))
	for _, n := range cfg.ProviderChain {
		named[n] = true
	}

	registry := map[string]provider.Provider{
		provider.RulesName: rules.New(log),
	}

	if named[llm.GroqName] {
		p, err := llm.NewGroq(llm.Config{
			APIKey:  cfg.GroqAPIKey,
			BaseURL: cfg.GroqBaseURL,
			Model:   cfg.GroqModel,
		}, log)
		if err != nil {
			return nil, fmt.Errorf("build %s rung: %w", llm.GroqName, err)
		}
		wrapped, err := breaker(cfg, p, clk, log)
		if err != nil {
			return nil, err
		}
		registry[llm.GroqName] = wrapped
	}

	if named[llm.GeminiName] {
		p, err := llm.NewGemini(llm.Config{
			APIKey:  cfg.GeminiAPIKey,
			BaseURL: cfg.GeminiBaseURL,
			Model:   cfg.GeminiModel,
		}, log)
		if err != nil {
			return nil, fmt.Errorf("build %s rung: %w", llm.GeminiName, err)
		}
		wrapped, err := breaker(cfg, p, clk, log)
		if err != nil {
			return nil, err
		}
		registry[llm.GeminiName] = wrapped
	}

	// A live chain costs real money against a capped free tier, so it should
	// never be a surprise discovered from a bill or a 429. Say so loudly.
	if len(registry) > 1 {
		log.Warn("live provider chain enabled, this makes real API calls",
			"chain", strings.Join(cfg.ProviderChain, ","))
	}
	return registry, nil
}

// breaker wraps one live rung so a failing provider stops costing a full
// timeout per record. The rules rung is never wrapped, and NewBreaker refuses
// to: it is the rung that cannot fail, and an open breaker in front of it
// would leave the chain with no answer at all.
func breaker(cfg serviceConfig, p provider.Provider, clk clock.Clock, log *slog.Logger) (provider.Provider, error) {
	b, err := provider.NewBreaker(p, provider.BreakerConfig{
		Threshold: cfg.BreakerThreshold,
		Cooldown:  cfg.BreakerCooldown,
	}, clk, log)
	if err != nil {
		return nil, fmt.Errorf("wrap %s in a circuit breaker: %w", p.Name(), err)
	}
	return b, nil
}

// buildNudgeRegistry is buildRegistry's ComposeNudge equivalent: only the
// rungs the configured chain actually names, the terminal rung is the
// static Hinglish template (rules.NewTemplateNudgeProvider) rather than the
// rules engine, and each live rung gets its own NudgeBreaker rather than
// sharing state with its Classify counterpart's Breaker (a deliberate,
// documented tradeoff, see docs/PHASE5_IMPLEMENTATION.md Unit E /
// docs/DECISIONS.md). A separate *llm.Provider instance per vendor here
// (rather than reusing the one buildRegistry built) costs one extra idle
// HTTP client per live provider and nothing else: llm.Provider holds no
// state that benefits from being shared.
func buildNudgeRegistry(cfg serviceConfig, clk clock.Clock, log *slog.Logger) (map[string]provider.NudgeProvider, error) {
	named := make(map[string]bool, len(cfg.ProviderChain))
	for _, n := range cfg.ProviderChain {
		named[n] = true
	}

	registry := map[string]provider.NudgeProvider{
		provider.RulesName: rules.NewTemplateNudgeProvider(),
	}

	if named[llm.GroqName] {
		p, err := llm.NewGroq(llm.Config{
			APIKey:  cfg.GroqAPIKey,
			BaseURL: cfg.GroqBaseURL,
			Model:   cfg.GroqModel,
		}, log)
		if err != nil {
			return nil, fmt.Errorf("build %s nudge rung: %w", llm.GroqName, err)
		}
		wrapped, err := nudgeBreaker(cfg, p, clk, log)
		if err != nil {
			return nil, err
		}
		registry[llm.GroqName] = wrapped
	}

	if named[llm.GeminiName] {
		p, err := llm.NewGemini(llm.Config{
			APIKey:  cfg.GeminiAPIKey,
			BaseURL: cfg.GeminiBaseURL,
			Model:   cfg.GeminiModel,
		}, log)
		if err != nil {
			return nil, fmt.Errorf("build %s nudge rung: %w", llm.GeminiName, err)
		}
		wrapped, err := nudgeBreaker(cfg, p, clk, log)
		if err != nil {
			return nil, err
		}
		registry[llm.GeminiName] = wrapped
	}

	return registry, nil
}

// nudgeBreaker is breaker's ComposeNudge equivalent.
func nudgeBreaker(cfg serviceConfig, p provider.NudgeProvider, clk clock.Clock, log *slog.Logger) (provider.NudgeProvider, error) {
	b, err := provider.NewNudgeBreaker(p, provider.BreakerConfig{
		Threshold: cfg.BreakerThreshold,
		Cooldown:  cfg.BreakerCooldown,
	}, clk, log)
	if err != nil {
		return nil, fmt.Errorf("wrap %s in a nudge circuit breaker: %w", p.Name(), err)
	}
	return b, nil
}
