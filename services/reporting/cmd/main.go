// Command reporting is the entrypoint for the reporting service.
//
// Aggregates batch results and streams live updates.
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
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/auditevent"
	"github.com/thisizaro/Momotaro/internal/platform/config"
	"github.com/thisizaro/Momotaro/internal/platform/interceptors"
	"github.com/thisizaro/Momotaro/internal/platform/kafkax"
	"github.com/thisizaro/Momotaro/internal/platform/logger"
	"github.com/thisizaro/Momotaro/internal/platform/metrics"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	"github.com/thisizaro/Momotaro/internal/platform/shutdown"
	reportingv1 "github.com/thisizaro/Momotaro/proto/gen/reporting/v1"
	"github.com/thisizaro/Momotaro/services/reporting/internal/server"
	"google.golang.org/grpc"
)

const serviceName = "reporting"

// serviceConfig adds this service's own settings to the shared ones.
type serviceConfig struct {
	config.Common
	// AuditEventsTopic/ConsumerGroup: docs/PHASE5_IMPLEMENTATION.md Unit F.
	// Overridable for the same reason every other topic/group in this
	// project is (docs/INCIDENTS.md): an e2e test's isolated stack needs
	// its own topic, or a fresh consumer group on the shared one would
	// replay history against records that no longer exist.
	AuditEventsTopic string
	ConsumerGroup    string
}

func loadConfig() (serviceConfig, error) {
	l := config.NewLoader()
	cfg := serviceConfig{
		Common:           config.LoadCommon(l, serviceName),
		AuditEventsTopic: l.StrDefault("AUDIT_EVENTS_TOPIC", auditevent.Topic),
		ConsumerGroup:    l.StrDefault("AUDIT_EVENTS_CONSUMER_GROUP", "reporting"),
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

	// One Hub, shared by every StreamBatchUpdates call (server.New) and the
	// Kafka consumer that feeds it (server.NewAuditConsumer) -- neither
	// knows about the other beyond this shared instance
	// (docs/PHASE5_IMPLEMENTATION.md Unit F).
	hub := server.NewHub()
	reportingServer := server.New(pool, hub)

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
	reportingv1.RegisterReportingServiceServer(grpcServer, reportingServer)

	grpcServeErr := make(chan error, 1)
	go func() {
		log.Info("grpc server listening", "port", cfg.GRPCPort)
		grpcServeErr <- grpcServer.Serve(lis)
	}()

	consumer, err := kafkax.NewConsumer(cfg.KafkaBrokers, cfg.ConsumerGroup, []string{cfg.AuditEventsTopic})
	if err != nil {
		return fmt.Errorf("connect to kafka: %w", err)
	}
	defer consumer.Close()

	// A separate cancel from the signal-driven ctx: if the consumer exits
	// on its own (error or otherwise), the gRPC server should stop too,
	// not run on unsupervised until a signal eventually arrives. Mirrors
	// services/decision-engine/cmd/main.go's own run-context split.
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	auditConsumer := server.NewAuditConsumer(hub, log)
	consumeErr := make(chan error, 1)
	go func() {
		consumeErr <- consumer.Consume(runCtx, auditConsumer.HandleMessage)
	}()

	log.Info("running", "audit_events_topic", cfg.AuditEventsTopic, "consumer_group", cfg.ConsumerGroup)

	var runErr error
	select {
	case err := <-consumeErr:
		cancelRun()
		if err != nil && !errors.Is(err, context.Canceled) {
			runErr = fmt.Errorf("consume %s: %w", cfg.AuditEventsTopic, err)
		}
	case err := <-grpcServeErr:
		cancelRun()
		<-consumeErr
		if err != nil {
			runErr = fmt.Errorf("grpc server: %w", err)
		}
	case <-ctx.Done():
		<-consumeErr
	}

	if shutErr := shutdown.Close(10*time.Second,
		func(ctx context.Context) error { consumer.Close(); return nil },
		func(ctx context.Context) error { grpcServer.GracefulStop(); return nil },
		func(ctx context.Context) error { return metricsServer.Shutdown(ctx) },
	); shutErr != nil && runErr == nil {
		runErr = shutErr
	}
	return runErr
}
