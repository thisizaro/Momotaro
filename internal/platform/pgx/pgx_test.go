package pgx

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// dsn returns the local Postgres DSN for tests. These tests hit the real
// docker-compose Postgres per docs/ENGINEERING.md section 1: do not mock
// what you own.
func dsn(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("POSTGRES_DSN"); v != "" {
		return v
	}
	return "postgres://momotaro:momotaro@localhost:5432/momotaro?sslmode=disable"
}

func TestNewPoolConnects(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := NewPool(ctx, dsn(t))
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestNewPoolRejectsBadDSN(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := NewPool(ctx, "not a dsn"); err == nil {
		t.Fatal("expected an error for a malformed DSN")
	}
}

func TestWithTxCommitsOnSuccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := NewPool(ctx, dsn(t))
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	setupScratchTable(ctx, t, pool)

	err = WithTx(ctx, pool, func(ctx context.Context, tx Tx) error {
		_, err := tx.Exec(ctx, "INSERT INTO pgx_scratch (value) VALUES ($1)", "committed")
		return err
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM pgx_scratch WHERE value = $1", "committed").Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 1 {
		t.Errorf("committed row not found, count = %d", count)
	}
}

// This is the rule pgx/doc.go exists to enforce: a state change and its
// audit entry either both land or neither does.
func TestWithTxRollsBackOnError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := NewPool(ctx, dsn(t))
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	setupScratchTable(ctx, t, pool)

	sentinel := errors.New("boom")
	err = WithTx(ctx, pool, func(ctx context.Context, tx Tx) error {
		if _, err := tx.Exec(ctx, "INSERT INTO pgx_scratch (value) VALUES ($1)", "rolled-back"); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("WithTx error = %v, want wrapped %v", err, sentinel)
	}

	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM pgx_scratch WHERE value = $1", "rolled-back").Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Errorf("rolled-back insert is visible, count = %d", count)
	}
}

// A panic inside the callback (e.g. a downstream bug) must not leave the
// transaction or the connection hanging open.
func TestWithTxRollsBackOnPanic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := NewPool(ctx, dsn(t))
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	setupScratchTable(ctx, t, pool)

	func() {
		defer func() { _ = recover() }()
		_ = WithTx(ctx, pool, func(ctx context.Context, tx Tx) error {
			_, _ = tx.Exec(ctx, "INSERT INTO pgx_scratch (value) VALUES ($1)", "panicked")
			panic("simulated downstream bug")
		})
	}()

	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM pgx_scratch WHERE value = $1", "panicked").Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Errorf("panicked insert is visible, count = %d", count)
	}

	// The pool must still be usable after a panic recovered inside WithTx.
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("pool unusable after panic: %v", err)
	}
}

func setupScratchTable(ctx context.Context, t *testing.T, pool *Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, "CREATE TABLE IF NOT EXISTS pgx_scratch (value TEXT NOT NULL)"); err != nil {
		t.Fatalf("create scratch table: %v", err)
	}
	if _, err := pool.Exec(ctx, "TRUNCATE pgx_scratch"); err != nil {
		t.Fatalf("truncate scratch table: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP TABLE IF EXISTS pgx_scratch")
	})
}
