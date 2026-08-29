// Command decision-engine is the entrypoint for the decision-engine service.
//
// Owns each record's state machine, the keyed worker pool, and the scheduler worker.
//
// See docs/ARCHITECTURE.md for where this sits in the system, and
// docs/ENGINEERING.md for the rules this service must follow (fail-fast
// config, graceful shutdown, injected clock, context deadlines).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/clock"
	"github.com/thisizaro/Momotaro/internal/platform/config"
	"github.com/thisizaro/Momotaro/internal/platform/interceptors"
	"github.com/thisizaro/Momotaro/internal/platform/kafkax"
	"github.com/thisizaro/Momotaro/internal/platform/logger"
	"github.com/thisizaro/Momotaro/internal/platform/metrics"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	"github.com/thisizaro/Momotaro/internal/platform/shutdown"
	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	executorv1 "github.com/thisizaro/Momotaro/proto/gen/executor/v1"
	"github.com/thisizaro/Momotaro/services/decision-engine/internal/economics"
	"github.com/thisizaro/Momotaro/services/decision-engine/internal/engine"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const serviceName = "decision-engine"

// Fixed per docs/ARCHITECTURE.md section 8 (three topics, not one per hop).
// All overridable via env so the walking-skeleton and depth integration
// tests can run against isolated topics/groups instead of the shared
// production ones.
const (
	defaultRawEventsTopic         = "raw.events"
	defaultRawEventsConsumerGroup = "decision-engine"
	defaultDLQTopic               = "raw.events.dlq"
)

// serviceConfig adds this service's own settings to the shared ones.
type serviceConfig struct {
	config.Common
	ClassifierAddr string
	ExecutorAddr   string
	CallTimeout    time.Duration
	Topic          string
	ConsumerGroup  string
	DLQTopic       string
	WorkerPoolSize int
	// RetryDelay/NudgeDelay: Phase 1 placeholders, see engine.Config.
	RetryDelay   time.Duration
	NudgeDelay   time.Duration
	PollInterval time.Duration
	// Guardrails: the hard limits from docs/PRD.md section 11. MaxRetries
	// mirrors NPCI-style mandate debit limits. The two durations are scaled
	// by DEMO_TIME_SCALE like every other wall-clock wait, because a 7 day
	// recovery window would otherwise never close inside a demo.
	// Paths to the checked-in economics config. Files rather than compiled-in
	// constants so a judge can read and argue with the numbers.
	InterventionCostsPath string
	RecoveryPriorsPath    string

	MaxRetries      int
	MaxContacts     int
	ContactCooldown time.Duration
	RecoveryWindow  time.Duration

	// LLMSampleRate is LLM_SAMPLE_RATE (docs/PHASE3_IMPLEMENTATION.md Unit
	// H): the fraction of records sampled deterministically for a live
	// model call rather than force_rules_only. Default 0.0, so every
	// existing test and every default run stays free of outbound LLM calls.
	LLMSampleRate float64

	// ClassifyConfidenceThreshold is CLASSIFY_CONFIDENCE_THRESHOLD
	// (docs/PHASE3_IMPLEMENTATION.md Unit G): below this, a classification
	// is escalated as a safety call rather than priced. Default 0.0, so
	// every existing test and every default run escalates nothing on
	// confidence (the rules engine's own confidence values are all > 0).
	ClassifyConfidenceThreshold float64
}

// guardrailsFrom builds the engine's guardrail limits from the loaded config.
// The two durations are scaled like every other wall-clock wait, or a 7 day
// recovery window would never close inside a demo.
func guardrailsFrom(cfg serviceConfig) engine.GuardrailConfig {
	return engine.GuardrailConfig{
		MaxRetries:      cfg.MaxRetries,
		MaxContacts:     cfg.MaxContacts,
		ContactCooldown: cfg.Scale(cfg.ContactCooldown),
		RecoveryWindow:  cfg.Scale(cfg.RecoveryWindow),
	}
}

func loadConfig() (serviceConfig, error) {
	l := config.NewLoader()
	cfg := serviceConfig{
		Common:         config.LoadCommon(l, serviceName),
		ClassifierAddr: l.Str("CLASSIFIER_ADDR"),
		ExecutorAddr:   l.Str("EXECUTOR_ADDR"),
		CallTimeout:    l.Duration("CALL_TIMEOUT", 5*time.Second),
		Topic:          l.StrDefault("RAW_EVENTS_TOPIC", defaultRawEventsTopic),
		ConsumerGroup:  l.StrDefault("RAW_EVENTS_CONSUMER_GROUP", defaultRawEventsConsumerGroup),
		DLQTopic:       l.StrDefault("RAW_EVENTS_DLQ_TOPIC", defaultDLQTopic),
		WorkerPoolSize: l.Int("WORKER_POOL_SIZE", 32),
		RetryDelay:     l.Duration("RETRY_DELAY", 30*time.Second),
		NudgeDelay:     l.Duration("NUDGE_DELAY", 30*time.Second),
		PollInterval:   l.Duration("SCHEDULER_POLL_INTERVAL", 2*time.Second),

		InterventionCostsPath: l.StrDefault("INTERVENTION_COSTS_PATH", "configs/intervention_costs.yaml"),
		RecoveryPriorsPath:    l.StrDefault("RECOVERY_PRIORS_PATH", "configs/recovery_priors.yaml"),

		MaxRetries:      l.Int("MAX_RETRIES", 3),
		MaxContacts:     l.Int("MAX_CONTACTS", 3),
		ContactCooldown: l.Duration("CONTACT_COOLDOWN", 24*time.Hour),
		RecoveryWindow:  l.Duration("RECOVERY_WINDOW", 7*24*time.Hour),

		LLMSampleRate: l.Float("LLM_SAMPLE_RATE", 0.0),

		ClassifyConfidenceThreshold: l.Float("CLASSIFY_CONFIDENCE_THRESHOLD", 0.0),
	}
	if err := l.Err(); err != nil {
		return cfg, err
	}
	// Guardrails are validated here rather than trusted, because their zero
	// value silently escalates every record instead of failing visibly.
	if err := guardrailsFrom(cfg).Validate(); err != nil {
		return cfg, fmt.Errorf("guardrail config: %w", err)
	}
	// Out of range must fail at startup, not silently clamp: a value of 3
	// intended as "3 in 10" would otherwise sample the whole batch.
	if cfg.LLMSampleRate < 0 || cfg.LLMSampleRate > 1 {
		return cfg, fmt.Errorf("LLM_SAMPLE_RATE must be in [0,1], got %v", cfg.LLMSampleRate)
	}
	// Confidence itself is documented as always in [0,1] (classifier.proto,
	// enforced by the classifier's own validate.go), so a threshold outside
	// that range could only ever mean a typo, never a deliberate setting.
	if cfg.ClassifyConfidenceThreshold < 0 || cfg.ClassifyConfidenceThreshold > 1 {
		return cfg, fmt.Errorf("CLASSIFY_CONFIDENCE_THRESHOLD must be in [0,1], got %v", cfg.ClassifyConfidenceThreshold)
	}
	return cfg, nil
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

func slogFallback() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", serviceName)
}

func run(ctx context.Context, cfg serviceConfig, log *slog.Logger) error {
	connectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	pool, err := pgxpkg.NewPool(connectCtx, cfg.PostgresDSN)
	cancel()
	if err != nil {
		return fmt.Errorf("connect to postgres: %w", err)
	}
	defer pool.Close()

	classifierConn, err := grpc.NewClient(cfg.ClassifierAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(interceptors.UnaryClientDefaultDeadline(cfg.CallTimeout)))
	if err != nil {
		return fmt.Errorf("dial classifier at %s: %w", cfg.ClassifierAddr, err)
	}
	defer classifierConn.Close()

	executorConn, err := grpc.NewClient(cfg.ExecutorAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(interceptors.UnaryClientDefaultDeadline(cfg.CallTimeout)))
	if err != nil {
		return fmt.Errorf("dial executor at %s: %w", cfg.ExecutorAddr, err)
	}
	defer executorConn.Close()

	dlqProducer, err := kafkax.NewProducer(cfg.KafkaBrokers)
	if err != nil {
		return fmt.Errorf("connect dlq producer to kafka: %w", err)
	}
	defer dlqProducer.Close()

	// decision-engine has no inbound gRPC server to instrument (it is a
	// Kafka consumer with only outbound gRPC clients), so this exposes
	// Go/process metrics only. The consumer-lag gauge (docs/PLAN.md Phase 4)
	// registers into the same m.Registry() once it lands.
	m := metrics.New()
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

	// Scale applies DEMO_TIME_SCALE (docs/ARCHITECTURE.md section 17): every
	// wall-clock wait in the system compresses through this one knob, and
	// these Phase 1 fixed delays are wall-clock waits like any other.
	// Loaded before anything starts consuming: a Decision Engine that cannot
	// price its actions must not run at all, because the failure mode is
	// silent. Every action would score zero and every record would close as
	// uneconomic, which looks exactly like a healthy agent that has decided
	// nothing is ever worth doing.
	model, err := economics.Load(cfg.InterventionCostsPath, cfg.RecoveryPriorsPath)
	if err != nil {
		return fmt.Errorf("load economics config: %w", err)
	}
	log.Info("economics config loaded", "costs", cfg.InterventionCostsPath, "priors", cfg.RecoveryPriorsPath)

	engCfg := engine.Config{
		CallTimeout: cfg.CallTimeout,
		// RetryDelay is deliberately NOT scaled here: retryDueAt
		// (schedule.go) scales it itself using TimeScale below, because it
		// also needs the raw factor for the INSUFFICIENT_FUNDS
		// salary-window branch. Scaling it here too would compress it
		// twice -- invisible at DEMO_TIME_SCALE=1 (production; scaleDuration
		// no-ops there) but silently near-instant at any other scale.
		RetryDelay:                  cfg.RetryDelay,
		NudgeDelay:                  cfg.Scale(cfg.NudgeDelay),
		DLQTopic:                    cfg.DLQTopic,
		TimeScale:                   cfg.DemoTimeScale,
		Guardrails:                  guardrailsFrom(cfg),
		LLMSampleRate:               cfg.LLMSampleRate,
		ClassifyConfidenceThreshold: cfg.ClassifyConfidenceThreshold,
	}
	eng := engine.New(pool,
		classifierv1.NewClassifierServiceClient(classifierConn),
		executorv1.NewExecutorServiceClient(executorConn),
		dlqProducer, clock.New(), model, engCfg)

	schedCfg := engine.SchedulerConfig{
		CallTimeout:  cfg.CallTimeout,
		PollInterval: cfg.Scale(cfg.PollInterval),
		DLQTopic:     cfg.DLQTopic,
		// See engCfg.RetryDelay above: retryDueAt scales this itself.
		RetryDelay: cfg.RetryDelay,
		NudgeDelay: cfg.Scale(cfg.NudgeDelay),
		TimeScale:  cfg.DemoTimeScale,
		Guardrails: guardrailsFrom(cfg),
	}
	scheduler := engine.NewScheduler(pool, executorv1.NewExecutorServiceClient(executorConn), dlqProducer, clock.New(), model, schedCfg)

	consumer, err := kafkax.NewConsumer(cfg.KafkaBrokers, cfg.ConsumerGroup, []string{cfg.Topic})
	if err != nil {
		return fmt.Errorf("connect to kafka: %w", err)
	}
	defer consumer.Close()

	// A separate cancel from the signal-driven ctx: if either the consumer
	// or the scheduler exits on its own (error or otherwise), the other
	// must stop too, not run on unsupervised until a signal eventually
	// arrives.
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	consumeErr := make(chan error, 1)
	go func() {
		consumeErr <- consumer.ConsumeKeyed(runCtx, cfg.WorkerPoolSize, eng.HandleMessage)
	}()

	schedulerErr := make(chan error, 1)
	go func() {
		schedulerErr <- scheduler.Run(runCtx)
	}()

	log.Info("running", "topic", cfg.Topic, "group", cfg.ConsumerGroup, "worker_pool_size", cfg.WorkerPoolSize, "poll_interval", cfg.PollInterval)

	var runErr error
	select {
	case err := <-consumeErr:
		cancelRun()
		<-schedulerErr
		if err != nil && !errors.Is(err, context.Canceled) {
			runErr = fmt.Errorf("consume %s: %w", cfg.Topic, err)
		}
	case err := <-schedulerErr:
		cancelRun()
		<-consumeErr
		if err != nil && !errors.Is(err, context.Canceled) {
			runErr = fmt.Errorf("scheduler: %w", err)
		}
	case <-ctx.Done():
		<-consumeErr
		<-schedulerErr
	}

	if shutErr := shutdown.Close(10*time.Second,
		func(ctx context.Context) error { consumer.Close(); return nil },
		func(ctx context.Context) error { return metricsServer.Shutdown(ctx) },
	); shutErr != nil {
		return errors.Join(runErr, shutErr)
	}
	return runErr
}
