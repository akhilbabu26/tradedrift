# ─── Stage 1: Build ───────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder

WORKDIR /workspace

# Copy the entire Go workspace
COPY go.work go.work.sum ./

# Copy all modules the workspace uses
COPY platform/      ./platform/
COPY services/auth/ ./services/auth/
COPY services/wallet/ ./services/wallet/

# Download dependencies for the auth service
WORKDIR /workspace/services/auth
RUN go mod download

# Build the auth service binary using the workspace
WORKDIR /workspace
RUN CGO_ENABLED=0 GOOS=linux go build -o /auth-server ./services/auth/cmd/server

# ─── Stage 2: Runtime ─────────────────────────────────────────────────────────
FROM alpine:3.20

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# Copy the compiled binary
COPY --from=builder /auth-server ./auth-server

# Copy migration files so the service can find them at startup
COPY services/auth/migrations/ ./services/auth/migrations/

EXPOSE 50051

ENTRYPOINT ["./auth-server"]
