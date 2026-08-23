// Command audit is the entrypoint for the audit service.
//
// Serves record audit trails and continuously verifies the correctness invariants.
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
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/config"
	"github.com/thisizaro/Momotaro/internal/platform/interceptors"
	"github.com/thisizaro/Momotaro/internal/platform/logger"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	"github.com/thisizaro/Momotaro/internal/platform/shutdown"
	auditv1 "github.com/thisizaro/Momotaro/proto/gen/audit/v1"
	"github.com/thisizaro/Momotaro/services/audit/internal/server"
	"google.golang.org/grpc"
)

const serviceName = "audit"

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

func run(ctx context.Context, cfg config.Common, log *slog.Logger) error {
	connectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	pool, err := pgxpkg.NewPool(connectCtx, cfg.PostgresDSN)
	cancel()
	if err != nil {
		return fmt.Errorf("connect to postgres: %w", err)
	}
	defer pool.Close()

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.GRPCPort))
	if err != nil {
		return fmt.Errorf("listen on grpc port %d: %w", cfg.GRPCPort, err)
	}

	grpcServer := grpc.NewServer(grpc.ChainUnaryInterceptor(
		interceptors.UnaryServerRecovery(),
		interceptors.UnaryServerRequireDeadline(),
	))
	auditv1.RegisterAuditServiceServer(grpcServer, server.New(pool))

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

	return shutdown.Close(10*time.Second, func(ctx context.Context) error {
		grpcServer.GracefulStop()
		return nil
	})
}
