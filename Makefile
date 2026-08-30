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
	INGESTION_ADDR=localhost:9090 REPORTING_ADDR=localhost:9200 AUDIT_ADDR=localhost:9194 \
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
        run-notification-simulator
