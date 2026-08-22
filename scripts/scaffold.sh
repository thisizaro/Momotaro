#!/usr/bin/env bash
#
# Generates the repo's service tree. Run ONCE by the orchestrator and commit
# the result; agents do not run this. Idempotent: existing files are never
# overwritten, so it is safe to re-run after adding a service.
#
#   ./scripts/scaffold.sh
#
# Layout rationale is docs/ARCHITECTURE.md section 2a.

set -euo pipefail

cd "$(dirname "$0")/.."
ROOT="$(pwd)"
MODULE="github.com/thisizaro/Momotaro"

# name|kind|proto_pkg|one-line description
# proto_pkg empty means this service exposes no inbound gRPC.
SERVICES=(
  "api-gateway|service||HTTP/WS edge. Translates external requests into internal gRPC. The only door in."
  "ingestion|service|ingestion|Accepts failure events (webhook or batch) and publishes them to raw.events."
  "decision-engine|service|decisionengine|Owns each record's state machine, the keyed worker pool, and the scheduler worker."
  "classifier|service|classifier|Diagnoses root cause and composes nudge text. Owns all LLM access."
  "executor|service|executor|Executes a chosen action exactly once against the recovery and notification ports."
  "audit|service|audit|Serves record audit trails and continuously verifies the correctness invariants."
  "reporting|service|reporting|Aggregates batch results and streams live updates."
  "world-simulator|demo|worldsim|Stands in for the bank and the customer. Holds the sealed ground truth."
  "notification-simulator|demo|notifier|Stands in for an SMS/WhatsApp provider. Logs what it would have sent."
)

PLATFORM_PKGS=(clock config logger interceptors kafkax pgx)

say() { printf '  %s\n' "$1"; }

keep() { [ -e "$1/.gitkeep" ] || : > "$1/.gitkeep"; }

# ---------------------------------------------------------------- shared dirs
say "shared directories"
for p in "${PLATFORM_PKGS[@]}"; do
  mkdir -p "internal/platform/$p"; keep "internal/platform/$p"
done
mkdir -p proto migrations deploy
keep proto; keep migrations; keep deploy

# ------------------------------------------------------------------- services
for entry in "${SERVICES[@]}"; do
  IFS='|' read -r name kind proto desc <<<"$entry"
  base="services/$name"; [ "$kind" = demo ] && base="demo/$name"

  say "$base"
  mkdir -p "$base/cmd" "$base/internal"
  keep "$base/internal"

  # ---- entrypoint
  if [ ! -f "$base/cmd/main.go" ]; then
    pkgname="$(echo "$name" | tr -d '-')"
    cat >"$base/cmd/main.go" <<GO
// Command $name is the entrypoint for the $name service.
//
// $desc
//
// See docs/ARCHITECTURE.md for where this sits in the system, and
// docs/ENGINEERING.md for the rules this service must follow (fail-fast
// config, graceful shutdown, injected clock, context deadlines).
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

const serviceName = "$name"

func main() {
	// Root context cancelled on SIGTERM/SIGINT so shutdown is graceful.
	// ENGINEERING.md section 6: drain in-flight work, commit offsets, exit.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", serviceName)

	if err := run(ctx, log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
	log.Info("shutdown complete")
}

func run(ctx context.Context, log *slog.Logger) error {
	// TODO($pkgname): load+validate config (fail fast), dial dependencies,
	// start the gRPC server / Kafka consumer, block until ctx is done.
	log.Info("not implemented yet")
	<-ctx.Done()
	return nil
}
GO
  fi

  # ---- Dockerfile
  if [ ! -f "$base/Dockerfile" ]; then
    cat >"$base/Dockerfile" <<DOCKER
# Multi-stage build for the $name service.
#
# IMPORTANT: build context is the REPO ROOT, not this directory, because the
# build needs go.mod and internal/platform. See docs/ARCHITECTURE.md 2a.
#
#   docker build -f $base/Dockerfile -t momotaro/$name .

FROM golang:1.26-alpine AS build
WORKDIR /src

# Dependency layer, cached independently of source changes.
COPY go.mod go.sum* ./
RUN go mod download

COPY . .
# CGO off so the binary runs in a distroless/scratch runtime.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \\
    -o /out/svc ./$base/cmd

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/svc /svc
USER nonroot:nonroot
ENTRYPOINT ["/svc"]
DOCKER
  fi

  # ---- boundary contract
  if [ ! -f "$base/AGENTS.md" ]; then
    if [ -n "$proto" ]; then
      iface="\`proto/$proto/v1/$proto.proto\` is the source of truth for this
service's API. Do not describe the interface in prose here, point at the
proto. Changing it requires its own PR, merged before any code that depends
on the new shape."
    else
      iface="This service exposes **no inbound gRPC**. It is an HTTP/WebSocket
server and a gRPC *client*. Its external contract is
\`docs/API_GATEWAY.md\`, which is also the only doc the \`web/\` agent needs."
    fi

    cat >"$base/AGENTS.md" <<MD
# AGENTS.md ($name)

## What this service does

$desc

Full detail in \`docs/ARCHITECTURE.md\`. Read \`AGENTS.md\` at the repo root
first, and \`docs/ENGINEERING.md\` before writing any code.

## Interface

$iface

## Owns

- \`$base/**\`, and nothing outside it.
- Postgres tables: see the ownership table in \`docs/ARCHITECTURE.md\` §10a.
  Write only what this service owns; read the rest.

## Must not touch

- Any other service's directory. \`$base/internal/**\` is private to this
  service and the compiler enforces it in both directions.
- \`proto/\`, \`migrations/\`, \`internal/platform/\`. If you need a change in
  any of them, **stop and propose it** rather than making it. Another agent
  is probably working there.
- Any table this service does not own.

## Depends on

- Shared code comes from \`internal/platform/\` only (clock, config, logger,
  gRPC interceptors). Never copy those into this service.
- Cross-service calls are gRPC, always. Never an in-process import.

## Reminders

- TDD, injected clock, context deadlines on every outbound call, graceful
  shutdown, money as integer paise. \`docs/ENGINEERING.md\`.
- Log what breaks to \`docs/INCIDENTS.md\` while it is fresh.
- No AI attribution in commits or PR descriptions.
MD
  fi
done

# ---------------------------------------------------------------------- report
echo
say "done. tree:"
find services demo internal proto migrations -maxdepth 2 -type d 2>/dev/null \
  | grep -v '\.git' | sort | sed 's/^/    /'
echo
say "next: go build ./... && see docs/PLAN.md Phase 0"
