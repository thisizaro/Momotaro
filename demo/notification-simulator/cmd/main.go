// Command notification-simulator is the entrypoint for the
// notification-simulator service.
//
// Stands in for an SMS/WhatsApp provider. Logs what it would have sent.
//
// See docs/ARCHITECTURE.md for where this sits in the system, and
// docs/ENGINEERING.md for the rules this service must follow (fail-fast
// config, graceful shutdown, injected clock, context deadlines). DEMO
// ONLY: a real deployment deletes this whole component (section 3b) in
// favour of a real SMS/WhatsApp provider on the same NotificationPort
// interface.
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

	"github.com/thisizaro/Momotaro/demo/notification-simulator/internal/server"
	"github.com/thisizaro/Momotaro/internal/platform/config"
	"github.com/thisizaro/Momotaro/internal/platform/interceptors"
	"github.com/thisizaro/Momotaro/internal/platform/logger"
	"github.com/thisizaro/Momotaro/internal/platform/metrics"
	"github.com/thisizaro/Momotaro/internal/platform/shutdown"
	notifierv1 "github.com/thisizaro/Momotaro/proto/gen/notifier/v1"
	"google.golang.org/grpc"
)

const serviceName = "notification-simulator"

func main() {
	// Root context cancelled on SIGTERM/SIGINT so shutdown is graceful.
	// ENGINEERING.md section 6: drain in-flight work, commit offsets, exit.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	l := config.NewLoader()
	cfg := config.LoadCommon(l, serviceName)
	if err := l.Err(); err != nil {
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

// run needs no Postgres and no Redis: this service holds no state and
// answers every call from its own checked-in pricing table alone
// (internal/server/server.go).
func run(ctx context.Context, cfg config.Common, log *slog.Logger) error {
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
	notifierv1.RegisterNotificationSimulatorServiceServer(grpcServer, server.New(log))

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
