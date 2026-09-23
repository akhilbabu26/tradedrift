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
    const d = toDecimal(value)
    const fixed = d.toFixed(2)
    if (signed && d.gt(0)) return `+${fixed}%`
    return `${fixed}%`
  } catch {
    return '0.00%'
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
