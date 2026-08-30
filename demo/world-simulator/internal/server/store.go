package server

import (
	"context"
	"errors"
	"fmt"

	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// store is World Simulator's only access point to Postgres: a read-only
// join of RECORD (for the original failure_code, reused on a failed retry
// as "the same underlying reason struck again" rather than inventing a new
// per-attempt code model GROUND_TRUTH does not carry) and GROUND_TRUTH
// itself. This is one of exactly two services ever permitted to read
// GROUND_TRUTH (proto/worldsim/v1/worldsim.proto package comment); it
// writes neither table.
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
}

func (s *store) loadRecordProfile(ctx context.Context, recordID string) (recordProfile, error) {
	var (
		rp         recordProfile
		trueBucket string
	)
	err := s.pool.QueryRow(ctx, `
		SELECT r.failure_code, gt.true_bucket, gt.recovery_probability, gt.wrong_action_probability, gt.response_delay_seconds
		FROM record r
		JOIN ground_truth gt ON gt.record_id = r.id
		WHERE r.id = $1`, recordID,
	).Scan(&rp.FailureCode, &trueBucket, &rp.Profile.RecoveryProbability, &rp.Profile.WrongActionProbability, &rp.Profile.ResponseDelaySeconds)
	if err != nil {
		if errors.Is(err, pgxpkg.ErrNoRows) {
			return recordProfile{}, fmt.Errorf("%w: %s", errNoGroundTruth, recordID)
		}
		return recordProfile{}, fmt.Errorf("load ground truth for %s: %w", recordID, err)
	}
	rp.Profile.TrueBucket = commonv1.RootCauseBucket(commonv1.RootCauseBucket_value[trueBucket])
	return rp, nil
}
