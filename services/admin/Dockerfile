# ─── Stage 1: Build ───────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder

WORKDIR /workspace

COPY go.work go.work.sum* ./
COPY platform/ platform/
COPY services/admin/ services/admin/
COPY services/auth/ services/auth/
COPY services/wallet/ services/wallet/
COPY services/wallet-topup/ services/wallet-topup/
COPY services/gateway/ services/gateway/
COPY services/order/ services/order/
COPY services/matching-engine/ services/matching-engine/
COPY services/trade/ services/trade/
COPY services/settlement/ services/settlement/
COPY services/market/ services/market/
COPY services/portfolio/ services/portfolio/
COPY services/notification/ services/notification/
COPY services/liquidity-engine/ services/liquidity-engine/

WORKDIR /workspace/services/admin

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/admin cmd/server/main.go

# ─── Stage 2: Minimal Runtime ──────────────────────────────────────────────────
FROM alpine:3.20

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

COPY --from=builder /bin/admin /app/admin
COPY --from=builder /workspace/services/admin/migrations /app/migrations

EXPOSE 8085

ENTRYPOINT ["/app/admin"]
