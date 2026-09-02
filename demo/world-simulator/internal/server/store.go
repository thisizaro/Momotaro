package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// store is World Simulator's access point to Postgres: a read-only join of
// RECORD (for the original failure_code, reused on a failed retry as "the
// same underlying reason struck again" rather than inventing a new
// per-attempt code model GROUND_TRUTH does not carry) and GROUND_TRUTH
// itself for loadRecordProfile, plus, since Phase 5.5 Unit W, the write
// side of BATCH/RECORD/GROUND_TRUTH for SeedBatch below. This is one of
// exactly two services ever permitted to touch GROUND_TRUTH at all
// (proto/worldsim/v1/worldsim.proto package comment,
// docs/ARCHITECTURE.md section 6: "only World Simulator and the
// Reporting Service's accuracy scorer"), and the only one ever permitted
// to write it. scripts/batchgen writes the exact same three tables the
// exact same way; SeedBatch exists so the API Gateway's demo control
// surface can trigger that without ever gaining a database handle itself
// (docs/PHASE5_5_IMPLEMENTATION.md Unit W).
type store struct {
	pool *pgxpkg.Pool
}

func newStore(pool *pgxpkg.Pool) *store {
	return &store{pool: pool}
}

// errNoGroundTruth means recordID has no GROUND_TRUTH row: either the
// record does not exist, or it exists but was never seeded with a hidden
// profile (real, non-synthetic traffic). Either way World Simulator, which
// is DEMO ONLY and exists solely to answer against that sealed profile, has
// nothing to roll and must not guess a probability.
var errNoGroundTruth = errors.New("no ground truth for record")

type recordProfile struct {
	FailureCode string
	Profile     groundTruthProfile
	// RollKey is what SimulateOutcome now keys its roll off (see rand.go's
	// seededRand), instead of the record's own id. Set at generation time
	// from (seed, ordinal index within the batch) by SeedBatch and
	// scripts/batchgen; empty for any row written before migration 00007,
	// which the caller falls back to record_id for.
	RollKey string
}

func (s *store) loadRecordProfile(ctx context.Context, recordID string) (recordProfile, error) {
	var (
		rp         recordProfile
		trueBucket string
	)
	err := s.pool.QueryRow(ctx, `
		SELECT r.failure_code, gt.true_bucket, gt.recovery_probability, gt.wrong_action_probability, gt.response_delay_seconds, gt.roll_key
		FROM record r
		JOIN ground_truth gt ON gt.record_id = r.id
		WHERE r.id = $1`, recordID,
	).Scan(&rp.FailureCode, &trueBucket, &rp.Profile.RecoveryProbability, &rp.Profile.WrongActionProbability, &rp.Profile.ResponseDelaySeconds, &rp.RollKey)
	if err != nil {
		if errors.Is(err, pgxpkg.ErrNoRows) {
			return recordProfile{}, fmt.Errorf("%w: %s", errNoGroundTruth, recordID)
		}
		return recordProfile{}, fmt.Errorf("load ground truth for %s: %w", recordID, err)
	}
	rp.Profile.TrueBucket = commonv1.RootCauseBucket(commonv1.RootCauseBucket_value[trueBucket])
	return rp, nil
}

// insertBatch creates a BATCH row and returns its id to the caller (SeedBatch
// generates the id itself, same as scripts/batchgen).
func (s *store) insertBatch(ctx context.Context, batchID, source string, totalRecords int32) error {
	if _, err := s.pool.Exec(ctx, `INSERT INTO batch (id, source, total_records) VALUES ($1, $2, $3)`,
		batchID, source, totalRecords,
	); err != nil {
		return fmt.Errorf("insert batch %s: %w", batchID, err)
	}
	return nil
}

// seedRecord is one generated record's full write payload: the RECORD row
// and its GROUND_TRUTH row, inserted together by insertSeedRecord.
type seedRecord struct {
	RecordID      string
	BatchID       string
	Type          string
	AmountPaise   int64
	FailureCode   string
	InstrumentRef string
	CreatedAt     time.Time

	TrueBucket             string
	RecoveryProbability    float64
	WrongActionProbability float64
	ResponseDelaySeconds   int32
	// RollKey is the value SimulateOutcome's roll derives from instead of
	// RecordID (docs/DEMO_READINESS.md Unit AD). SeedBatch sets this to the
	// record's ordinal index within the batch, formatted as decimal
	// digits: reproducible across two batches seeded with the same seed,
	// unlike RecordID, which must stay a fresh uuid on every run so two
	// same-seed batches never collide on this table's primary key.
	RollKey string
}

// insertSeedRecord writes r's RECORD row and its GROUND_TRUTH row, mirroring
// scripts/batchgen/main.go's own two inserts exactly (same columns, same
// literal 'INR' currency) so a scenario-seeded batch is indistinguishable
// on disk from one seeded by the CLI.
func (s *store) insertSeedRecord(ctx context.Context, r seedRecord) error {
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO record (id, batch_id, type, amount_paise, currency, failure_code, instrument_ref, created_at)
		VALUES ($1, $2, $3, $4, 'INR', $5, $6, $7)`,
		r.RecordID, r.BatchID, r.Type, r.AmountPaise, r.FailureCode, r.InstrumentRef, r.CreatedAt,
	); err != nil {
		return fmt.Errorf("insert record %s: %w", r.RecordID, err)
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO ground_truth (record_id, true_bucket, recovery_probability, wrong_action_probability, response_delay_seconds, roll_key)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		r.RecordID, r.TrueBucket, r.RecoveryProbability, r.WrongActionProbability, r.ResponseDelaySeconds, r.RollKey,
	); err != nil {
		return fmt.Errorf("insert ground_truth %s: %w", r.RecordID, err)
	}
	return nil
}
