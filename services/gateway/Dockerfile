# ─── Stage 1: Build ───────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder

WORKDIR /workspace

# Copy the entire Go workspace
COPY go.work go.work.sum ./

# Copy all modules the workspace references (go.work requires all go.mod files to be present)
COPY platform/          ./platform/
COPY services/auth/     ./services/auth/
COPY services/wallet/   ./services/wallet/
COPY services/gateway/  ./services/gateway/

# Download dependencies
WORKDIR /workspace/services/gateway
RUN go mod download

# Build the gateway binary using the workspace
WORKDIR /workspace
RUN CGO_ENABLED=0 GOOS=linux go build -o /gateway-server ./services/gateway/cmd/server

# ─── Stage 2: Runtime ─────────────────────────────────────────────────────────
FROM alpine:3.20

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

COPY --from=builder /gateway-server ./gateway-server

EXPOSE 8080

ENTRYPOINT ["./gateway-server"]
