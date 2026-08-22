// Command migrate applies the repo's Postgres migrations.
//
// Goose is used as a LIBRARY rather than a separately-installed binary, so
// the version is pinned in go.mod and every agent on every machine runs the
// identical migrator with no extra install step. Same reasoning as the
// committed proto/gen: eliminate "works on my machine".
//
// Do not apply migrations with psql directly. The files carry `-- +goose Up`
// and `-- +goose Down` annotations that psql ignores, so it happily runs both
// halves and leaves you with an empty database. See docs/INCIDENTS.md.
//
// Usage:
//
//	go run ./scripts/migrate up          # apply everything pending
//	go run ./scripts/migrate status      # what is applied
//	go run ./scripts/migrate down        # roll back one (avoid; see below)
//
// Migrations are forward-only in practice during the build
// (docs/ARCHITECTURE.md section 12a). Recreating from `make down-clean` plus
// the batch generator is faster and safer than relying on a down step.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/pressly/goose/v3"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	dir := flag.String("dir", "./migrations", "directory holding migration files")
	dsn := flag.String("dsn", os.Getenv("POSTGRES_DSN"), "Postgres DSN (defaults to $POSTGRES_DSN)")
	flag.Parse()

	cmd := flag.Arg(0)
	if cmd == "" {
		cmd = "up"
	}

	if *dsn == "" {
		log.Fatal("no DSN: pass -dsn or set POSTGRES_DSN (see .env.example)")
	}

	db, err := goose.OpenDBWithDriver("pgx", *dsn)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer db.Close()

	goose.SetDialect("postgres")

	// A real context with a deadline. Never pass nil: goose derives from it,
	// and a nil context makes the run hang rather than fail. Also means a
	// migration wedged on a lock fails loudly instead of blocking CI forever.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var rest []string
	if args := flag.Args(); len(args) > 1 {
		rest = args[1:]
	}

	if err := goose.RunContext(ctx, cmd, db, *dir, rest...); err != nil {
		log.Fatalf("%s: %v", cmd, err)
	}
	fmt.Printf("migrate %s: ok\n", cmd)
}
