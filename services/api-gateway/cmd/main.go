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
	"github.com/thisizaro/Momotaro/internal/platform/logger"
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
	IngestionAddr string
	APIKey        string
	HTTPPort      int
	CallTimeout   time.Duration
}

func loadConfig() (serviceConfig, error) {
	l := config.NewLoader()
	cfg := serviceConfig{
		Common:        config.LoadCommon(l, serviceName),
		IngestionAddr: l.Str("INGESTION_ADDR"),
		APIKey:        l.Str("API_KEY"),
		HTTPPort:      l.Port("HTTP_PORT", 8090),
		CallTimeout:   l.Duration("CALL_TIMEOUT", 5*time.Second),
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
	ingestionConn, err := grpc.NewClient(cfg.IngestionAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dial ingestion at %s: %w", cfg.IngestionAddr, err)
	}
	defer ingestionConn.Close()

	handler := httpapi.New(ingestionv1.NewIngestionServiceClient(ingestionConn), cfg.APIKey, cfg.CallTimeout)

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

	return shutdown.Close(10*time.Second, func(ctx context.Context) error {
		return httpServer.Shutdown(ctx)
	})
}
