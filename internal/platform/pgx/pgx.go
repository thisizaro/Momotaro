package pgx

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool is the shared Postgres connection pool type. Aliased so callers never
// need to import pgxpool directly.
type Pool = pgxpool.Pool

// Tx is the transaction handle passed into a WithTx callback. Aliased so
// callers never need to import pgx directly.
type Tx = pgx.Tx

// ErrNoRows is returned by Query/QueryRow when there were no results.
// Aliased so callers never need to import pgx directly just to check it.
var ErrNoRows = pgx.ErrNoRows

// NewPool builds a connection pool from a DSN, per docs/ARCHITECTURE.md
// section 10a: pgx v5 with pgxpool, never database/sql.
func NewPool(ctx context.Context, dsn string) (*Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse postgres dsn: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create postgres pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return pool, nil
}

// WithTx runs fn inside a transaction, committing on success and rolling
// back on any error or panic.
//
// This is THE mechanism behind docs/ARCHITECTURE.md section 10a's rule: a
// state change and its AUDIT_ENTRY row are written in one transaction, by
// the service that owns the change. Either both land or neither does.
func WithTx(ctx context.Context, pool *Pool, fn func(ctx context.Context, tx Tx) error) (err error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
	}()

	if err := fn(ctx, tx); err != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil {
			return fmt.Errorf("tx failed: %w (rollback also failed: %v)", err, rbErr)
		}
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}
