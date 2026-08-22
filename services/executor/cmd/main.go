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
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

const serviceName = "executor"

func main() {
	// Root context cancelled on SIGTERM/SIGINT so shutdown is graceful.
	// ENGINEERING.md section 6: drain in-flight work, commit offsets, exit.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", serviceName)

	if err := run(ctx, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
	log.Info("shutdown complete")
}

func run(ctx context.Context, log *slog.Logger) error {
	// TODO(executor): load+validate config (fail fast), dial dependencies,
	// start the gRPC server / Kafka consumer, block until ctx is done.
	log.Info("not implemented yet")
	<-ctx.Done()
	return nil
}
