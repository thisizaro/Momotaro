// Command loadgen posts live production-shaped traffic at the API Gateway's
// public webhook route, at a steady, chosen rate.
//
// This is Phase 6's `scripts/loadgen` (docs/ARCHITECTURE.md section 14,
// docs/PLAN.md Phase 6, docs/PHASE5_IMPLEMENTATION.md's note distinguishing
// it from scripts/batchgen), built here for Unit AJ
// (docs/DEMO_READINESS.md): the Live Event Stream panel has nothing to show
// without continuous traffic, and the production entry point
// (POST /v1/webhooks/payment-failed) already works end to end but has
// nothing sending to it outside a judge's own curl command.
//
// It talks only to the public HTTP API, deliberately: no direct database
// write, no Kafka producer, no gRPC client. That is not a shortcut, it is
// the point (docs/DEMO_READINESS.md Unit AJ) -- it proves the production
// path works with no new backend surface and no new permissions, the same
// way a real webhook sender would reach this system. Event variety is drawn
// from internal/platform/syntheticgen, the same generator scripts/batchgen
// and the World Simulator use, so live traffic looks like the same
// vocabulary rather than a second, invented one. It never carries
// GROUND_TRUTH: the webhook route has no field for it, by design, only
// scripts/batchgen, writing straight to Postgres, may ever seed it
// (docs/ARCHITECTURE.md section 6). See docs/DECISIONS.md for why this
// extends the empty scripts/loadgen slot rather than becoming a second,
// differently-named tool.
//
// Usage:
//
//	go run ./scripts/loadgen -rate 5 -duration 5m
//	go run ./scripts/loadgen -rate 2 -count 200 -gateway-url http://localhost:8090
//	make loadgen RATE=5 DURATION=5m
//
// The API key is never read from a flag default, never logged, and never
// appears in the exit summary: pass -api-key or set $API_KEY (see
// .env.example), the same convention scripts/batchgen and the rest of the
// stack already use for secrets.
//
// The same is true of the webhook secret (docs/PHASE5_5_IMPLEMENTATION.md
// Unit Z): pass -webhook-secret or set $WEBHOOK_SECRET. Once the Gateway
// has WEBHOOK_SECRET configured it rejects an unsigned webhook, so this is
// required in practice, not optional hardening; the alternative -- the
// Gateway special-casing loadgen traffic to skip verification -- is
// exactly the bypass docs/PHASE5_5_IMPLEMENTATION.md Unit Z rules out.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/clock"
)

// webhookPath is POST /v1/webhooks/payment-failed's path on the Gateway
// (docs/API_GATEWAY.md), appended to -gateway-url rather than made part of
// the URL flag itself, so -gateway-url stays "where the Gateway is" the
// same way every other script and the dashboard's VITE_API_BASE_URL already
// mean it.
const webhookPath = "/v1/webhooks/payment-failed"

func main() {
	gatewayURL := flag.String("gateway-url", "http://localhost:8090", "API Gateway base URL (webhook path is appended)")
	apiKey := flag.String("api-key", os.Getenv("API_KEY"), "X-API-Key value; defaults to $API_KEY, never logged or printed")
	webhookSecret := flag.String("webhook-secret", os.Getenv("WEBHOOK_SECRET"), "webhook secret used to sign X-Razorpay-Signature; defaults to $WEBHOOK_SECRET, never logged or printed (docs/PHASE5_5_IMPLEMENTATION.md Unit Z)")
	rate := flag.Float64("rate", 5, "events per second, steady (not bursty)")
	count := flag.Int("count", 0, "total events to send; mutually exclusive with -duration")
	duration := flag.Duration("duration", 0, "how long to run, e.g. 5m; mutually exclusive with -count")
	seed := flag.Int64("seed", time.Now().UnixNano(), "random seed for event variety; fixed value gives a reproducible traffic shape (never ground truth, see package docs)")
	requestTimeout := flag.Duration("request-timeout", 5*time.Second, "per-request HTTP timeout")
	flag.Parse()

	if *rate <= 0 {
		fmt.Fprintf(os.Stderr, "loadgen: -rate must be positive, got %v\n", *rate)
		os.Exit(2)
	}
	if (*count <= 0) == (*duration <= 0) {
		fmt.Fprintln(os.Stderr, "loadgen: exactly one of -count or -duration must be set")
		os.Exit(2)
	}
	if *apiKey == "" {
		fmt.Fprintln(os.Stderr, "loadgen: no API key: pass -api-key or set $API_KEY (see .env.example)")
		os.Exit(2)
	}
	if *webhookSecret == "" {
		fmt.Fprintln(os.Stderr, "loadgen: no webhook secret: pass -webhook-secret or set $WEBHOOK_SECRET (see .env.example); the Gateway rejects an unsigned webhook (docs/PHASE5_5_IMPLEMENTATION.md Unit Z)")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	cfg := runConfig{
		url:           *gatewayURL + webhookPath,
		apiKey:        *apiKey,
		webhookSecret: *webhookSecret,
		rate:          *rate,
		count:         *count,
		duration:      *duration,
		seed:          *seed,
		httpClient:    &http.Client{Timeout: *requestTimeout},
		clk:           clock.New(),
		logger:        logger,
	}

	summary, err := run(ctx, cfg)

	if summary != nil {
		// The one line this tool always prints on exit, success or not:
		// sent/accepted/failed, and nothing else -- see Summary.String's
		// own doc comment for why the API key can never end up in it.
		fmt.Printf("loadgen summary: %s\n", summary.String())
	}

	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "loadgen: %v\n", err)
		os.Exit(1)
	}

	// A run that sent events but the Gateway accepted none of them is a
	// real failure a script or CI should be able to detect from the exit
	// code, not just from parsing the summary line.
	if summary != nil && summary.Sent > 0 && summary.Accepted == 0 {
		os.Exit(1)
	}
}
