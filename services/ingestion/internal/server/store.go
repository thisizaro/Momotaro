package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
)

// recordStore is Ingestion's only access point to the two tables it owns,
// BATCH and RECORD (docs/ARCHITECTURE.md section 10a). Keeping every SQL
// statement behind this type is what lets server.go read as orchestration
// rather than a mix of gRPC handling and SQL (docs/ENGINEERING.md
// section 14).
type recordStore struct {
	pool               *pgxpkg.Pool
	rollingBatchSource string
}

func newRecordStore(pool *pgxpkg.Pool, rollingBatchSource string) *recordStore {
	return &recordStore{pool: pool, rollingBatchSource: rollingBatchSource}
}

// newRecordParams is everything insertRecord needs for one RECORD row.
type newRecordParams struct {
	ID             string
	BatchID        string
	Type           string
	AmountPaise    int64
	Currency       string
	FailureCode    string
	InstrumentRef  string
	CreatedAt      time.Time
	IdempotencyKey string // "" is stored as NULL, see insertRecord.
}

// createBatch inserts a new BATCH row for one SubmitBatch call and returns
// its id. Every SubmitBatch submission gets its own batch; this is distinct
// from rollingBatchID, which SubmitEvent calls share.
func (s *recordStore) createBatch(ctx context.Context, source string) (string, error) {
	id := uuid.NewString()
	if _, err := s.pool.Exec(ctx, `INSERT INTO batch (id, source, total_records) VALUES ($1, $2, 0)`, id, source); err != nil {
		return "", fmt.Errorf("create batch: %w", err)
	}
	return id, nil
}

// rollingBatchID returns the id of the shared batch that ungrouped
// SubmitEvent calls attach to, creating it on first use. Without this, a
// production webhook record would have no batch to be reported under
// (proto/ingestion/v1/ingestion.proto: "grouped into an implicit rolling
// batch so every record is reportable"). It is one long-lived batch, not
// time-windowed: see docs/DECISIONS.md for why that is enough for now.
//
// Two concurrent first-ever calls can each create one; the SELECT's ORDER BY
// deterministically settles on the older row for everything after, so the
// only cost of that race is a second, mostly-empty batch row, never a lost
// or double-counted record.
func (s *recordStore) rollingBatchID(ctx context.Context) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx,
		`SELECT id FROM batch WHERE source = $1 ORDER BY created_at LIMIT 1`,
		s.rollingBatchSource,
	).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgxpkg.ErrNoRows) {
		return "", fmt.Errorf("look up rolling batch: %w", err)
	}
	return s.createBatch(ctx, s.rollingBatchSource)
}

// insertRecord inserts one RECORD row. An empty IdempotencyKey is stored as
// SQL NULL, not the empty string, so the partial unique index on that column
// never collides two keyless records (docs/DECISIONS.md).
func (s *recordStore) insertRecord(ctx context.Context, p newRecordParams) error {
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO record (id, batch_id, type, amount_paise, currency, failure_code, instrument_ref, created_at, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		p.ID, p.BatchID, p.Type, p.AmountPaise, p.Currency, p.FailureCode, p.InstrumentRef, p.CreatedAt, nullIfEmpty(p.IdempotencyKey),
	); err != nil {
		return fmt.Errorf("insert record %s: %w", p.ID, err)
	}
	return nil
}

// findByIdempotencyKey returns the record_id and batch_id of a previously
// ingested event carrying this key, or found=false if none exists yet.
// Callers must never pass an empty key: SubmitBatch records and keyless
// SubmitEvent calls are never deduplicated against anything, by design.
func (s *recordStore) findByIdempotencyKey(ctx context.Context, key string) (recordID, batchID string, found bool, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT id, batch_id FROM record WHERE idempotency_key = $1`, key,
	).Scan(&recordID, &batchID)
	if err == nil {
		return recordID, batchID, true, nil
	}
	if errors.Is(err, pgxpkg.ErrNoRows) {
		return "", "", false, nil
	}
	return "", "", false, fmt.Errorf("look up idempotency key: %w", err)
}

// setBatchTotal overwrites BATCH.total_records. Used by SubmitBatch, which
// knows the final accepted count once its whole request has been processed.
func (s *recordStore) setBatchTotal(ctx context.Context, batchID string, total int32) error {
	if _, err := s.pool.Exec(ctx, `UPDATE batch SET total_records=$1 WHERE id=$2`, total, batchID); err != nil {
		return fmt.Errorf("update batch total_records: %w", err)
	}
	return nil
}

// incrementBatchTotal adds one to BATCH.total_records. Used by SubmitEvent,
// where records land on a shared rolling batch one gRPC call at a time
// rather than all at once.
func (s *recordStore) incrementBatchTotal(ctx context.Context, batchID string) error {
	if _, err := s.pool.Exec(ctx, `UPDATE batch SET total_records = total_records + 1 WHERE id=$1`, batchID); err != nil {
		return fmt.Errorf("increment batch total_records: %w", err)
	}
	return nil
}

// batchSummary is one row of a ListBatches page.
type batchSummary struct {
	ID           string
	CreatedAt    time.Time
	TotalRecords int32
	Source       string
}

// listBatches returns up to limit BATCH rows, newest first
// (docs/API_GATEWAY.md: "so a 'pick the most recent one' default has
// something real to select"). total_records is read directly off the row
// rather than computed from RECORD, since SubmitBatch/SubmitEvent already
// keep it accurate (setBatchTotal/incrementBatchTotal above), and a second
// aggregate query here would just be a slower way to ask the same table
// something it already knows.
func (s *recordStore) listBatches(ctx context.Context, limit int32) ([]batchSummary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, created_at, total_records, source
		FROM batch
		ORDER BY created_at DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list batches: %w", err)
	}
	defer rows.Close()

	var result []batchSummary
	for rows.Next() {
		var b batchSummary
		if err := rows.Scan(&b.ID, &b.CreatedAt, &b.TotalRecords, &b.Source); err != nil {
			return nil, fmt.Errorf("scan batch row: %w", err)
		}
		result = append(result, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate batch rows: %w", err)
	}
	return result, nil
}

// nullIfEmpty converts "" to SQL NULL so pgx binds it correctly. Any other
// string is passed through unchanged.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
