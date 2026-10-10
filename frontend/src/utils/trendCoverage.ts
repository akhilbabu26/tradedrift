/**
 * Trend coverage evaluation utility.
 *
 * Implements strict data-sufficiency thresholds to prevent sparse historical data
 * (e.g. 2-3 hours after a database reset) from falsely presenting as a "7D Trend".
 */

export type TrendCoverageStatus = 'accumulating' | 'provisional' | 'partial' | 'full'

export interface TrendCoverage {
  status: TrendCoverageStatus
  label: string
  hoursCovered: number
  isSufficient: boolean
}

/**
 * Evaluates the genuine coverage of a trend dataset using both observation count
 * and timestamp range.
 *
 * Policy:
 * - Fewer than 12 hourly observations or < 12h timespan: 'accumulating' (neutral placeholder, no sparkline)
 * - 12–23 hourly points (12–23h coverage): 'provisional' (renders sparkline with 'Trend · Nh')
 * - 24–167 hourly points (24–167h coverage): 'partial' (renders sparkline with 'Trend · Nd' or 'Trend · Nd Nh')
 * - 168+ hourly points spanning at least 7 full days (168h): 'full' (renders '7D Trend')
 */
export function evaluateTrendCoverage(
  pointCount: number,
  startTime?: string | number,
  endTime?: string | number
): TrendCoverage {
  if (pointCount < 1) {
    return {
      status: 'accumulating',
      label: 'Accumulating data',
      hoursCovered: 0,
      isSufficient: false,
    }
  }

  // Calculate actual time span in hours if timestamps are provided
  let hours = pointCount
  if (startTime && endTime) {
    const startMs = typeof startTime === 'string' ? new Date(startTime).getTime() : startTime * 1000
    const endMs = typeof endTime === 'string' ? new Date(endTime).getTime() : endTime * 1000
    if (!isNaN(startMs) && !isNaN(endMs) && endMs > startMs) {
      // Add 1 hour because each hourly candle covers a 1h duration window
      const spanHours = Math.round((endMs - startMs) / (3600 * 1000)) + 1
      hours = Math.max(pointCount, spanHours)
    }
  }

  if (pointCount < 2) {
    return {
      status: 'accumulating',
      label: pointCount === 1 ? '1 observation' : 'Accumulating data',
      hoursCovered: hours,
      isSufficient: false,
    }
  }

  if (hours < 24) {
    return {
      status: 'provisional',
      label: `Trend · ${hours}h`,
      hoursCovered: hours,
      isSufficient: true,
    }
  }

  if (hours < 168 || pointCount < 168) {
    const days = Math.floor(hours / 24)
    const remHours = hours % 24
    const label = remHours > 0 ? `Trend · ${days}d ${remHours}h` : `Trend · ${days}d`
    return {
      status: 'partial',
      label,
      hoursCovered: hours,
      isSufficient: true,
    }
  }

  return {
    status: 'full',
    label: '7D Trend',
    hoursCovered: hours,
    isSufficient: true,
  }
}
