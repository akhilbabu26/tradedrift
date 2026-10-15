import type { UTCTimestamp } from 'lightweight-charts'

export interface IndicatorPoint {
  time: UTCTimestamp
  value: number
}

export interface BollingerBandsPoints {
  upper: IndicatorPoint[]
  middle: IndicatorPoint[]
  lower: IndicatorPoint[]
}

export interface ActiveIndicators {
  ema20: boolean
  ema50: boolean
  ema200: boolean
  bollinger: boolean
}

export const DEFAULT_ACTIVE_INDICATORS: ActiveIndicators = {
  ema20: false,
  ema50: false,
  ema200: false,
  bollinger: false,
}

/**
 * Calculates Exponential Moving Average (EMA) for period N over close prices.
 */
export function calculateEMA(
  data: { time: UTCTimestamp; close: number }[],
  period: number
): IndicatorPoint[] {
  if (!Array.isArray(data) || data.length < period || period <= 0) return []

  const k = 2 / (period + 1)
  const results: IndicatorPoint[] = []

  // First EMA is the simple average of the first `period` bars
  let sum = 0
  for (let i = 0; i < period; i++) {
    sum += data[i].close
  }
  let prevEma = sum / period

  results.push({
    time: data[period - 1].time,
    value: Number(prevEma.toFixed(6)),
  })

  // Subsequent EMAs
  for (let i = period; i < data.length; i++) {
    const currentClose = data[i].close
    const currentEma = currentClose * k + prevEma * (1 - k)
    results.push({
      time: data[i].time,
      value: Number(currentEma.toFixed(6)),
    })
    prevEma = currentEma
  }

  return results
}

/**
 * Calculates Bollinger Bands (period, stdDevMultiplier) over close prices.
 * Defaults to 20 periods, 2 standard deviations.
 */
export function calculateBollingerBands(
  data: { time: UTCTimestamp; close: number }[],
  period: number = 20,
  stdDevMultiplier: number = 2
): BollingerBandsPoints {
  if (!Array.isArray(data) || data.length < period || period <= 0) {
    return { upper: [], middle: [], lower: [] }
  }

  const upper: IndicatorPoint[] = []
  const middle: IndicatorPoint[] = []
  const lower: IndicatorPoint[] = []

  for (let i = period - 1; i < data.length; i++) {
    let sum = 0
    for (let j = i - period + 1; j <= i; j++) {
      sum += data[j].close
    }
    const sma = sum / period

    let varianceSum = 0
    for (let j = i - period + 1; j <= i; j++) {
      varianceSum += Math.pow(data[j].close - sma, 2)
    }
    const stdDev = Math.sqrt(varianceSum / period)

    const u = sma + stdDevMultiplier * stdDev
    const l = sma - stdDevMultiplier * stdDev

    const t = data[i].time
    middle.push({ time: t, value: Number(sma.toFixed(6)) })
    upper.push({ time: t, value: Number(u.toFixed(6)) })
    lower.push({ time: t, value: Number(l.toFixed(6)) })
  }

  return { upper, middle, lower }
}
