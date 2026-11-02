# ==============================================================================
# TradeDrift Safe Database Migration Deployment Runner (PowerShell)
# ==============================================================================
# Usage:
#   .\scripts\deploy-migrations.ps1 -EnvFile <path> [-Action <verify|status|up>] [-ConfirmUp]
#
# Examples:
#   1. Verify database connectivity (default, safe read-only ping):
#      .\scripts\deploy-migrations.ps1 -EnvFile .env.production
#
#   2. Inspect migration versions & applied/pending status:
#      .\scripts\deploy-migrations.ps1 -EnvFile .env.production -Action status
#
#   3. Apply pending migrations (requires explicit -ConfirmUp switch):
#      .\scripts\deploy-migrations.ps1 -EnvFile .env.production -Action up -ConfirmUp
# ==============================================================================

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true, HelpMessage = "Path to environment file (e.g. .env.production)")]
    [string]$EnvFile,

    [Parameter(Mandatory = $false)]
    [ValidateSet("verify", "status", "up")]
    [string]$Action = "verify",

    [Parameter(Mandatory = $false)]
    [switch]$ConfirmUp
)

$ErrorActionPreference = "Stop"

$RootDir = Split-Path -Parent $PSScriptRoot
Set-Location $RootDir

Write-Host "====================================================================" -ForegroundColor Cyan
Write-Host " TradeDrift Safe Database Migration Runner (PowerShell)" -ForegroundColor Cyan
Write-Host "====================================================================" -ForegroundColor Cyan

# ── 1. Validate & Resolve Environment File ────────────────────────────────────
if ([string]::IsNullOrWhiteSpace($EnvFile)) {
    Write-Error "[fatal] You must specify an explicit environment file via -EnvFile (e.g. -EnvFile .env.production)"
    exit 1
}

$ResolvedEnvPath = $EnvFile
if (-not [System.IO.Path]::IsPathRooted($ResolvedEnvPath)) {
    $ResolvedEnvPath = Join-Path $RootDir $EnvFile
}

if (-not (Test-Path $ResolvedEnvPath -PathType Leaf)) {
    Write-Error "[fatal] Specified environment file '$ResolvedEnvPath' does not exist."
    exit 1
}

Write-Host "[info] Target environment file: $ResolvedEnvPath" -ForegroundColor Yellow

# ── 2. Parse Environment Variables Safely (No Secret Leakage) ─────────────────
$ParsedEnv = @{}
$UnresolvedPlaceholders = @()
$LineNum = 0

foreach ($line in Get-Content $ResolvedEnvPath) {
    $LineNum++
    $trimmed = $line.Trim()
    if ($trimmed -eq "" -or $trimmed.StartsWith("#")) {
        continue
    }

    $eqIdx = $trimmed.IndexOf("=")
    if ($eqIdx -le 0) {
        Write-Error "[fatal] Malformed environment entry at line $LineNum in $EnvFile"
        exit 1
    }

    $key = $trimmed.Substring(0, $eqIdx).Trim()
    $val = $trimmed.Substring($eqIdx + 1).Trim()

    # Strip surrounding quotes if present
    if (($val.StartsWith('"') -and $val.EndsWith('"')) -or ($val.StartsWith("'") -and $val.EndsWith("'"))) {
        if ($val.Length -ge 2) {
            $val = $val.Substring(1, $val.Length - 2)
        }
    }

    if ($val -match "(?i)(REPLACE_WITH|placeholder|<.*>|CHANGE_ME)") {
        $UnresolvedPlaceholders += "$key (contains placeholder)"
    }

    $ParsedEnv[$key] = $val
}

if ($UnresolvedPlaceholders.Count -gt 0) {
    Write-Error "[fatal] Environment file contains unresolved placeholders:`n  - $($UnresolvedPlaceholders -join "`n  - ")"
    exit 1
}

# ── 3. Production Environment & DSN Safety Checks ─────────────────────────────
$IsProd = ($ParsedEnv["ENV"] -eq "production" -or $ParsedEnv["APP_ENV"] -eq "production")

# Expected 11 databases and their env vars
$ServiceMap = [ordered]@{
    "auth"            = @{ EnvVar = "AUTH_POSTGRES_DSN";            ExpectedDB = "tradedrift_auth" }
    "wallet"          = @{ EnvVar = "WALLET_POSTGRES_DSN";          ExpectedDB = "tradedrift_wallet" }
    "market"          = @{ EnvVar = "MARKET_POSTGRES_DSN";          ExpectedDB = "tradedrift_market" }
    "order"           = @{ EnvVar = "ORDER_POSTGRES_DSN";           ExpectedDB = "tradedrift_order" }
    "matching-engine" = @{ EnvVar = "MATCHING_ENGINE_POSTGRES_DSN"; ExpectedDB = "tradedrift_matching" }
    "settlement"      = @{ EnvVar = "SETTLEMENT_POSTGRES_DSN";      ExpectedDB = "tradedrift_settlement" }
    "trade"           = @{ EnvVar = "TRADE_POSTGRES_DSN";           ExpectedDB = "tradedrift_trade" }
    "portfolio"       = @{ EnvVar = "PORTFOLIO_POSTGRES_DSN";       ExpectedDB = "tradedrift_portfolio" }
    "notification"    = @{ EnvVar = "NOTIFICATION_POSTGRES_DSN";    ExpectedDB = "tradedrift_notification" }
    "admin"           = @{ EnvVar = "ADMIN_POSTGRES_DSN";           ExpectedDB = "tradedrift_admin" }
    "wallet-topup"    = @{ EnvVar = "WALLET_TOPUP_POSTGRES_DSN";    ExpectedDB = "tradedrift_topup" }
}

# If specific service DSNs are not set individually, check for base connection variables
$BaseHost = $ParsedEnv["POSTGRES_HOST"]
$BasePort = if ($ParsedEnv["POSTGRES_PORT"]) { $ParsedEnv["POSTGRES_PORT"] } else { "5432" }
$BaseUser = if ($ParsedEnv["POSTGRES_USER"]) { $ParsedEnv["POSTGRES_USER"] } else { "tradedrift_admin" }
$BasePass = $ParsedEnv["POSTGRES_PASSWORD"]
$BaseSSLMode = if ($ParsedEnv["POSTGRES_SSLMODE"]) { $ParsedEnv["POSTGRES_SSLMODE"] } else { "disable" }

Write-Host "`n── Pre-Flight DSN Safety Validation ──" -ForegroundColor Yellow

function Get-SafeDSNMetadata([string]$rawDSN) {
    if ([string]::IsNullOrWhiteSpace($rawDSN)) {
        return @{ Host = ""; DB = "" }
    }
    if ($rawDSN.StartsWith("postgres://") -or $rawDSN.StartsWith("postgresql://")) {
        try {
            $u = [System.Uri]$rawDSN
            $db = $u.AbsolutePath.TrimStart('/')
            if ($db.Contains("?")) {
                $db = $db.Substring(0, $db.IndexOf("?"))
            }
            $h = if ($u.Port -gt 0 -and $u.Port -ne 5432) { "$($u.Host):$($u.Port)" } else { $u.Host }
            return @{ Host = $h; DB = $db }
        } catch {
            return @{ Host = "unknown-host"; DB = "unknown-db" }
        }
    }
    # Keyword/value format
    $h = if ($rawDSN -match "host=([^\s]+)") { $matches[1].Trim("'", '"') } else { "unknown-host" }
    $d = if ($rawDSN -match "dbname=([^\s]+)") { $matches[1].Trim("'", '"') } else { "unknown-db" }
    return @{ Host = $h; DB = $d }
}

$ValidationFailed = $false
foreach ($svc in $ServiceMap.Keys) {
    $info = $ServiceMap[$svc]
    $varName = $info.EnvVar
    $expectedDB = $info.ExpectedDB

    $dsn = $ParsedEnv[$varName]
    if ([string]::IsNullOrWhiteSpace($dsn)) {
        if (-not [string]::IsNullOrWhiteSpace($BaseHost) -and -not [string]::IsNullOrWhiteSpace($BasePass)) {
            $escapedPass = [System.Uri]::EscapeDataString($BasePass)
            $dsn = "postgres://${BaseUser}:${escapedPass}@${BaseHost}:${BasePort}/${expectedDB}?sslmode=${BaseSSLMode}"
            $ParsedEnv[$varName] = $dsn
        } else {
            Write-Host "  [FAIL] $svc : Missing $varName or base POSTGRES_HOST/POSTGRES_PASSWORD" -ForegroundColor Red
            $ValidationFailed = $true
            continue
        }
    }

    $meta = Get-SafeDSNMetadata -rawDSN $dsn
    $extractedHost = $meta.Host
    $extractedDB = $meta.DB

    # Validate against localhost in production
    if ($IsProd) {
        $lowerHost = $extractedHost.ToLower()
        if ($lowerHost -match "(localhost|127\.0\.0\.1|host\.docker\.internal|::1)") {
            Write-Host "  [FAIL] $svc : Production DSN targets forbidden loopback address: $extractedHost" -ForegroundColor Red
            $ValidationFailed = $true
            continue
        }
    }

    # Validate database name
    if ($extractedDB -ne $expectedDB) {
        Write-Host "  [FAIL] $svc : Database name mismatch. Expected '$expectedDB', found '$extractedDB'" -ForegroundColor Red
        $ValidationFailed = $true
        continue
    }

    Write-Host "  [OK]   $svc : DB: $expectedDB | Host: $extractedHost" -ForegroundColor Green
}

if ($ValidationFailed) {
    Write-Error "[fatal] Pre-flight DSN validation failed. Operation aborted to protect database integrity."
    exit 1
}

# ── 4. Verify Explicit Intent for 'up' Action ─────────────────────────────────
if ($Action -eq "up") {
    if (-not $ConfirmUp) {
        Write-Host "`n[BLOCKED] Action 'up' modifies database schema." -ForegroundColor Red
        Write-Host "To execute pending migrations, you must explicitly authorize by adding -ConfirmUp:`n" -ForegroundColor Yellow
        Write-Host "  .\scripts\deploy-migrations.ps1 -EnvFile $EnvFile -Action up -ConfirmUp`n" -ForegroundColor Cyan
        exit 1
    }
    Write-Host "`n[AUTHORIZED] Operator confirmed migration execution ('up')." -ForegroundColor Magenta
} else {
    Write-Host "`n[SAFE] Action '$Action' is non-destructive (no schema modifications)." -ForegroundColor Cyan
}

# ── 5. Inject Clean Environment Variables into Process ────────────────────────
foreach ($k in $ParsedEnv.Keys) {
    [System.Environment]::SetEnvironmentVariable($k, $ParsedEnv[$k], [System.EnvironmentVariableTarget]::Process)
}

# ── 6. Build Migration Binary ─────────────────────────────────────────────────
if (-not (Test-Path "bin")) {
    New-Item -ItemType Directory -Path "bin" | Out-Null
}

$MigrateBin = Join-Path $RootDir "bin\migrate.exe"
Write-Host "`n[info] Building migration runner binary from platform/cmd/migrate..." -ForegroundColor Yellow
go build -o $MigrateBin ./platform/cmd/migrate
if ($LASTEXITCODE -ne 0) {
    Write-Error "[fatal] Failed to build migration binary. Build exited with code $LASTEXITCODE."
    exit $LASTEXITCODE
}

# ── 7. Execute Migration CLI ──────────────────────────────────────────────────
Write-Host "`n── Executing Migration CLI (Action: $Action, Strict: True) ──" -ForegroundColor Yellow

$cliArgs = @(
    "-service=all",
    "-action=$Action",
    "-strict",
    "-no-env"
)

& $MigrateBin @cliArgs
$exitCode = $LASTEXITCODE

if ($exitCode -eq 0) {
    Write-Host "`n[success] Migration task '$Action' completed successfully across all 11 databases." -ForegroundColor Green
    exit 0
} else {
    Write-Host "`n[fatal] Migration task '$Action' failed with exit code $exitCode." -ForegroundColor Red
    exit $exitCode
}
