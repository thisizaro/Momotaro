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

.DEFAULT_GOAL := help

## help: list targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //' | awk -F': ' '{printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

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

## test: run all tests with race detection
test:
	go test -race ./...

## vet: go vet
vet:
	go vet ./...

## fmt: format all Go code
fmt:
	go fmt ./...

## check: what CI runs. Run this before pushing.
check: fmt vet proto-lint build test

## up: start local infra (postgres, redis, kafka, kafka ui)
up:
	docker compose up -d
	@echo "kafka ui: http://localhost:8080"

## down: stop local infra
down:
	docker compose down

## down-clean: stop local infra AND delete its data
down-clean:
	docker compose down -v

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

.PHONY: help tools proto proto-lint proto-breaking build test vet fmt check \
        up down down-clean migrate-up migrate-status docker-build
