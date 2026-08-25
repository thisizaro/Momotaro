# Multi-stage build for the decision-engine service.
#
# IMPORTANT: build context is the REPO ROOT, not this directory, because the
# build needs go.mod and internal/platform. See docs/ARCHITECTURE.md 2a.
#
#   docker build -f services/decision-engine/Dockerfile -t momotaro/decision-engine .

FROM golang:1.26-alpine AS build
WORKDIR /src

# Dependency layer, cached independently of source changes.
COPY go.mod go.sum* ./
RUN go mod download

COPY . .
# CGO off so the binary runs in a distroless/scratch runtime.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/svc ./services/decision-engine/cmd

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/svc /svc
# The economics config travels with the binary. It is read at startup and the
# service refuses to run without it, deliberately: an engine that cannot price
# its actions would score every one at zero and close every record as
# uneconomic, which looks like a healthy agent doing nothing.
COPY --from=build /src/configs /configs
USER nonroot:nonroot
ENTRYPOINT ["/svc"]
