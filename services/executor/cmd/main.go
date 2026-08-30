// Command executor is the entrypoint for the executor service.
//
// Executes a chosen action exactly once against the recovery and notification ports.
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
	"github.com/thisizaro/Momotaro/internal/platform/logger"
	"github.com/thisizaro/Momotaro/internal/platform/metrics"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	"github.com/thisizaro/Momotaro/internal/platform/shutdown"
	executorv1 "github.com/thisizaro/Momotaro/proto/gen/executor/v1"
	notifierv1 "github.com/thisizaro/Momotaro/proto/gen/notifier/v1"
	worldsimv1 "github.com/thisizaro/Momotaro/proto/gen/worldsim/v1"
	"github.com/thisizaro/Momotaro/services/executor/internal/attempt"
	"github.com/thisizaro/Momotaro/services/executor/internal/ports"
	"github.com/thisizaro/Momotaro/services/executor/internal/server"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const serviceName = "executor"

// serviceConfig adds this service's own settings to the shared ones.
type serviceConfig struct {
	config.Common
	WorldSimulatorAddr        string
	NotificationSimulatorAddr string
	CallTimeout               time.Duration
}

func loadConfig() (serviceConfig, error) {
	l := config.NewLoader()
	cfg := serviceConfig{
		Common:                    config.LoadCommon(l, serviceName),
		WorldSimulatorAddr:        l.Str("WORLD_SIMULATOR_ADDR"),
		NotificationSimulatorAddr: l.Str("NOTIFICATION_SIMULATOR_ADDR"),
		CallTimeout:               l.Duration("CALL_TIMEOUT", 5*time.Second),
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

	worldSimConn, err := grpc.NewClient(cfg.WorldSimulatorAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(interceptors.UnaryClientDefaultDeadline(cfg.CallTimeout)))
	if err != nil {
		return fmt.Errorf("dial world simulator at %s: %w", cfg.WorldSimulatorAddr, err)
	}
	defer worldSimConn.Close()

	notificationSimConn, err := grpc.NewClient(cfg.NotificationSimulatorAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(interceptors.UnaryClientDefaultDeadline(cfg.CallTimeout)))
	if err != nil {
		return fmt.Errorf("dial notification simulator at %s: %w", cfg.NotificationSimulatorAddr, err)
	}
	defer notificationSimConn.Close()

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
	// The two ports from docs/ARCHITECTURE.md section 3b. Phase 1 backed
	// these with in-process scripted stubs (ports/stub.go); this is the
	// Phase 5 swap to real gRPC clients dialling demo/world-simulator and
	// demo/notification-simulator, with no change to internal/server.
	clk := clock.New()
	recovery := ports.NewWorldSimRecovery(worldsimv1.NewWorldSimulatorServiceClient(worldSimConn))
	notification := ports.NewNotificationSimAdapter(notifierv1.NewNotificationSimulatorServiceClient(notificationSimConn))
	router := ports.NewRouter(recovery, notification)
	executorv1.RegisterExecutorServiceServer(grpcServer,
		server.New(attempt.NewStore(pool), router, clk))

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
