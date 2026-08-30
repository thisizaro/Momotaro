package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDefaultMigrationsDirResolvesRegardlessOfWorkingDirectory proves
// defaultMigrationsDir finds the real migrations/ directory using this
// source file's own location, not the caller's working directory -- the
// exact gap that let `go run ./scripts/migrate` fail with "./migrations
// directory does not exist" when invoked from outside the repo root
// (docs/INCIDENTS.md, 2026-08-30).
func TestDefaultMigrationsDirResolvesRegardlessOfWorkingDirectory(t *testing.T) {
	dir := defaultMigrationsDir()

	known := filepath.Join(dir, "00001_initial_schema.sql")
	if _, err := os.Stat(known); err != nil {
		t.Fatalf("defaultMigrationsDir() = %q, does not contain 00001_initial_schema.sql: %v", dir, err)
	}
}
