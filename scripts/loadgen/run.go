package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/clock"
	"github.com/thisizaro/Momotaro/internal/platform/syntheticgen"
)

// instrumentPoolSize is a fixed, generous pool for PickInstrumentRef to draw
// from: this run has no natural batch size to derive it from the way
// scripts/batchgen does (docs/PLAN.md sizes batchgen's own pool at roughly a
// tenth of -count), since a live traffic generator's total event count is
// often unbounded (duration-driven, not count-driven).
const instrumentPoolSize = 200

// gatewayDownThreshold is how many consecutive failed sends trigger the
// explicit "gateway appears unreachable" log line, distinct from the
// per-event warning every failure already gets. Low enough to surface a
// dead gateway quickly rather than well into a long run.
const gatewayDownThreshold = 3

// runConfig is everything one run of the generator needs. clk is injected
// (docs/ENGINEERING.md section 2: pacing is time-based logic and untestable
// against the wall clock); logger is injected so tests can capture and
// assert on it instead of scraping stderr.
type runConfig struct {
	url    string
	apiKey string
	// webhookSecret signs every event's body into X-Razorpay-Signature
	// (docs/PHASE5_5_IMPLEMENTATION.md Unit Z). Required in practice once
	// the Gateway has WEBHOOK_SECRET configured, the same way apiKey is
	// required once it has API_KEY configured; main.go fails fast if it is
	// empty, the same check apiKey already gets.
	webhookSecret string
	rate          float64
	count         int           // total events to send; 0 means duration-bound instead
	duration      time.Duration // how long to run; 0 means count-bound instead
	seed          int64
	httpClient    *http.Client
	clk           clock.Clock
	logger        *slog.Logger
}

// run drives the send loop: paced by a RateLimiter, each event drawn fresh
// from syntheticgen, counted into a Summary, until either -count events
// have been sent, -duration has elapsed, or ctx is cancelled (SIGINT).
// Returns the summary accumulated so far in every case, including
// cancellation, since a partial count is exactly what "graceful shutdown, a
// clear summary line on exit" needs.
func run(ctx context.Context, cfg runConfig) (*Summary, error) {
	if cfg.rate <= 0 {
		return nil, fmt.Errorf("rate must be positive, got %v", cfg.rate)
	}
	if (cfg.count <= 0) == (cfg.duration <= 0) {
		return nil, fmt.Errorf("exactly one of count or duration must be set")
	}
	if cfg.httpClient == nil {
		return nil, fmt.Errorf("httpClient is required")
	}
	if cfg.clk == nil {
		return nil, fmt.Errorf("clk is required")
	}
	if cfg.logger == nil {
		cfg.logger = slog.Default()
	}
	if cfg.url == "" {
		return nil, fmt.Errorf("url is required")
	}

	rng := rand.New(rand.NewSource(cfg.seed))
	pool := syntheticgen.InstrumentRefPool(instrumentPoolSize)

	start := cfg.clk.Now()
	limiter, err := NewRateLimiter(cfg.rate, start, cfg.clk)
	if err != nil {
		return nil, err
	}

	var deadline time.Time
	if cfg.duration > 0 {
		deadline = start.Add(cfg.duration)
	}

	summary := &Summary{}
	consecutiveFailures := 0
	warnedGatewayDown := false

	for n := 0; ; n++ {
		if cfg.count > 0 && n >= cfg.count {
			break
		}
		if !deadline.IsZero() && !cfg.clk.Now().Before(deadline) {
			break
		}

		if err := limiter.Wait(ctx, n); err != nil {
			// SIGINT (a cancelled context) or a deadline on ctx itself:
			// stop cleanly and hand back what was sent so far, rather than
			// treating this as a hard failure.
			return summary, err
		}

		payload, err := buildPayload(rng, pool)
		if err != nil {
			// A generation bug, not a network condition; log and skip
			// rather than abort the whole run over one bad draw.
			cfg.logger.Error("build synthetic event", "error", err)
			continue
		}

		summary.RecordSent()
		result := sendEvent(ctx, cfg.httpClient, cfg.url, cfg.apiKey, cfg.webhookSecret, payload)
		if result.accepted {
			summary.RecordAccepted()
			consecutiveFailures = 0
			warnedGatewayDown = false
			continue
		}

		summary.RecordFailed()
		consecutiveFailures++
		cfg.logger.Warn("event not accepted", "status", result.status, "error", errText(result.err))
		if consecutiveFailures >= gatewayDownThreshold && !warnedGatewayDown {
			cfg.logger.Error("gateway appears unreachable: repeated consecutive failures, check -url and that the Gateway is running",
				"consecutive_failures", consecutiveFailures, "url", cfg.url)
			warnedGatewayDown = true
		}
	}

	return summary, nil
}

// errText turns a possibly-nil error into a string for structured logging.
// Never formats anything but err.Error(): in particular never the API key,
// which sendEvent's own errors never carry in the first place.
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
