# dev-restart.ps1 — Clean restart for TradeDrift local dev
#
# Solves the common dev-loop problem:
#   docker compose down wipes Kafka volumes but leaves Postgres intact.
#   The Matching Engine then fails recovery because its checkpoint offset
#   is ahead of Kafka's fresh log-end (0).
#
# This script:
#   1. Brings down all containers (with orphan removal)
#   2. Resets the ME's Postgres checkpoint/snapshot tables
#   3. Brings everything back up cleanly
#
# Usage:
#   .\scripts\dev-restart.ps1             # normal restart
#   .\scripts\dev-restart.ps1 -Build      # rebuild all images first
#   .\scripts\dev-restart.ps1 -ResetDB    # also wipe all service DBs (full reset)

param(
    [switch]$Build,
    [switch]$ResetDB
)

$ErrorActionPreference = "Stop"
$ProjectRoot = Split-Path $PSScriptRoot -Parent

Write-Host "==> Stopping all containers..." -ForegroundColor Cyan
docker compose -f "$ProjectRoot\docker-compose.yml" down --remove-orphans

if ($Build) {
    Write-Host "==> Rebuilding images..." -ForegroundColor Cyan
    docker compose -f "$ProjectRoot\docker-compose.yml" build
}

# Always reset ME checkpoints -- Kafka topic is wiped on compose down
Write-Host "==> Resetting Matching Engine Postgres checkpoints..." -ForegroundColor Cyan
$ME_DSN = "postgres://postgres:123@localhost:5432/tradedrift_matching?sslmode=disable"
psql $ME_DSN -c "TRUNCATE kafka_checkpoints, market_snapshots, market_sequences CASCADE;" | Out-Null
Write-Host "    kafka_checkpoints, market_snapshots, market_sequences cleared." -ForegroundColor Green

if ($ResetDB) {
    Write-Host "==> Full DB reset -- wiping all service databases..." -ForegroundColor Yellow
    $services = @(
        @{ dsn = "postgres://postgres:123@localhost:5432/tradedrift_auth?sslmode=disable";       tables = "users, refresh_tokens" },
        @{ dsn = "postgres://postgres:123@localhost:5432/tradedrift_wallet?sslmode=disable";     tables = "wallets, transactions, outbox" },
        @{ dsn = "postgres://postgres:123@localhost:5432/tradedrift_order?sslmode=disable";      tables = "orders, outbox" },
        @{ dsn = "postgres://postgres:123@localhost:5432/tradedrift_market?sslmode=disable";     tables = "markets" },
        @{ dsn = "postgres://postgres:123@localhost:5432/tradedrift_settlement?sslmode=disable"; tables = "settled_trades" },
        @{ dsn = "postgres://postgres:123@localhost:5432/tradedrift_trade?sslmode=disable";      tables = "trades" }
    )
    foreach ($svc in $services) {
        try {
            psql $svc.dsn -c "TRUNCATE $($svc.tables) CASCADE;" | Out-Null
            Write-Host "    Cleared: $($svc.tables)" -ForegroundColor Green
        } catch {
            Write-Host "    Skipped (DB may not exist yet): $($svc.dsn)" -ForegroundColor DarkGray
        }
    }
}

Write-Host "==> Starting all containers..." -ForegroundColor Cyan
docker compose -f "$ProjectRoot\docker-compose.yml" up -d

Write-Host ""
Write-Host "==> Stack is up! Check status:" -ForegroundColor Green
Write-Host "    docker compose ps"
Write-Host "    curl http://localhost:8081/status   (Liquidity Engine)"
Write-Host "    curl http://localhost:8080/healthz  (Gateway)"
