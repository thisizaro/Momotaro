// Command api-gateway is the entrypoint for the api-gateway service.
//
// HTTP/WS edge. Translates external requests into internal gRPC. The only door in.
//
// See docs/ARCHITECTURE.md for where this sits in the system, and
// docs/ENGINEERING.md for the rules this service must follow (fail-fast
// config, graceful shutdown, injected clock, context deadlines).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/config"
	"github.com/thisizaro/Momotaro/internal/platform/interceptors"
	"github.com/thisizaro/Momotaro/internal/platform/logger"
	"github.com/thisizaro/Momotaro/internal/platform/metrics"
	"github.com/thisizaro/Momotaro/internal/platform/shutdown"
	ingestionv1 "github.com/thisizaro/Momotaro/proto/gen/ingestion/v1"
	"github.com/thisizaro/Momotaro/services/api-gateway/internal/httpapi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const serviceName = "api-gateway"

// serviceConfig adds this service's own settings to the shared ones.
type serviceConfig struct {
	config.Common
	IngestionAddr  string
	APIKey         string
	HTTPPort       int
	CallTimeout    time.Duration
	RateLimitRPS   float64
	RateLimitBurst int
}

func loadConfig() (serviceConfig, error) {
	l := config.NewLoader()
	cfg := serviceConfig{
		Common:        config.LoadCommon(l, serviceName),
		IngestionAddr: l.Str("INGESTION_ADDR"),
		APIKey:        l.Str("API_KEY"),
		HTTPPort:      l.Port("HTTP_PORT", 8090),
		CallTimeout:   l.Duration("CALL_TIMEOUT", 5*time.Second),
		// Basic protection against a runaway caller, not per-tenant fairness:
		// this system has no concept of a "user" to key on (ARCHITECTURE.md
		// section 17). Defaults comfortably clear the 50 records/sec NFR
		// (PRD.md section 10) with headroom for bursts.
		RateLimitRPS:   l.Float("RATE_LIMIT_RPS", 100),
		RateLimitBurst: l.Int("RATE_LIMIT_BURST", 200),
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
	ingestionConn, err := grpc.NewClient(cfg.IngestionAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(interceptors.UnaryClientDefaultDeadline(cfg.CallTimeout)))
	if err != nil {
		return fmt.Errorf("dial ingestion at %s: %w", cfg.IngestionAddr, err)
	}
	defer ingestionConn.Close()

	handler := httpapi.New(ingestionv1.NewIngestionServiceClient(ingestionConn), cfg.APIKey, cfg.CallTimeout, cfg.RateLimitRPS, cfg.RateLimitBurst)

	// api-gateway has no inbound gRPC server to instrument (it is an HTTP
	// edge with only outbound gRPC clients), so this exposes Go/process
	// metrics only; there is no requests_total/request_duration_seconds
	// data here the way there is for a gRPC-serving service.
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

	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler:           handler.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		log.Info("http server listening", "port", cfg.HTTPPort)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("http server: %w", err)
		}
		return nil
	case <-ctx.Done():
	}

	return shutdown.Close(10*time.Second,
		func(ctx context.Context) error {
			return httpServer.Shutdown(ctx)
		},
		func(ctx context.Context) error {
			return metricsServer.Shutdown(ctx)
		},
	)
}
