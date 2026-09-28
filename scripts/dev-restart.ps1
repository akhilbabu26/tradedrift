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
$env:PGPASSWORD = "123"
try {
    psql -h localhost -p 5432 -U postgres -d tradedrift_matching -c "TRUNCATE kafka_checkpoints, market_snapshots, market_sequences CASCADE;" 2>$null | Out-Null
    Write-Host "    kafka_checkpoints, market_snapshots, market_sequences cleared." -ForegroundColor Green
} catch {
    Write-Host "    Matching Engine checkpoint reset skipped." -ForegroundColor DarkGray
}

if ($ResetDB) {
    Write-Host "==> Full DB reset -- wiping all service databases (schema reset)..." -ForegroundColor Yellow
    $dbs = @(
        'tradedrift_admin',
        'tradedrift_auth',
        'tradedrift_market',
        'tradedrift_matching',
        'tradedrift_notification',
        'tradedrift_order',
        'tradedrift_portfolio',
        'tradedrift_settlement',
        'tradedrift_topup',
        'tradedrift_trade',
        'tradedrift_wallet'
    )
    foreach ($db in $dbs) {
        try {
            psql -h localhost -p 5432 -U postgres -d $db -c "DROP SCHEMA public CASCADE; CREATE SCHEMA public; GRANT ALL ON SCHEMA public TO postgres; GRANT ALL ON SCHEMA public TO public;" 2>$null | Out-Null
            Write-Host "    Reset schema for: $db" -ForegroundColor Green
        } catch {
            Write-Host "    Skipped (DB may not exist yet): $db" -ForegroundColor DarkGray
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
