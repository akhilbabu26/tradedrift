# ── Stage 1: Build ────────────────────────────────────────────────────────────
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

# Build the trade binary
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
    -o /trade ./services/trade/cmd/server

# ── Stage 2: Runtime ──────────────────────────────────────────────────────────
FROM alpine:3.20

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

COPY --from=builder /trade ./trade
COPY services/trade/migration/ ./services/trade/migration/

ENTRYPOINT ["./trade"]
