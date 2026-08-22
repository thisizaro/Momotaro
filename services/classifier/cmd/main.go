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
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/config"
	"github.com/thisizaro/Momotaro/internal/platform/logger"
	"github.com/thisizaro/Momotaro/internal/platform/shutdown"
	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	"github.com/thisizaro/Momotaro/services/classifier/internal/server"
	"google.golang.org/grpc"
)

const serviceName = "classifier"

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

// slogFallback is used only if config loading itself fails, before the real
// logger (which needs the validated log level) can be built.
func slogFallback() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", serviceName)
}

func run(ctx context.Context, cfg config.Common, log *slog.Logger) error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.GRPCPort))
	if err != nil {
		return fmt.Errorf("listen on grpc port %d: %w", cfg.GRPCPort, err)
	}

	grpcServer := grpc.NewServer()
	classifierv1.RegisterClassifierServiceServer(grpcServer, server.New())

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
