# syntax=docker/dockerfile:1.7

# -----------------------------------------------------------------------------
# Stage 1 — build
# BUILDPLATFORM keeps the toolchain native; TARGETOS/TARGETARCH come from buildx
# for cross-arch output. Module + build caches survive across runs.
# -----------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS builder

ARG TARGETOS
ARG TARGETARCH

ENV CGO_ENABLED=0 \
    GOOS=${TARGETOS:-linux} \
    GOARCH=${TARGETARCH:-amd64} \
    GOTOOLCHAIN=auto

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/backend ./cmd/server

# -----------------------------------------------------------------------------
# Stage 2 — runtime (~20 MB)
# -----------------------------------------------------------------------------
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S app && adduser -S -G app app

WORKDIR /app
COPY --from=builder /out/backend /app/backend
COPY configs /app/configs

USER app
EXPOSE 8081

ENTRYPOINT ["/app/backend"]
