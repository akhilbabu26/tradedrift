import type { UTCTimestamp } from 'lightweight-charts'
import type { Candle, Timeframe } from '../types/trade'

export interface SanitizedBar {
  time: UTCTimestamp
  open: number
  high: number
  low: number
  close: number
}

export interface SanitizedVolume {
  time: UTCTimestamp
  value: number
  color: string
}

export function getTimeframeSeconds(tf: Timeframe): number {
  switch (tf) {
    case '1m':  return 60
    case '5m':  return 300
    case '15m': return 900
    case '1h':  return 3600
    case '4h':  return 14400
    case '1d':  return 86400
    default:    return 3600
  }
}

/**
 * Validates, sorts ascending, and strictly deduplicates candle timestamps.
 * Guarantees time[i] < time[i + 1] to prevent Lightweight Charts invariant crashes.
 */
export function sanitizeCandles(rawCandles: Candle[]): { bars: SanitizedBar[]; volumes: SanitizedVolume[] } {
  if (!Array.isArray(rawCandles) || rawCandles.length === 0) {
    return { bars: [], volumes: [] }
  }

  // 1. Validate numerical integrity and ISO timestamp
  const valid = rawCandles
    .map((c) => {
      if (!c || !c.start_time) return null
      const d = new Date(c.start_time)
      const ms = d.getTime()
      if (isNaN(ms) || ms <= 0) return null

      const time = Math.floor(ms / 1000) as UTCTimestamp
      const open = parseFloat(c.open)
      const high = parseFloat(c.high)
      const low = parseFloat(c.low)
      const close = parseFloat(c.close)
      const volume = parseFloat(c.volume || '0')

      if (
        isNaN(open) || isNaN(high) || isNaN(low) || isNaN(close) ||
        open <= 0 || high <= 0 || low <= 0 || close <= 0
      ) {
        return null
      }

      const bar: SanitizedBar = {
        time,
        open,
        high: Math.max(high, open, close),
        low:  Math.min(low,  open, close),
        close,
      }

      const vol: SanitizedVolume = {
        time,
        value: isNaN(volume) || volume < 0 ? 0 : volume,
        color: close >= open ? 'rgba(16,185,129,0.45)' : 'rgba(239,68,68,0.45)',
      }

      return { bar, vol }
    })
    .filter((x): x is { bar: SanitizedBar; vol: SanitizedVolume } => x !== null)

  if (valid.length === 0) {
    return { bars: [], volumes: [] }
  }

  // 2. Sort ascending by time
  valid.sort((a, b) => (a.bar.time as number) - (b.bar.time as number))

  // 3. Strict deduplication: keep LAST entry for duplicate timestamps
  const bars: SanitizedBar[] = []
  const volumes: SanitizedVolume[] = []

  for (const item of valid) {
    const lastBar = bars[bars.length - 1]
    if (lastBar && lastBar.time === item.bar.time) {
      bars[bars.length - 1] = item.bar
      volumes[volumes.length - 1] = item.vol
    } else {
      bars.push(item.bar)
      volumes.push(item.vol)
    }
  }

  return { bars, volumes }
}
