# ─── Stage 1: Build ───────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder

WORKDIR /workspace

# Copy the entire Go workspace (go.work resolves all modules)
COPY go.work go.work.sum ./

# Copy all modules referenced in go.work
COPY platform/                  ./platform/
COPY services/auth/             ./services/auth/
COPY services/wallet/           ./services/wallet/
COPY services/gateway/          ./services/gateway/
COPY services/order/            ./services/order/
COPY services/market/           ./services/market/
COPY services/matching-engine/  ./services/matching-engine/
COPY services/settlement/       ./services/settlement/
COPY services/liquidity-engine/ ./services/liquidity-engine/
COPY services/trade/            ./services/trade/
COPY services/portfolio/        ./services/portfolio/

# Build the settlement binary using Go module cache mounts for fast rebuilds
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
    -o /settlement ./services/settlement/cmd/server

# ─── Stage 2: Runtime ─────────────────────────────────────────────────────────
FROM alpine:3.20

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# Copy binary and migration files
COPY --from=builder /settlement ./settlement
COPY services/settlement/migration/ ./services/settlement/migration/

ENTRYPOINT ["./settlement"]
