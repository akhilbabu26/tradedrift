# ─── Stage 1: Build ───────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder

WORKDIR /workspace

# Copy the entire Go workspace
COPY go.work go.work.sum ./

# Copy all modules referenced in go.work
COPY platform/         ./platform/
COPY services/auth/    ./services/auth/
COPY services/wallet/  ./services/wallet/
COPY services/gateway/ ./services/gateway/
COPY services/order/   ./services/order/
COPY services/market/  ./services/market/

# Download dependencies for the market service
WORKDIR /workspace/services/market
RUN go mod download

# Build the market service binary
WORKDIR /workspace
RUN CGO_ENABLED=0 GOOS=linux go build -o /market-server ./services/market/cmd/server

# ─── Stage 2: Runtime ─────────────────────────────────────────────────────────
FROM alpine:3.20

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# Copy the compiled binary and migrations
COPY --from=builder /market-server ./market-server
COPY services/market/migration/ ./services/market/migration/

EXPOSE 50054

ENTRYPOINT ["./market-server"]
