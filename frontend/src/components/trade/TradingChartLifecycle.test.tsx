import { describe, it, expect, vi } from 'vitest'
import { renderToString } from 'react-dom/server'
import TradingChart from './TradingChart'
import { getTimeframeSeconds, sanitizeCandles } from '../../utils/chartSanitizer'
import type { Candle } from '../../types/trade'

describe('TradingChart Lifecycle and Data Integrity Audit', () => {
  it('correctly maps timeframe buckets to epoch seconds', () => {
    expect(getTimeframeSeconds('1m')).toBe(60)
    expect(getTimeframeSeconds('5m')).toBe(300)
    expect(getTimeframeSeconds('15m')).toBe(900)
    expect(getTimeframeSeconds('1h')).toBe(3600)
    expect(getTimeframeSeconds('4h')).toBe(14400)
    expect(getTimeframeSeconds('1d')).toBe(86400)
  })

  it('sanitizes and strictly deduplicates candle timestamps to prevent chart invariant crashes', () => {
    const rawCandles: Candle[] = [
      {
        start_time: '2026-10-15T10:00:00Z',
        open: '64000.00',
        high: '64500.00',
        low: '63900.00',
        close: '64200.00',
        volume: '12.5',
        quote_volume: '802500.00',
      },
      // Duplicate timestamp — should be deduplicated
      {
        start_time: '2026-10-15T10:00:00Z',
        open: '64000.00',
        high: '64600.00',
        low: '63900.00',
        close: '64300.00',
        volume: '15.0',
        quote_volume: '964500.00',
      },
      // Newer bar
      {
        start_time: '2026-10-15T11:00:00Z',
        open: '64200.00',
        high: '64800.00',
        low: '64100.00',
        close: '64700.00',
        volume: '8.2',
        quote_volume: '530540.00',
      },
      // Invalid bar with negative price — should be filtered out
      {
        start_time: '2026-10-15T12:00:00Z',
        open: '-10.00',
        high: '64800.00',
        low: '64100.00',
        close: '64700.00',
        volume: '8.2',
        quote_volume: '0.00',
      },
    ]

    const { bars, volumes } = sanitizeCandles(rawCandles)
    expect(bars.length).toBe(2)
    expect(volumes.length).toBe(2)
    expect(bars[0].time).toBeLessThan(bars[1].time as number)
    expect(bars[0].open).toBe(64000)
    expect(bars[1].close).toBe(64700)
  })

  it('renders chart container structure without crashing', () => {
    const html = renderToString(
      <TradingChart
        marketId="BTC-USDT"
        symbol="BTC/USDT"
        lastPrice="64200.00"
        candles={[]}
        loading={false}
        timeframe="1h"
        onTimeframeChange={vi.fn()}
      />
    )
    expect(html).toContain('BTC/USDT')
    expect(html).toContain('1H')
  })
})
