import { describe, it, expect } from 'vitest'
import { calculateEMA, calculateBollingerBands } from './indicators'
import type { UTCTimestamp } from 'lightweight-charts'

describe('Technical Indicators Utilities', () => {
  const mockBars = [
    { time: 1000 as UTCTimestamp, close: 10 },
    { time: 2000 as UTCTimestamp, close: 11 },
    { time: 3000 as UTCTimestamp, close: 12 },
    { time: 4000 as UTCTimestamp, close: 13 },
    { time: 5000 as UTCTimestamp, close: 14 },
    { time: 6000 as UTCTimestamp, close: 15 },
  ]

  describe('calculateEMA', () => {
    it('returns empty array if data has fewer bars than period', () => {
      expect(calculateEMA(mockBars, 10)).toEqual([])
      expect(calculateEMA([], 5)).toEqual([])
    })

    it('calculates correct initial SMA and subsequent exponential weighting', () => {
      const period = 3
      const ema = calculateEMA(mockBars, period)
      // First EMA at index 2 (time 3000): (10+11+12)/3 = 11
      expect(ema[0]).toEqual({ time: 3000, value: 11 })

      // k = 2 / (3 + 1) = 0.5
      // Bar 4 (close 13): 13 * 0.5 + 11 * 0.5 = 12
      expect(ema[1]).toEqual({ time: 4000, value: 12 })

      // Bar 5 (close 14): 14 * 0.5 + 12 * 0.5 = 13
      expect(ema[2]).toEqual({ time: 5000, value: 13 })
    })
  })

  describe('calculateBollingerBands', () => {
    it('returns empty arrays if data has fewer bars than period', () => {
      const bb = calculateBollingerBands(mockBars, 10)
      expect(bb.upper).toEqual([])
      expect(bb.middle).toEqual([])
      expect(bb.lower).toEqual([])
    })

    it('calculates middle SMA and upper/lower standard deviation bands', () => {
      const period = 3
      const bb = calculateBollingerBands(mockBars, period, 2)
      expect(bb.middle.length).toBe(4) // indices 2, 3, 4, 5
      expect(bb.upper.length).toBe(4)
      expect(bb.lower.length).toBe(4)

      // First point at index 2 (values 10, 11, 12): SMA = 11
      // stdDev = sqrt(((10-11)^2 + (11-11)^2 + (12-11)^2) / 3) = sqrt(2/3) ≈ 0.816497
      // upper = 11 + 2 * 0.816497 ≈ 12.63299
      // lower = 11 - 2 * 0.816497 ≈ 9.36701
      expect(bb.middle[0].value).toBe(11)
      expect(bb.upper[0].value).toBeCloseTo(12.633, 2)
      expect(bb.lower[0].value).toBeCloseTo(9.367, 2)
    })
  })
})
