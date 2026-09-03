# Momotaro. Run `make help` for the list.
#
# Tool versions are pinned in tools.go / go.mod so every machine and every
# agent generates byte-identical code. Never invoke buf or protoc directly
# with an unpinned version, that reintroduces the "works on my machine"
# class of failure the committed proto/gen/ exists to prevent.

SHELL := /bin/bash
GOBIN ?= $(shell go env GOPATH)/bin
export PATH := $(GOBIN):$(PATH)

BUF_VERSION           := v1.47.2
PROTOC_GEN_GO_VERSION := v1.35.2
PROTOC_GEN_GRPC_VER   := v1.5.1

SERVICES := api-gateway ingestion decision-engine classifier executor audit reporting
DEMOS    := world-simulator notification-simulator

# Load .env into every recipe's environment (docs/ENGINEERING.md section 5:
# services read config from the environment, never a dotfile directly).
# Without this, `go run` sees none of POSTGRES_DSN / REDIS_ADDR / etc, since
# a Makefile recipe does not source .env on its own.
ifneq (,$(wildcard ./.env))
	include .env
	export
endif

# PROFILE selects a checked-in config profile from configs/, layered ON TOP of
# .env: `make run-decision-engine PROFILE=demo`, or PROFILE=dev.
#
# This exists because sourcing a profile into the shell does not work and
# silently appears to (docs/INCIDENTS.md 2026-08-31). GNU Make gives a variable
# assigned inside a makefile precedence over the same variable in the
# environment, so `include .env` above beat every `source configs/demo.env`,
# on every run-* target, always. configs/demo.env had therefore never once
# taken effect: every demo ran with real-time waits and no LLM chain.
#
# A second include is the fix because a later assignment wins, so values here
# override .env while anything the profile does not mention still falls through
# to it. Command-line variables (make run-x DEMO_TIME_SCALE=1) still outrank
# both, which is what makes a one-off override possible.
#
# Verify with:  make --eval='__show:; @echo $(DEMO_TIME_SCALE)' __show PROFILE=demo
ifneq (,$(PROFILE))
ifeq (,$(wildcard configs/$(PROFILE).env))
$(error PROFILE=$(PROFILE) but configs/$(PROFILE).env does not exist. Available: $(patsubst configs/%.env,%,$(wildcard configs/*.env)))
endif
include configs/$(PROFILE).env
export
endif

.DEFAULT_GOAL := help

## help: list targets
help:
	@grep -hE '^## ' $(MAKEFILE_LIST) | sed 's/## //' | awk -F': ' '{printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

## tools: install pinned codegen tooling
tools:
	go install github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)
	go install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GRPC_VER)

## proto: lint protos and regenerate proto/gen (commit the result)
proto: proto-lint
	cd proto && buf generate
	go mod tidy

## proto-lint: lint protos only
proto-lint:
	cd proto && buf lint

## proto-breaking: fail if protos broke compatibility vs origin/main
proto-breaking:
	cd proto && buf breaking --against '../.git#branch=origin/main,subdir=proto'

## build: build every service
build:
	go build ./...

## test: unit tests only, no infrastructure needed
test:
	go test -race ./...

## test-integration: tests that need the docker-compose stack (brings it up)
test-integration: up migrate-up
	go test -race -count=1 -tags='integration e2e' ./...

## vet: go vet
vet:
	go vet ./...

## fmt: format all Go code
fmt:
	go fmt ./...

## check: what CI runs. Run this before pushing.
check: fmt vet proto-lint build test

# Fixed local-dev ports, one pair per service, so all seven can run
# simultaneously against the same docker-compose infra without colliding on
# GRPC_PORT/METRICS_PORT (docs/DECISIONS.md). ingestion keeps
# .env.example's own GRPC_PORT=9090/METRICS_PORT=9091 (the "just one
# service" defaults); the rest start at 9190 rather than immediately after,
# to stay clear of Kafka's own host port 9092 (docker-compose.yml). Cross-
# service addresses are overridden here too, not in .env, since they only
# make sense together with these fixed ports.

## run-ingestion: run ingestion on its fixed local port
run-ingestion:
	GRPC_PORT=9090 METRICS_PORT=9091 go run ./services/ingestion/cmd

## run-classifier: run classifier on its fixed local port
run-classifier:
	GRPC_PORT=9190 METRICS_PORT=9191 go run ./services/classifier/cmd

## run-executor: run executor on its fixed local port
run-executor:
	GRPC_PORT=9192 METRICS_PORT=9193 \
	WORLD_SIMULATOR_ADDR=localhost:9202 NOTIFICATION_SIMULATOR_ADDR=localhost:9204 \
	go run ./services/executor/cmd

## run-audit: run audit on its fixed local port
run-audit:
	GRPC_PORT=9194 METRICS_PORT=9195 go run ./services/audit/cmd

## run-decision-engine: run decision-engine on its fixed local port
run-decision-engine:
	GRPC_PORT=9196 METRICS_PORT=9197 \
	CLASSIFIER_ADDR=localhost:9190 EXECUTOR_ADDR=localhost:9192 \
	go run ./services/decision-engine/cmd

## run-api-gateway: run api-gateway on its fixed local port
run-api-gateway:
	GRPC_PORT=9198 METRICS_PORT=9199 \
	INGESTION_ADDR=localhost:9090 REPORTING_ADDR=localhost:9200 AUDIT_ADDR=localhost:9194 DECISION_ENGINE_ADDR=localhost:9196 WORLD_SIMULATOR_ADDR=localhost:9202 \
	go run ./services/api-gateway/cmd

## run-reporting: run reporting on its fixed local port
run-reporting:
	GRPC_PORT=9200 METRICS_PORT=9201 go run ./services/reporting/cmd

## run-world-simulator: run world-simulator on its fixed local port
run-world-simulator:
	GRPC_PORT=9202 METRICS_PORT=9203 DECISION_ENGINE_ADDR=localhost:9196 \
	go run ./demo/world-simulator/cmd

## run-notification-simulator: run notification-simulator on its fixed local port
run-notification-simulator:
	GRPC_PORT=9204 METRICS_PORT=9205 go run ./demo/notification-simulator/cmd

DEMO_LOG_DIR ?= .demo-logs
ALL_RUNNABLE := $(SERVICES) $(DEMOS)

## demo-up: infra + migrations + all 9 services in the background (PROFILE=demo)
## Logs land in $(DEMO_LOG_DIR)/<service>.log. Stop with make demo-down.
demo-up: up migrate-up
	@mkdir -p $(DEMO_LOG_DIR)
	@echo "starting 9 services, PROFILE=$(if $(PROFILE),$(PROFILE),<none, see docs/INCIDENTS.md 2026-08-31>)"
        # Leaves first, then decision-engine (needs classifier+executor), then
        # api-gateway (needs ingestion+reporting+audit). gRPC dialing is lazy so
        # strict ordering is not required for correctness, only for clean logs.
	@for s in classifier executor audit ingestion world-simulator notification-simulator reporting; do \
		nohup $(MAKE) run-$$s PROFILE=$(PROFILE) > $(DEMO_LOG_DIR)/$$s.log 2>&1 & \
	done
	@$(MAKE) --no-print-directory wait-ports PORTS="9190 9192 9194 9090 9202 9204 9200"
	@for s in decision-engine api-gateway; do \
		nohup $(MAKE) run-$$s PROFILE=$(PROFILE) > $(DEMO_LOG_DIR)/$$s.log 2>&1 & \
	done
	@$(MAKE) --no-print-directory wait-ports PORTS="9196 8090"
	@echo "all 9 up. gateway: http://localhost:8090  logs: $(DEMO_LOG_DIR)/"
	@echo "next: make batchgen COUNT=100 SEED=7   then open web/ (npm run dev)"

## demo-down: stop the 9 services started by demo-up (leaves infra running)
demo-down:
        # Kill by listening port rather than by name: `go run` execs the binary
        # from a temp build path, so pgrep on the source path matches nothing.
	@for p in 8090 9090 9190 9192 9194 9196 9200 9202 9204 \
	          9091 9191 9193 9195 9197 9199 9201 9203 9205; do \
		pid=$$(ss -ltnp 2>/dev/null | grep ":$$p " | grep -oP 'pid=\K[0-9]+' | head -1); \
		[ -n "$$pid" ] && kill $$pid 2>/dev/null || true; \
	done
	@echo "services stopped (infra still up; make down to stop that too)"

## demo-reset: clear the decision-engine consumer group so a wedged stack
## recovers without a full down-clean (docs/INCIDENTS.md 2026-08-31).
## Safe to run any time the decision-engine is stopped or has crashed: it
## touches Kafka's group offsets only, never Postgres. On next start,
## decision-engine re-reads raw.events from the beginning of the topic, but
## HandleMessage's redelivery check (record_state already exists) skips
## every record it already finished, and a message pointing at a record
## that no longer exists is now dead-lettered instead of wedging the loop
## again, which is the whole point of Unit U.
demo-reset:
	docker compose exec -T kafka /opt/kafka/bin/kafka-consumer-groups.sh \
		--bootstrap-server localhost:29092 --delete \
		--group $(if $(RAW_EVENTS_CONSUMER_GROUP),$(RAW_EVENTS_CONSUMER_GROUP),decision-engine)
	@echo "consumer group cleared. restart decision-engine (make run-decision-engine or make demo-up) to resume."

# Internal: block until every port in PORTS is listening.
wait-ports:
	@for p in $(PORTS); do \
		until ss -ltn 2>/dev/null | grep -q ":$$p "; do sleep 1; done; \
	done

COUNT  ?= 100
SOURCE ?= synthetic-demo
SEED   ?=

## batchgen: seed a synthetic batch with hidden ground truth, straight into
## Postgres and raw.events (override with COUNT=n SOURCE=name SEED=n).
## Requires the stack up (make up) and decision-engine et al. already
## running (make run-<service>) to actually process what this seeds.
batchgen:
	go run ./scripts/batchgen -count $(COUNT) -source $(SOURCE) $(if $(SEED),-seed $(SEED),)

RATE        ?= 5
DURATION    ?= 5m
EVENTS      ?=
GATEWAY_URL ?= http://localhost:8090

## loadgen: post live traffic at the API Gateway's public webhook API,
## filling the Live Event Stream panel the way real production traffic
## would (docs/DEMO_READINESS.md Unit AJ). Steady rate, no ground truth,
## same synthetic vocabulary as batchgen. Defaults to a time-bounded run
## (override with RATE=n DURATION=5m); pass EVENTS=n instead of DURATION
## for a fixed total. Requires api-gateway already running
## (make run-api-gateway or make demo-up).
loadgen:
	go run ./scripts/loadgen -gateway-url $(GATEWAY_URL) -rate $(RATE) $(if $(EVENTS),-count $(EVENTS),-duration $(DURATION))

## up: start local infra (postgres, redis, kafka, kafka ui)
up:
	docker compose up -d
	@echo "kafka ui: http://localhost:8080"

# HOST_IP is what Prometheus (in its own container) dials to reach the
# fixed run-<service> ports on this host. host.docker.internal is the
# portable default (Docker Desktop mirrored networking, native Linux
# Engine 20.10+ via extra_hosts: host-gateway in docker-compose.observability.yml).
# Override it when that alias does not actually route to where
# run-<service> binds -- e.g. Docker Desktop + WSL2 in NAT networking mode,
# where host.docker.internal reaches Docker Desktop's own internal VM
# instead of this distro (docs/INCIDENTS.md 2026-08-29). Find your real one
# with `hostname -I | awk '{print $$1}'`, then:
#   make up-observability HOST_IP=172.25.75.22
HOST_IP ?= host.docker.internal

## up-observability: up, plus Prometheus/Alertmanager/Grafana scraping the fixed run-<service> ports
up-observability:
	sed 's/HOST_IP_PLACEHOLDER/$(HOST_IP)/g' deploy/observability/prometheus.yml.tmpl > deploy/observability/prometheus.generated.yml
	docker compose -f docker-compose.yml -f docker-compose.observability.yml up -d
	@echo "kafka ui:     http://localhost:8080"
	@echo "prometheus:   http://localhost:9900 (check Status > Targets: HOST_IP=$(HOST_IP))"
	@echo "alertmanager: http://localhost:9901"
	@echo "grafana:      http://localhost:9902 (admin/momotaro, or anonymous viewer)"

## down: stop local infra (base stack and observability, if either is up)
down:
	docker compose -f docker-compose.yml -f docker-compose.observability.yml down

## down-clean: stop local infra AND delete its data
down-clean:
	docker compose -f docker-compose.yml -f docker-compose.observability.yml down -v

## migrate-up: apply all migrations
migrate-up:
	go run ./scripts/migrate -dir ./migrations up

## migrate-status: show applied migrations
migrate-status:
	go run ./scripts/migrate -dir ./migrations status

## docker-build: build every service image (context is repo root, by design)
docker-build:
	@set -e; for s in $(SERVICES); do \
		echo "==> momotaro/$$s"; \
		docker build -q -f services/$$s/Dockerfile -t momotaro/$$s . ; \
	done; \
	for d in $(DEMOS); do \
		echo "==> momotaro/$$d"; \
		docker build -q -f demo/$$d/Dockerfile -t momotaro/$$d . ; \
	done

.PHONY: help tools proto proto-lint proto-breaking build test test-integration \
        vet fmt check up up-observability down down-clean migrate-up migrate-status \
        docker-build run-ingestion run-classifier run-executor run-audit \
        run-decision-engine run-api-gateway run-reporting run-world-simulator \
        run-notification-simulator batchgen loadgen demo-up demo-down demo-reset wait-ports
