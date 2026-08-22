# Multi-stage build for the world-simulator service.
#
# IMPORTANT: build context is the REPO ROOT, not this directory, because the
# build needs go.mod and internal/platform. See docs/ARCHITECTURE.md 2a.
#
#   docker build -f demo/world-simulator/Dockerfile -t momotaro/world-simulator .

FROM golang:1.26-alpine AS build
WORKDIR /src

# Dependency layer, cached independently of source changes.
COPY go.mod go.sum* ./
RUN go mod download

COPY . .
# CGO off so the binary runs in a distroless/scratch runtime.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/svc ./demo/world-simulator/cmd

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/svc /svc
USER nonroot:nonroot
ENTRYPOINT ["/svc"]
