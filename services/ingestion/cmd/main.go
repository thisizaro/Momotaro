// Command ingestion is the entrypoint for the ingestion service.
//
// Accepts failure events (webhook or batch) and publishes them to raw.events.
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
	ingestionv1 "github.com/thisizaro/Momotaro/proto/gen/ingestion/v1"
	"github.com/thisizaro/Momotaro/services/ingestion/internal/server"
	"google.golang.org/grpc"
)

const serviceName = "ingestion"

// defaultRawEventsTopic is the topic Ingestion publishes to per
// docs/ARCHITECTURE.md section 8 (three topics, not one per hop).
// Overridable via RAW_EVENTS_TOPIC so the walking-skeleton integration test
// can run against an isolated topic instead of the shared one.
const defaultRawEventsTopic = "raw.events"

// defaultRollingBatchSource names the shared BATCH row that ungrouped
// SubmitEvent calls attach to (proto/ingestion/v1/ingestion.proto:
// "grouped into an implicit rolling batch so every record is reportable").
// Overridable via ROLLING_BATCH_SOURCE for the same reason RAW_EVENTS_TOPIC
// is: so tests don't share, and pollute, the production rolling batch.
const defaultRollingBatchSource = "webhook"

func main() {
	// Root context cancelled on SIGTERM/SIGINT so shutdown is graceful.
	// ENGINEERING.md section 6: drain in-flight work, commit offsets, exit.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	l := config.NewLoader()
	cfg := config.LoadCommon(l, serviceName)
	topic := l.StrDefault("RAW_EVENTS_TOPIC", defaultRawEventsTopic)
	rollingBatchSource := l.StrDefault("ROLLING_BATCH_SOURCE", defaultRollingBatchSource)
	if err := l.Err(); err != nil {
		slogFallback().Error("fatal", "err", err)
		os.Exit(1)
	}

	log := logger.New(cfg.ServiceName, cfg.LogLevel)

	if err := run(ctx, cfg, topic, rollingBatchSource, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
	log.Info("shutdown complete")
}

func slogFallback() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", serviceName)
}

func run(ctx context.Context, cfg config.Common, topic, rollingBatchSource string, log *slog.Logger) error {
	connectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	pool, err := pgxpkg.NewPool(connectCtx, cfg.PostgresDSN)
	cancel()
	if err != nil {
		return fmt.Errorf("connect to postgres: %w", err)
	}
	defer pool.Close()

	producer, err := kafkax.NewProducer(cfg.KafkaBrokers)
	if err != nil {
		return fmt.Errorf("connect to kafka: %w", err)
	}
	defer producer.Close()

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

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.GRPCPort))
	if err != nil {
		return fmt.Errorf("listen on grpc port %d: %w", cfg.GRPCPort, err)
	}

	grpcServer := grpc.NewServer(grpc.ChainUnaryInterceptor(
		interceptors.UnaryServerRecovery(),
		interceptors.UnaryServerRequireDeadline(),
		interceptors.UnaryServerMetrics(m),
	))
	ingestionv1.RegisterIngestionServiceServer(grpcServer, server.New(pool, producer, clock.New(), topic, rollingBatchSource))

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
