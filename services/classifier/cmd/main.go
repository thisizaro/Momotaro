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
	"strings"
	"syscall"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/config"
	"github.com/thisizaro/Momotaro/internal/platform/interceptors"
	"github.com/thisizaro/Momotaro/internal/platform/logger"
	"github.com/thisizaro/Momotaro/internal/platform/shutdown"
	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	"github.com/thisizaro/Momotaro/services/classifier/internal/provider"
	"github.com/thisizaro/Momotaro/services/classifier/internal/rules"
	"github.com/thisizaro/Momotaro/services/classifier/internal/server"
	"google.golang.org/grpc"
)

const serviceName = "classifier"

// defaultProviderChain is rules-only: the LLM provider(s) are deliberately
// not decided yet (AGENTS.md, ARCHITECTURE.md section 5), so the default
// config runs the classifier at zero cost with no real API calls.
const defaultProviderChain = "rules"

// defaultLLMTimeout documents intent for the Phase 3 provider calls this
// service does not make yet (SPEC.md section 8).
const defaultLLMTimeout = 2 * time.Second

// serviceConfig adds this service's own settings to the shared ones.
type serviceConfig struct {
	config.Common
	ProviderChain []string
	LLMTimeout    time.Duration
}

func loadConfig() (serviceConfig, error) {
	l := config.NewLoader()
	cfg := serviceConfig{
		Common:        config.LoadCommon(l, serviceName),
		ProviderChain: splitCSV(l.StrDefault("LLM_PROVIDER_CHAIN", defaultProviderChain)),
		LLMTimeout:    l.Duration("LLM_TIMEOUT", defaultLLMTimeout),
	}
	return cfg, l.Err()
}

// splitCSV parses a comma-separated list, trimming whitespace and dropping
// empty entries. Local to this service so LLM_PROVIDER_CHAIN can have a
// default (config.Loader.CSV is required-only); see loadConfig.
func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
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

// slogFallback is used only if config loading itself fails, before the real
// logger (which needs the validated log level) can be built.
func slogFallback() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", serviceName)
}

func run(ctx context.Context, cfg serviceConfig, log *slog.Logger) error {
	rulesProvider := rules.New(log)
	registry := map[string]provider.Provider{
		provider.RulesName: rulesProvider,
	}
	chain, err := provider.NewChain(cfg.ProviderChain, registry, log)
	if err != nil {
		return fmt.Errorf("build provider chain: %w", err)
	}

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.GRPCPort))
	if err != nil {
		return fmt.Errorf("listen on grpc port %d: %w", cfg.GRPCPort, err)
	}

	grpcServer := grpc.NewServer(grpc.ChainUnaryInterceptor(
		interceptors.UnaryServerRecovery(),
		interceptors.UnaryServerRequireDeadline(),
	))
	classifierv1.RegisterClassifierServiceServer(grpcServer, server.New(chain, log))

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
