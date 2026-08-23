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
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/clock"
	"github.com/thisizaro/Momotaro/internal/platform/config"
	"github.com/thisizaro/Momotaro/internal/platform/interceptors"
	"github.com/thisizaro/Momotaro/internal/platform/kafkax"
	"github.com/thisizaro/Momotaro/internal/platform/logger"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	"github.com/thisizaro/Momotaro/internal/platform/shutdown"
	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	executorv1 "github.com/thisizaro/Momotaro/proto/gen/executor/v1"
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
	}
	return cfg, l.Err()
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

	// Scale applies DEMO_TIME_SCALE (docs/ARCHITECTURE.md section 17): every
	// wall-clock wait in the system compresses through this one knob, and
	// these Phase 1 fixed delays are wall-clock waits like any other.
	engCfg := engine.Config{
		CallTimeout: cfg.CallTimeout,
		RetryDelay:  cfg.Scale(cfg.RetryDelay),
		NudgeDelay:  cfg.Scale(cfg.NudgeDelay),
		DLQTopic:    cfg.DLQTopic,
	}
	eng := engine.New(pool,
		classifierv1.NewClassifierServiceClient(classifierConn),
		executorv1.NewExecutorServiceClient(executorConn),
		dlqProducer, clock.New(), engCfg)

	schedCfg := engine.SchedulerConfig{CallTimeout: cfg.CallTimeout, PollInterval: cfg.Scale(cfg.PollInterval), DLQTopic: cfg.DLQTopic}
	scheduler := engine.NewScheduler(pool, executorv1.NewExecutorServiceClient(executorConn), dlqProducer, clock.New(), schedCfg)

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

	if shutErr := shutdown.Close(10*time.Second, func(ctx context.Context) error { consumer.Close(); return nil }); shutErr != nil {
		return errors.Join(runErr, shutErr)
	}
	return runErr
}
