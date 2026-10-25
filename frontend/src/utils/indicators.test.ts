import { describe, it, expect } from 'vitest'
import {
  calculateEMA,
  calculateBollingerBands,
  calculateIncrementalEMA,
  calculateIncrementalBollingerBands,
} from './indicators'
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

  describe('calculateIncrementalEMA', () => {
    it('produces identical result to calculateEMA for the active bar', () => {
      const period = 3
      const fullEma = calculateEMA(mockBars, period)
      // Bar at index 4 (time 5000) was 13
      const prevEma = fullEma[fullEma.length - 2].value // EMA of bar at index 4
      const activeBar = mockBars[mockBars.length - 1] // Bar at index 5, close 15

      const incPoint = calculateIncrementalEMA(prevEma, activeBar.close, period, activeBar.time)
      const fullLastPoint = fullEma[fullEma.length - 1]

      expect(incPoint.time).toBe(fullLastPoint.time)
      expect(incPoint.value).toBe(fullLastPoint.value)
    })
  })

  describe('calculateIncrementalBollingerBands', () => {
    it('produces identical result to calculateBollingerBands for the active bar', () => {
      const period = 3
      const fullBB = calculateBollingerBands(mockBars, period, 2)
      const precedingBars = mockBars.slice(0, -1)
      const activeBar = mockBars[mockBars.length - 1]

      const incBB = calculateIncrementalBollingerBands(precedingBars, activeBar.close, period, 2, activeBar.time)
      expect(incBB).not.toBeNull()
      const fullLastUpper = fullBB.upper[fullBB.upper.length - 1]
      const fullLastMiddle = fullBB.middle[fullBB.middle.length - 1]
      const fullLastLower = fullBB.lower[fullBB.lower.length - 1]

      expect(incBB!.upper.value).toBe(fullLastUpper.value)
      expect(incBB!.middle.value).toBe(fullLastMiddle.value)
      expect(incBB!.lower.value).toBe(fullLastLower.value)
    })

    it('returns null if fewer preceding bars than period - 1', () => {
      expect(calculateIncrementalBollingerBands([], 10, 5, 2, 1000 as UTCTimestamp)).toBeNull()
    })
  })
})
