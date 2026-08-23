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

	"github.com/thisizaro/Momotaro/internal/platform/clock"
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

// defaultInvariantCheckInterval is how often the continuous verifier
// (services/audit/internal/server/watch.go) re-checks every batch.
// Overridable via INVARIANT_CHECK_INTERVAL.
const defaultInvariantCheckInterval = time.Minute

// serviceConfig adds this service's own settings to the shared ones.
type serviceConfig struct {
	config.Common
	InvariantCheckInterval time.Duration
}

func loadConfig() (serviceConfig, error) {
	l := config.NewLoader()
	cfg := serviceConfig{
		Common:                 config.LoadCommon(l, serviceName),
		InvariantCheckInterval: l.Duration("INVARIANT_CHECK_INTERVAL", defaultInvariantCheckInterval),
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

	auditServer := server.New(pool)

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.GRPCPort))
	if err != nil {
		return fmt.Errorf("listen on grpc port %d: %w", cfg.GRPCPort, err)
	}

	grpcServer := grpc.NewServer(grpc.ChainUnaryInterceptor(
		interceptors.UnaryServerRecovery(),
		interceptors.UnaryServerRequireDeadline(),
	))
	auditv1.RegisterAuditServiceServer(grpcServer, auditServer)

	serveErr := make(chan error, 1)
	go func() {
		log.Info("grpc server listening", "port", cfg.GRPCPort)
		serveErr <- grpcServer.Serve(lis)
	}()

	// The continuous invariant verifier: the other half of this service's
	// job (services/audit/AGENTS.md), running independently of any caller.
	watcher := server.NewWatcher(auditServer, clock.New(), cfg.InvariantCheckInterval, log)
	go watcher.Run(ctx)
	log.Info("invariant watcher started", "interval", cfg.InvariantCheckInterval)

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
