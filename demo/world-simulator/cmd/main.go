// Command world-simulator is the entrypoint for the world-simulator service.
//
// Stands in for the bank and the customer. Holds the sealed ground truth.
//
// See docs/ARCHITECTURE.md section 6 for the design, and
// docs/ENGINEERING.md for the rules this service must follow (fail-fast
// config, graceful shutdown, injected clock, context deadlines). DEMO
// ONLY: a real deployment deletes this whole component (section 3b).
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

	"github.com/redis/go-redis/v9"
	"github.com/thisizaro/Momotaro/demo/world-simulator/internal/server"
	"github.com/thisizaro/Momotaro/internal/platform/clock"
	"github.com/thisizaro/Momotaro/internal/platform/config"
	"github.com/thisizaro/Momotaro/internal/platform/interceptors"
	"github.com/thisizaro/Momotaro/internal/platform/kafkax"
	"github.com/thisizaro/Momotaro/internal/platform/logger"
	"github.com/thisizaro/Momotaro/internal/platform/metrics"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	"github.com/thisizaro/Momotaro/internal/platform/shutdown"
	decisionenginev1 "github.com/thisizaro/Momotaro/proto/gen/decisionengine/v1"
	worldsimv1 "github.com/thisizaro/Momotaro/proto/gen/worldsim/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const serviceName = "world-simulator"

// defaultRawEventsTopic mirrors services/ingestion/cmd/main.go's own
// default: SeedBatch and InjectPoison (Phase 5.5 Unit W) publish onto the
// same topic Ingestion does, so the Decision Engine cannot tell a
// demo-seeded record from a normally-ingested one.
const defaultRawEventsTopic = "raw.events"

// serviceConfig adds this service's own settings to the shared ones.
type serviceConfig struct {
	config.Common
	DecisionEngineAddr string
	CallTimeout        time.Duration
	// PollInterval is how often the delayed-outcome queue is drained.
	// Deliberately NOT scaled by DemoTimeScale (docs/ARCHITECTURE.md
	// section 6 says the tick interval is "computed from" the scale
	// factor, but scaling an already-small interval down further would
	// make polling impractically frequent; instead only resolves_at
	// itself is scaled, and a small fixed interval is already fast enough
	// to catch a compressed delay promptly, mirroring
	// services/decision-engine's own SchedulerConfig.PollInterval, which
	// is likewise unscaled).
	PollInterval time.Duration
	// RawEventsTopic backs SeedBatch and InjectPoison (Phase 5.5 Unit W):
	// both publish onto raw.events exactly the way Ingestion does.
	// Overridable for the same reason services/ingestion/cmd/main.go's own
	// RAW_EVENTS_TOPIC is: an isolated scratch topic in a test.
	RawEventsTopic string
}

func loadConfig() (serviceConfig, error) {
	l := config.NewLoader()
	cfg := serviceConfig{
		Common:             config.LoadCommon(l, serviceName),
		DecisionEngineAddr: l.Str("DECISION_ENGINE_ADDR"),
		CallTimeout:        l.Duration("CALL_TIMEOUT", 5*time.Second),
		PollInterval:       l.Duration("WORLDSIM_POLL_INTERVAL", time.Second),
		RawEventsTopic:     l.StrDefault("RAW_EVENTS_TOPIC", defaultRawEventsTopic),
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

	redisClient := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	defer redisClient.Close()
	pingCtx, pingCancel := context.WithTimeout(ctx, 5*time.Second)
	if err := redisClient.Ping(pingCtx).Err(); err != nil {
		pingCancel()
		return fmt.Errorf("connect to redis at %s: %w", cfg.RedisAddr, err)
	}
	pingCancel()

	decisionEngineConn, err := grpc.NewClient(cfg.DecisionEngineAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(interceptors.UnaryClientDefaultDeadline(cfg.CallTimeout)))
	if err != nil {
		return fmt.Errorf("dial decision engine at %s: %w", cfg.DecisionEngineAddr, err)
	}
	defer decisionEngineConn.Close()
	decisionEngineClient := decisionenginev1.NewDecisionEngineServiceClient(decisionEngineConn)

	// Backs SeedBatch and InjectPoison (Phase 5.5 Unit W): both publish onto
	// raw.events exactly the way Ingestion does.
	rawEventsProducer, err := kafkax.NewProducer(cfg.KafkaBrokers)
	if err != nil {
		return fmt.Errorf("connect to kafka: %w", err)
	}
	defer rawEventsProducer.Close()

	clk := clock.New()
	worldSimServer := server.New(pool, redisClient, clk, cfg.Common.Scale, rawEventsProducer, cfg.RawEventsTopic)
	poller := server.NewPoller(redisClient, decisionEngineClient, clk, cfg.PollInterval, cfg.CallTimeout, log)

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
	worldsimv1.RegisterWorldSimulatorServiceServer(grpcServer, worldSimServer)

	grpcServeErr := make(chan error, 1)
	go func() {
		log.Info("grpc server listening", "port", cfg.GRPCPort)
		grpcServeErr <- grpcServer.Serve(lis)
	}()

	// A separate cancel from the signal-driven ctx: if either the gRPC
	// server or the poller exits on its own, the other must stop too,
	// not run on unsupervised until a signal eventually arrives
	// (mirrors services/decision-engine/cmd/main.go).
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	pollerErr := make(chan error, 1)
	go func() {
		pollerErr <- poller.Run(runCtx)
	}()

	log.Info("running", "poll_interval", cfg.PollInterval)

	var runErr error
	select {
	case err := <-grpcServeErr:
		cancelRun()
		<-pollerErr
		if err != nil {
			runErr = fmt.Errorf("grpc server: %w", err)
		}
	case err := <-pollerErr:
		cancelRun()
		if err != nil && !errors.Is(err, context.Canceled) {
			runErr = fmt.Errorf("poller: %w", err)
		}
	case <-ctx.Done():
		<-pollerErr
	}

	if shutErr := shutdown.Close(10*time.Second,
		func(ctx context.Context) error { grpcServer.GracefulStop(); return nil },
		func(ctx context.Context) error { return metricsServer.Shutdown(ctx) },
	); shutErr != nil {
		return errors.Join(runErr, shutErr)
	}
	return runErr
}
