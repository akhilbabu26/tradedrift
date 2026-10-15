// ─── Financial & Date Formatters ─────────────────────────────────────────────
// All display formatting for dashboard values lives here.
// Components never call toFixed() or Number() directly in JSX.

import { toDecimal } from './decimal'

/**
 * Formats a value as USDT with 2 decimal places and thousands separators.
 * e.g. "34250.80" → "34,250.80 USDT"
 */
export function formatUSDT(value: string | number): string {
  try {
    const d = toDecimal(value)
    return `${formatWithCommas(d.toFixed(2))} USDT`
  } catch {
    return '0.00 USDT'
  }
}

/**
 * Formats a price with 2 decimal places and thousands separators.
 * e.g. "68450.00" → "68,450.00"
 */
export function formatPrice(value: string | number): string {
  try {
    const d = toDecimal(value)
    return formatWithCommas(d.toFixed(2))
  } catch {
    return '0.00'
  }
}

/**
 * Formats a quantity (up to 4 decimal places).
 * e.g. "0.1200" → "0.1200"
 */
export function formatQuantity(value: string | number, decimals = 4): string {
  try {
    return toDecimal(value).toFixed(decimals)
  } catch {
    return '0.0000'
  }
}

/**
 * Formats a percentage with sign and 2 decimal places.
 * e.g. "3.38" with signed=true → "+3.38%"
 * e.g. "-0.70"                 → "-0.70%"
 */
export function formatPercentage(value: string | number, signed = true): string {
  try {
    const clean = typeof value === 'string' ? value.replace(/%/g, '').trim() : value
    const d = toDecimal(clean)
    const fixed = d.toFixed(2)
    if (signed && d.gt(0)) return `+${fixed}%`
    return `${fixed}%`
  } catch {
    return '0.00%'
  }
}

/**
 * Calculates order execution progress percentage clamped to [0, 100].
 * Safely handles missing, zero, invalid, or negative numbers without NaN / Infinity.
 * e.g. filled = 0.56, total = 5.0 -> 11.2
 */
export function calculateOrderProgress(
  filled: string | number | undefined | null,
  total: string | number | undefined | null
): number {
  try {
    const f = toDecimal(filled || '0')
    const t = toDecimal(total || '0')
    if (t.lte(0) || f.lte(0)) return 0
    if (f.gte(t)) return 100
    const pct = f.dividedBy(t).times(100).toNumber()
    if (isNaN(pct) || !isFinite(pct) || pct <= 0) return 0
    return Math.min(100, Math.max(0, Math.round(pct * 10) / 10))
  } catch {
    return 0
  }
}

/**
 * Formats order execution progress percentage string.
 * Formats whole percentages without decimals (e.g. 0%, 20%, 100%),
 * and fractional percentages with 1 decimal place (e.g. 11.2%).
 */
export function formatOrderProgress(progress: number | string | undefined | null): string {
  try {
    const p = typeof progress === 'number' ? progress : parseFloat(String(progress || '0'))
    if (isNaN(p) || !isFinite(p) || p <= 0) return '0%'
    if (p >= 100) return '100%'
    if (p % 1 === 0) return `${p.toFixed(0)}%`
    return `${p.toFixed(1)}%`
  } catch {
    return '0%'
  }
}

/**
 * Formats a raw quote turnover value into compact dollar currency string.
 * e.g. "3247011.56" → "$3.25M"
 *      "150250.00"  → "$150.3K"
 *      "45.20"      → "$45.20"
 */
export function formatTurnover(raw: string | number | undefined): string {
  if (!raw) return '$0'
  try {
    const v = toDecimal(raw)
    if (v.lte(0)) return '$0'
    if (v.gte(1_000_000_000)) return `$${v.dividedBy(1_000_000_000).toFixed(2)}B`
    if (v.gte(1_000_000))     return `$${v.dividedBy(1_000_000).toFixed(2)}M`
    if (v.gte(1_000))         return `$${v.dividedBy(1_000).toFixed(1)}K`
    return `$${v.toFixed(2)}`
  } catch {
    return '$0'
  }
}

/** Alias for backward compatibility */
export const formatVolume = formatTurnover

/**
 * Formats 24h high/low price string.
 * When price is 0, non-positive, or missing, returns "—" (never displays "$0.00").
 */
export function formatHighLow(price: string | number | undefined): string {
  if (!price) return '—'
  try {
    const d = toDecimal(price)
    if (d.lte(0)) return '—'
    return formatPrice(d.toString())
  } catch {
    return '—'
  }
}

/**
 * Formats a compact signed value with commas (no USDT suffix).
 * e.g. "1120.40"  → "+1,120.40"
 * e.g. "-250.00"  → "-250.00"
 */
export function formatCompact(value: string | number): string {
  try {
    const d = toDecimal(value)
    const fixed = formatWithCommas(d.abs().toFixed(2))
    if (d.gt(0)) return `+${fixed}`
    if (d.lt(0)) return `-${fixed}`
    return fixed
  } catch {
    return '0.00'
  }
}

/**
 * Formats a relative time from an ISO timestamp.
 * e.g. 12 minutes ago → "12m ago"
 *      1 hour ago     → "1h ago"
 *      2 hours ago    → "2h ago"
 */
export function formatRelativeTime(isoDate: string): string {
  try {
    const now = Date.now()
    const then = new Date(isoDate).getTime()
    const diffMs = now - then
    const diffMin = Math.floor(diffMs / 60_000)
    if (diffMin < 1) return 'just now'
    if (diffMin < 60) return `${diffMin}m ago`
    const diffHr = Math.floor(diffMin / 60)
    if (diffHr < 24) return `${diffHr}h ago`
    const diffDay = Math.floor(diffHr / 24)
    return `${diffDay}d ago`
  } catch {
    return '—'
  }
}

/**
 * Formats an ISO date into e.g. "Sep 14, 2026 12:45:10"
 */
export function formatDate(isoDate: string): string {
  try {
    const d = new Date(isoDate)
    if (isNaN(d.getTime())) return isoDate
    const months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']
    const month = months[d.getMonth()]
    const day = d.getDate()
    const year = d.getFullYear()
    const hours = String(d.getHours()).padStart(2, '0')
    const mins = String(d.getMinutes()).padStart(2, '0')
    const secs = String(d.getSeconds()).padStart(2, '0')
    return `${month} ${day}, ${year} ${hours}:${mins}:${secs}`
  } catch {
    return isoDate
  }
}

/**
 * Internal helper: adds thousands separators to a fixed decimal string.
 */
function formatWithCommas(value: string): string {
  const [int, dec] = value.split('.')
  const formatted = int.replace(/\B(?=(\d{3})+(?!\d))/g, ',')
  return dec !== undefined ? `${formatted}.${dec}` : formatted
}

/**
 * Derives price display precision and minimum move from market tick size.
 * e.g. "0.01"   → { precision: 2, minMove: 0.01 }
 *      "0.001"  → { precision: 3, minMove: 0.001 }
 *      "0.0001" → { precision: 4, minMove: 0.0001 }
 * Falls back to 2 decimals / 0.01 if tickSize is missing or invalid.
 */
export function getPrecisionFromTickSize(tickSize?: string): { precision: number; minMove: number } {
  if (!tickSize) return { precision: 2, minMove: 0.01 }
  const parsed = parseFloat(tickSize)
  if (isNaN(parsed) || parsed <= 0) return { precision: 2, minMove: 0.01 }
  const parts = tickSize.split('.')
  const decimals = parts.length > 1 ? parts[1].length : 0
  return { precision: Math.max(0, Math.min(decimals, 8)), minMove: parsed }
}

