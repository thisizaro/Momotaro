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

// defaultRawEventsTopic and defaultRawEventsConsumerGroup are fixed per
// docs/ARCHITECTURE.md section 8 (three topics, not one per hop). Both are
// overridable via env so the walking-skeleton integration test can run
// against an isolated topic/group instead of the shared production ones.
const (
	defaultRawEventsTopic         = "raw.events"
	defaultRawEventsConsumerGroup = "decision-engine"
)

// serviceConfig adds this service's own settings to the shared ones.
type serviceConfig struct {
	config.Common
	ClassifierAddr string
	ExecutorAddr   string
	CallTimeout    time.Duration
	Topic          string
	ConsumerGroup  string
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

	classifierConn, err := grpc.NewClient(cfg.ClassifierAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dial classifier at %s: %w", cfg.ClassifierAddr, err)
	}
	defer classifierConn.Close()

	executorConn, err := grpc.NewClient(cfg.ExecutorAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dial executor at %s: %w", cfg.ExecutorAddr, err)
	}
	defer executorConn.Close()

	eng := engine.New(pool,
		classifierv1.NewClassifierServiceClient(classifierConn),
		executorv1.NewExecutorServiceClient(executorConn),
		clock.New(), cfg.CallTimeout)

	consumer, err := kafkax.NewConsumer(cfg.KafkaBrokers, cfg.ConsumerGroup, []string{cfg.Topic})
	if err != nil {
		return fmt.Errorf("connect to kafka: %w", err)
	}
	defer consumer.Close()

	consumeErr := make(chan error, 1)
	go func() {
		consumeErr <- consumer.Consume(ctx, eng.HandleMessage)
	}()

	log.Info("consuming", "topic", cfg.Topic, "group", cfg.ConsumerGroup)

	select {
	case err := <-consumeErr:
		if err != nil && !errors.Is(err, context.Canceled) {
			return fmt.Errorf("consume %s: %w", cfg.Topic, err)
		}
		return nil
	case <-ctx.Done():
	}

	return shutdown.Close(10*time.Second, func(ctx context.Context) error {
		consumer.Close()
		return nil
	})
}
