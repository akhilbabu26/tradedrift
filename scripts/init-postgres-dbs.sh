#!/bin/bash
set -e

# ==============================================================================
# TradeDrift PostgreSQL Multi-Database Initialization Script
# Executed automatically by postgres container on first startup (/docker-entrypoint-initdb.d)
# ==============================================================================

DATABASES=(
    "tradedrift_admin"
    "tradedrift_auth"
    "tradedrift_market"
    "tradedrift_matching"
    "tradedrift_notification"
    "tradedrift_order"
    "tradedrift_portfolio"
    "tradedrift_settlement"
    "tradedrift_topup"
    "tradedrift_trade"
    "tradedrift_wallet"
)

echo "[postgres-init] Initializing TradeDrift isolated databases..."

for db in "${DATABASES[@]}"; do
    echo "[postgres-init] Checking database: ${db}"
    psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
        SELECT 'CREATE DATABASE ${db}' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '${db}')\gexec
EOSQL
    echo "[postgres-init] Database ${db} verified/created."
done

echo "[postgres-init] All 11 TradeDrift databases initialized successfully."

