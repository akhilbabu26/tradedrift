# syntax=docker/dockerfile:1

# ── BUILD STAGE ───────────────────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git

WORKDIR /workspace

# Copy go workspace files
COPY go.work go.work.sum ./

# Copy platform module (shared proto gen)
COPY platform/ platform/

# Copy the LE service
COPY services/liquidity-engine/ services/liquidity-engine/

# Download dependencies via workspace
RUN go work sync

WORKDIR /workspace/services/liquidity-engine

# Build the binary — CGO disabled for minimal scratch image
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /liquidity-engine ./cmd/server

# ── RUNTIME STAGE ─────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12

# Copy binary
COPY --from=builder /liquidity-engine /liquidity-engine

# Health and metrics ports
EXPOSE 8080 9090

# SINGLETON CONSTRAINT: replicas must be 1 in Kubernetes
ENTRYPOINT ["/liquidity-engine"]
