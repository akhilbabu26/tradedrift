#!/usr/bin/env bash
# ==============================================================================
# TradeDrift Safe Database Migration Deployment Runner (Bash / EC2)
# ==============================================================================
# Usage:
#   ./scripts/deploy-migrations.sh -e <env-file> [-a <verify|status|up>] [--confirm-up]
#
# Examples:
#   1. Pre-flight connectivity check (default, read-only ping):
#      ./scripts/deploy-migrations.sh -e .env.production
#
#   2. Inspect migration versions & applied/pending status:
#      ./scripts/deploy-migrations.sh -e .env.production -a status
#
#   3. Apply pending migrations (requires explicit --confirm-up):
#      ./scripts/deploy-migrations.sh -e .env.production -a up --confirm-up
# ==============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

cd "${ROOT_DIR}"

ENV_FILE=""
ACTION="verify"
CONFIRM_UP=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        -e|--env-file)
            ENV_FILE="$2"
            shift 2
            ;;
        -a|--action)
            ACTION="$2"
            shift 2
            ;;
        --confirm-up)
            CONFIRM_UP=true
            shift
            ;;
        -h|--help)
            echo "Usage: $0 -e <env-file> [-a <verify|status|up>] [--confirm-up]"
            exit 0
            ;;
        *)
            echo "Unknown argument: $1" >&2
            exit 1
            ;;
    esac
done

echo "===================================================================="
echo " TradeDrift Safe Database Migration Runner (Linux / EC2)"
echo "===================================================================="

# ── 1. Validate & Resolve Environment File ────────────────────────────────────
if [ -z "${ENV_FILE}" ]; then
    echo "[fatal] You must specify an explicit environment file via -e (e.g. -e .env.production)" >&2
    exit 1
fi

if [ ! -f "${ENV_FILE}" ]; then
    echo "[fatal] Specified environment file '${ENV_FILE}' does not exist." >&2
    exit 1
fi

echo "[info] Target environment file: ${ENV_FILE}"

# ── 2. Validate Action ────────────────────────────────────────────────────────
case "${ACTION}" in
    verify|status|up)
        # Supported safe actions
        ;;
    *)
        echo "[fatal] Unsupported action '${ACTION}'. Supported actions: verify, status, up" >&2
        exit 1
        ;;
esac

# ── 3. Parse Environment Variables Safely (No Secret Leakage) ─────────────────
# Load variables into current subshell
set -a
# shellcheck disable=SC1090
source "${ENV_FILE}"
set +a

# Check for unresolved placeholders
if grep -Eqi "(REPLACE_WITH|placeholder|<.*>|CHANGE_ME)" "${ENV_FILE}"; then
    echo "[fatal] Environment file contains unresolved placeholders:" >&2
    grep -Eoi "(REPLACE_WITH[A-Z0-9_]*|placeholder|<.*>|CHANGE_ME)" "${ENV_FILE}" | head -n 5 >&2
    exit 1
fi

IS_PROD=false
if [ "${ENV:-}" = "production" ] || [ "${APP_ENV:-}" = "production" ]; then
    IS_PROD=true
fi

# ── 4. Pre-Flight DSN Safety Checks ───────────────────────────────────────────
echo ""
echo "── Pre-Flight DSN Safety Validation ──"

SERVICES=(
    "auth:AUTH_POSTGRES_DSN:tradedrift_auth"
    "wallet:WALLET_POSTGRES_DSN:tradedrift_wallet"
    "market:MARKET_POSTGRES_DSN:tradedrift_market"
    "order:ORDER_POSTGRES_DSN:tradedrift_order"
    "matching-engine:MATCHING_ENGINE_POSTGRES_DSN:tradedrift_matching"
    "settlement:SETTLEMENT_POSTGRES_DSN:tradedrift_settlement"
    "trade:TRADE_POSTGRES_DSN:tradedrift_trade"
    "portfolio:PORTFOLIO_POSTGRES_DSN:tradedrift_portfolio"
    "notification:NOTIFICATION_POSTGRES_DSN:tradedrift_notification"
    "admin:ADMIN_POSTGRES_DSN:tradedrift_admin"
    "wallet-topup:WALLET_TOPUP_POSTGRES_DSN:tradedrift_topup"
)

BASE_USER="${POSTGRES_USER:-tradedrift_admin}"
BASE_PASS="${POSTGRES_PASSWORD:-}"
BASE_HOST="${POSTGRES_HOST:-}"
BASE_PORT="${POSTGRES_PORT:-5432}"
BASE_SSLMODE="${POSTGRES_SSLMODE:-disable}"

VALIDATION_FAILED=false

for item in "${SERVICES[@]}"; do
    IFS=":" read -r svc var_name expected_db <<< "${item}"
    
    dsn="${!var_name:-}"
    if [ -z "${dsn}" ]; then
        if [ -n "${BASE_HOST}" ] && [ -n "${BASE_PASS}" ]; then
            escaped_pass=$(python3 -c "import urllib.parse, sys; print(urllib.parse.quote(sys.argv[1], safe=''))" "${BASE_PASS}" 2>/dev/null || echo "${BASE_PASS}")
            dsn="postgres://${BASE_USER}:${escaped_pass}@${BASE_HOST}:${BASE_PORT}/${expected_db}?sslmode=${BASE_SSLMODE}"
            export "${var_name}=${dsn}"
        else
            echo "  [FAIL] ${svc} : Missing ${var_name} and base POSTGRES_HOST/POSTGRES_PASSWORD" >&2
            VALIDATION_FAILED=true
            continue
        fi
    fi

    # Extract host and db
    extracted_host=$(echo "${dsn}" | sed -E -n 's/.*@([^/:]+).*/\1/p')
    extracted_db=$(echo "${dsn}" | sed -E -n 's/.*\/([^?]+).*/\1/p')

    if [ "${IS_PROD}" = "true" ]; then
        case "${extracted_host}" in
            localhost|127.0.0.1|host.docker.internal|::1)
                echo "  [FAIL] ${svc} : Production DSN targets forbidden loopback address: ${extracted_host}" >&2
                VALIDATION_FAILED=true
                continue
                ;;
        esac
    fi

    if [ "${extracted_db}" != "${expected_db}" ]; then
        echo "  [FAIL] ${svc} : Database mismatch. Expected '${expected_db}', found '${extracted_db}'" >&2
        VALIDATION_FAILED=true
        continue
    fi

    echo "  [OK]   ${svc} : DB: ${expected_db} | Host: ${extracted_host}"
done

if [ "${VALIDATION_FAILED}" = "true" ]; then
    echo "[fatal] Pre-flight DSN validation failed. Operation aborted." >&2
    exit 1
fi

# ── 5. Authorization Check for 'up' ───────────────────────────────────────────
if [ "${ACTION}" = "up" ]; then
    if [ "${CONFIRM_UP}" != "true" ]; then
        echo "" >&2
        echo "[BLOCKED] Action 'up' modifies database schema." >&2
        echo "To execute pending migrations, you must explicitly authorize with --confirm-up:" >&2
        echo "  $0 -e ${ENV_FILE} -a up --confirm-up" >&2
        echo "" >&2
        exit 1
    fi
    echo "[AUTHORIZED] Operator confirmed migration execution ('up')."
else
    echo "[SAFE] Action '${ACTION}' is non-destructive (no schema modifications)."
fi

# ── 6. Build Migration Binary ─────────────────────────────────────────────────
mkdir -p "${ROOT_DIR}/bin"
MIGRATE_BIN="${ROOT_DIR}/bin/migrate"

echo ""
echo "[info] Building migration runner binary from platform/cmd/migrate..."
go build -o "${MIGRATE_BIN}" ./platform/cmd/migrate

# ── 7. Execute Migration CLI ──────────────────────────────────────────────────
echo ""
echo "── Executing Migration CLI (Action: ${ACTION}, Strict: True) ──"

"${MIGRATE_BIN}" -service=all -action="${ACTION}" -strict -no-env

echo ""
echo "[success] Migration task '${ACTION}' completed successfully across all 11 databases."
