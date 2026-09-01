// Package server's delayed-outcome queue: docs/ARCHITECTURE.md section 6's
// "classic lightweight delayed-job queue pattern" over a single Redis
// sorted set, so a nudge's eventual answer can be scheduled now and
// delivered later without holding a gRPC call open for hours.
package server

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// delayedOutcomesKey is the sorted set docs/ARCHITECTURE.md section 6
// names verbatim: score is resolves_at as a Unix timestamp, member
// identifies which answer to deliver.
const delayedOutcomesKey = "wsim:delayed_outcomes"

// delayedOutcome is one scheduled answer.
type delayedOutcome struct {
	RecordID      string
	AttemptNumber int32
	Outcome       commonv1.Outcome
	// FailureCode is set only when Outcome is FAILURE.
	FailureCode string
}

// member encodes d as "record_id:attempt_number:outcome:failure_code".
// docs/ARCHITECTURE.md section 6 gives the first three fields verbatim
// as the example format; failure_code is added because
// ReportDelayedOutcomeRequest accepts one and services/decision-engine/
// internal/engine/scheduler.go's ResumeNudge already documents it as
// informational (logged, never decision-driving), so carrying it through
// costs nothing and keeps a failed nudge's audit trail as informative as
// a failed retry's. Safe to split on ':': a UUID, an Outcome enum name and
// a Razorpay failure code never contain one.
func (d delayedOutcome) member() string {
	return strings.Join([]string{d.RecordID, strconv.Itoa(int(d.AttemptNumber)), d.Outcome.String(), d.FailureCode}, ":")
}

func parseMember(s string) (delayedOutcome, error) {
	parts := strings.SplitN(s, ":", 4)
	if len(parts) < 3 {
		return delayedOutcome{}, fmt.Errorf("malformed delayed-outcome member %q", s)
	}
	attemptNumber, err := strconv.Atoi(parts[1])
	if err != nil {
		return delayedOutcome{}, fmt.Errorf("malformed attempt number in member %q: %w", s, err)
	}
	d := delayedOutcome{
		RecordID:      parts[0],
		AttemptNumber: int32(attemptNumber),
		Outcome:       commonv1.Outcome(commonv1.Outcome_value[parts[2]]),
	}
	if len(parts) == 4 {
		d.FailureCode = parts[3]
	}
	return d, nil
}

type queue struct {
	client *redis.Client
}

// pendingEntry is one queue member with its due time attached, the shape
// GetWorldState (Phase 5.5 Unit W, docs/API_GATEWAY.md GET /v1/demo/world)
// returns.
type pendingEntry struct {
	delayedOutcome
	DueAt time.Time
}

func newQueue(client *redis.Client) *queue {
	return &queue{client: client}
}

// schedule adds d to the delayed-outcome set, to be delivered once due
// reaches resolvesAt.
func (q *queue) schedule(ctx context.Context, d delayedOutcome, resolvesAt time.Time) error {
	err := q.client.ZAdd(ctx, delayedOutcomesKey, redis.Z{
		Score:  float64(resolvesAt.Unix()),
		Member: d.member(),
	}).Err()
	if err != nil {
		return fmt.Errorf("schedule delayed outcome for %s: %w", d.RecordID, err)
	}
	return nil
}

// due returns every entry scheduled at or before now, removing each from
// the set as it is read. malformed carries any member this service wrote
// to itself but cannot parse back (should not happen; see member/
// parseMember), removed the same as a well-formed one rather than wedging
// every future poll behind the same bad entry -- the caller logs these,
// due does not, since it has no logger of its own
// (docs/ENGINEERING.md section 14).
//
// Not perfectly atomic (ZRANGEBYSCORE then one ZREM per member, not a
// single Lua script): World Simulator runs its poller as one goroutine in
// one instance, so there is no concurrent poller to race against, and
// ReportDelayedOutcome is already documented as at-least-once and
// idempotent-safe downstream (a duplicate is discarded, not an error).
// Revisit if this service is ever run with more than one replica.
func (q *queue) due(ctx context.Context, now time.Time) (delivered []delayedOutcome, malformed []string, err error) {
	members, err := q.client.ZRangeByScore(ctx, delayedOutcomesKey, &redis.ZRangeBy{
		Min: "-inf",
		Max: strconv.FormatInt(now.Unix(), 10),
	}).Result()
	if err != nil {
		return nil, nil, fmt.Errorf("scan due delayed outcomes: %w", err)
	}

	for _, m := range members {
		if d, perr := parseMember(m); perr != nil {
			malformed = append(malformed, m)
		} else {
			delivered = append(delivered, d)
		}
		if remErr := q.client.ZRem(ctx, delayedOutcomesKey, m).Err(); remErr != nil {
			return delivered, malformed, fmt.Errorf("remove delayed outcome %q: %w", m, remErr)
		}
	}
	return delivered, malformed, nil
}

// peekAll returns every entry currently queued, with its due time, without
// removing anything. Unlike due, this backs a read: GetWorldState
// (docs/API_GATEWAY.md GET /v1/demo/world) must not deliver a pending
// outcome early merely because someone looked at the dashboard. A malformed
// member (should not happen; see member/parseMember) is skipped rather than
// failing the whole read, same tolerance as due -- the caller has no logger
// here either (docs/ENGINEERING.md section 14).
func (q *queue) peekAll(ctx context.Context) ([]pendingEntry, error) {
	zs, err := q.client.ZRangeWithScores(ctx, delayedOutcomesKey, 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("peek delayed outcomes: %w", err)
	}

	entries := make([]pendingEntry, 0, len(zs))
	for _, z := range zs {
		member, ok := z.Member.(string)
		if !ok {
			continue
		}
		d, perr := parseMember(member)
		if perr != nil {
			continue
		}
		entries = append(entries, pendingEntry{delayedOutcome: d, DueAt: time.Unix(int64(z.Score), 0)})
	}
	return entries, nil
}
