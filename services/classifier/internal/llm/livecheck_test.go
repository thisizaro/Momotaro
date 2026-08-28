//go:build manual

// Live provider check. NOT part of any automated tier.
//
// The `manual` build tag is not in CI's untagged run, nor in the
// `integration e2e` run, so this never executes without someone asking. It
// needs real API keys and real network, which docs/PHASE3_IMPLEMENTATION.md
// Flaw 7 rules out for every automated test in this repo.
//
// It exists for two jobs that a fake server cannot do:
//
//  1. Confirm the request shapes in groq.go and gemini.go match what the
//     vendors actually accept. An httptest server will happily accept a
//     malformed request, so green unit tests prove the client works against
//     the shape we *believe* in, not the one that exists.
//  2. Measure real round-trip latency, which is what LLM_TIMEOUT has to be set
//     from. The value in .env.example is a placeholder and was known to be
//     wrong (docs/DECISIONS.md 2026-08-28).
//
// Run it, from the repo root, with the keys loaded:
//
//	set -a && . ./.env && set +a && go test -tags manual -run TestLive -v ./services/classifier/internal/llm/
package llm

import (
	"context"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/logger"
	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// liveSamples is kept small on purpose: the free tiers are 30 RPM (Groq) and
// 10 to 15 RPM (Gemini), and burning quota to produce a prettier percentile is
// a poor trade when the number is only used to pick a timeout.
const liveSamples = 8

func liveRecords() []*commonv1.Record {
	return []*commonv1.Record{
		{Id: "live-1", Type: commonv1.RecordType_RECORD_TYPE_PAYMENT, AmountPaise: 499900, Currency: "INR", FailureCode: "BANK_TIMEOUT", InstrumentRef: "card_live_1"},
		{Id: "live-2", Type: commonv1.RecordType_RECORD_TYPE_PAYMENT, AmountPaise: 1250000, Currency: "INR", FailureCode: "INSUFFICIENT_FUNDS", InstrumentRef: "upi_live_2"},
		{Id: "live-3", Type: commonv1.RecordType_RECORD_TYPE_PAYMENT, AmountPaise: 89900, Currency: "INR", FailureCode: "EXPIRED_INSTRUMENT", InstrumentRef: "card_live_3"},
		{Id: "live-4", Type: commonv1.RecordType_RECORD_TYPE_PAYMENT, AmountPaise: 2500000, Currency: "INR", FailureCode: "SUSPECTED_FRAUD", InstrumentRef: "card_live_4"},
		// Deliberately unrecognisable: the honest answer is UNSPECIFIED plus
		// ESCALATE, and a model that invents a confident diagnosis here is one
		// to distrust everywhere else.
		{Id: "live-5", Type: commonv1.RecordType_RECORD_TYPE_PAYMENT, AmountPaise: 100000, Currency: "INR", FailureCode: "ERR_7734_XQ", InstrumentRef: "card_live_5"},
	}
}

func TestLiveGroq(t *testing.T) {
	key := os.Getenv("GROQ_API_KEY")
	if key == "" {
		t.Skip("GROQ_API_KEY not set")
	}
	p, err := NewGroq(Config{
		APIKey:  key,
		BaseURL: envOr("GROQ_BASE_URL", DefaultGroqBaseURL),
		Model:   envOr("GROQ_MODEL", DefaultGroqModel),
	}, logger.Discard())
	if err != nil {
		t.Fatalf("NewGroq: %v", err)
	}
	runLive(t, p)
}

func TestLiveGemini(t *testing.T) {
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		t.Skip("GEMINI_API_KEY not set")
	}
	p, err := NewGemini(Config{
		APIKey:  key,
		BaseURL: envOr("GEMINI_BASE_URL", DefaultGeminiBaseURL),
		Model:   envOr("GEMINI_MODEL", DefaultGeminiModel),
	}, logger.Discard())
	if err != nil {
		t.Fatalf("NewGemini: %v", err)
	}
	runLive(t, p)
}

func runLive(t *testing.T, p *Provider) {
	t.Helper()
	records := liveRecords()
	latencies := make([]time.Duration, 0, liveSamples)

	for i := 0; i < liveSamples; i++ {
		rec := records[i%len(records)]
		req := &classifierv1.ClassifyRequest{Record: rec}

		// Generous on purpose: this measures, it does not enforce. Using the
		// timeout under evaluation would make the measurement circular.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		start := time.Now()
		resp, err := p.Classify(ctx, req)
		elapsed := time.Since(start)
		cancel()

		if err != nil {
			t.Errorf("[%s] %s (%s) failed after %s: %v", p.Name(), rec.GetId(), rec.GetFailureCode(), elapsed, err)
			continue
		}
		latencies = append(latencies, elapsed)
		t.Logf("[%s] %-20s %-22s %-28s conf=%.2f  %s\n      rationale: %s",
			p.Name(), rec.GetFailureCode(), resp.GetBucket().String(),
			resp.GetRecommendedAction().String(), resp.GetConfidence(), elapsed, resp.GetRationale())
	}

	if len(latencies) == 0 {
		t.Fatalf("[%s] every live call failed", p.Name())
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	t.Logf("[%s] LATENCY over %d calls: min=%s p50=%s max=%s   <-- LLM_TIMEOUT must clear max with headroom",
		p.Name(), len(latencies),
		latencies[0], latencies[len(latencies)/2], latencies[len(latencies)-1])
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
