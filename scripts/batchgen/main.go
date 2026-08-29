// Command batchgen seeds a synthetic batch of revenue-at-risk records,
// straight into Postgres (BATCH, RECORD, GROUND_TRUTH), and publishes each
// one to raw.events so the real pipeline picks it up exactly as if
// Ingestion had received it (docs/PHASE5_IMPLEMENTATION.md Unit B).
//
// Deliberately not a call to Ingestion's own SubmitBatch API: only this
// tool may ever write GROUND_TRUTH (docs/ARCHITECTURE.md section 6, "never
// by the Classifier or Decision Engine"), and Ingestion's proto has no
// field for it at all, by design, so there is no honest way to seed the
// hidden answer key through the public API.
//
// Distinct from scripts/loadgen (docs/PLAN.md Phase 6): that tool submits
// through the real HTTP API for throughput testing and can never carry
// ground truth: this one writes directly to Postgres because only it can.
//
// Usage:
//
//	go run ./scripts/batchgen -count 100
//	go run ./scripts/batchgen -count 500 -seed 42   # reproducible batch
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/thisizaro/Momotaro/internal/platform/kafkax"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
)

func main() {
	dsn := flag.String("dsn", os.Getenv("POSTGRES_DSN"), "Postgres DSN (defaults to $POSTGRES_DSN)")
	brokers := flag.String("brokers", os.Getenv("KAFKA_BROKERS"), "comma-separated Kafka brokers (defaults to $KAFKA_BROKERS)")
	topic := flag.String("topic", "raw.events", "raw.events topic name (must match decision-engine's RAW_EVENTS_TOPIC)")
	count := flag.Int("count", 100, "number of records to generate")
	source := flag.String("source", "synthetic-demo", "batch.source value")
	seed := flag.Int64("seed", time.Now().UnixNano(), "random seed; pass a fixed value for a reproducible batch")
	flag.Parse()

	if *dsn == "" {
		log.Fatal("no DSN: pass -dsn or set POSTGRES_DSN (see .env.example)")
	}
	if *brokers == "" {
		log.Fatal("no brokers: pass -brokers or set KAFKA_BROKERS (see .env.example)")
	}
	if *count <= 0 {
		log.Fatalf("-count must be positive, got %d", *count)
	}

	ctx := context.Background()

	connectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	pool, err := pgxpkg.NewPool(connectCtx, *dsn)
	cancel()
	if err != nil {
		log.Fatalf("connect to postgres: %v", err)
	}
	defer pool.Close()

	producer, err := kafkax.NewProducer(strings.Split(*brokers, ","))
	if err != nil {
		log.Fatalf("connect to kafka: %v", err)
	}
	defer producer.Close()

	rng := rand.New(rand.NewSource(*seed))

	batchID := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO batch (id, source, total_records) VALUES ($1, $2, $3)`,
		batchID, *source, *count,
	); err != nil {
		log.Fatalf("create batch: %v", err)
	}

	instrumentRefs := instrumentRefPool(*count)

	for i := 0; i < *count; i++ {
		rec := generateRecord(rng)
		instrumentRef := pickInstrumentRef(rng, rec.Type, instrumentRefs)
		recordID := uuid.NewString()
		createdAt := time.Now()

		if _, err := pool.Exec(ctx, `
			INSERT INTO record (id, batch_id, type, amount_paise, currency, failure_code, instrument_ref, created_at)
			VALUES ($1, $2, $3, $4, 'INR', $5, $6, $7)`,
			recordID, batchID, rec.Type.String(), rec.AmountPaise, rec.FailureCode, instrumentRef, createdAt,
		); err != nil {
			log.Fatalf("insert record %d: %v", i, err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO ground_truth (record_id, true_bucket, recovery_probability, wrong_action_probability, response_delay_seconds)
			VALUES ($1, $2, $3, $4, $5)`,
			recordID, rec.TrueBucket.String(), rec.RecoveryProbability, rec.WrongActionProbability, rec.ResponseDelaySeconds,
		); err != nil {
			log.Fatalf("insert ground_truth %d: %v", i, err)
		}

		payload, err := json.Marshal(rawEvent{
			RecordID:      recordID,
			BatchID:       batchID,
			Type:          rec.Type.String(),
			AmountPaise:   rec.AmountPaise,
			Currency:      "INR",
			FailureCode:   rec.FailureCode,
			InstrumentRef: instrumentRef,
			CreatedAt:     createdAt,
		})
		if err != nil {
			log.Fatalf("marshal raw event %d: %v", i, err)
		}
		if err := producer.Publish(ctx, *topic, recordID, payload); err != nil {
			log.Fatalf("publish raw event %d: %v", i, err)
		}
	}

	fmt.Printf("batch %s: %d records generated, seed=%d\n", batchID, *count, *seed)
}

// rawEvent mirrors services/ingestion/internal/server.RawEvent exactly:
// the raw.events wire payload has no proto (docs/ARCHITECTURE.md section 9
// applies to gRPC contracts, not this internal topic), so every producer
// keeps its own copy in sync by hand.
type rawEvent struct {
	RecordID      string    `json:"record_id"`
	BatchID       string    `json:"batch_id"`
	Type          string    `json:"type"`
	AmountPaise   int64     `json:"amount_paise"`
	Currency      string    `json:"currency"`
	FailureCode   string    `json:"failure_code"`
	InstrumentRef string    `json:"instrument_ref"`
	CreatedAt     time.Time `json:"created_at"`
}
