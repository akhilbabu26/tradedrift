# ─── Stage 1: Build ───────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder

WORKDIR /workspace

# Copy the entire Go workspace
COPY go.work go.work.sum ./

# Copy all modules referenced in go.work
COPY platform/                 ./platform/
COPY services/auth/            ./services/auth/
COPY services/wallet/          ./services/wallet/
COPY services/gateway/         ./services/gateway/
COPY services/order/           ./services/order/
COPY services/market/          ./services/market/
COPY services/matching-engine/ ./services/matching-engine/

# Build binary using Go module cache mount
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -o /matching-engine ./services/matching-engine/cmd/server

# ─── Stage 2: Runtime ─────────────────────────────────────────────────────────
FROM alpine:3.20

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# Copy the compiled binary and migrations
COPY --from=builder /matching-engine ./matching-engine
COPY services/matching-engine/migration/ ./services/matching-engine/migration/

ENTRYPOINT ["./matching-engine"]
