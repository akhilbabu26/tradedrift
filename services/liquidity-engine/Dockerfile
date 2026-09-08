# ── BUILD STAGE ───────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder

WORKDIR /workspace

# Copy go workspace files
COPY go.work go.work.sum ./

# Copy platform and all modules listed in go.work
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

# Build using Go module cache mount to share downloads across parallel builds
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /liquidity-engine ./services/liquidity-engine/cmd/server

# ── RUNTIME STAGE ─────────────────────────────────────────────────────────
FROM alpine:3.20

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# Copy binary
COPY --from=builder /liquidity-engine ./liquidity-engine

# Health and metrics ports
EXPOSE 8080 9090

# SINGLETON CONSTRAINT: replicas must be 1 in Kubernetes
ENTRYPOINT ["./liquidity-engine"]

