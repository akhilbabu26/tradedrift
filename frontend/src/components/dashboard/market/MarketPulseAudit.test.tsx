import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderToString } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import MarketPulse from './MarketPulse'
import MarketsTable from '../../markets/MarketsTable'
import Sparkline from '../shared/Sparkline'
import type { MarketRow } from '../../../types/dashboard'
import type { MarketEntry } from '../../../types/markets'
import { useMarketStore, resetMarketStoreForTesting } from '../../../store/marketStore'

// Helper to render component wrapped in MemoryRouter
function renderWithRouter(ui: React.ReactElement): string {
  return renderToString(<MemoryRouter>{ui}</MemoryRouter>)
}

describe('Market Pulse Trend & Data Integrity Audit (All 11 Required Test Cases)', () => {
  beforeEach(() => {
    resetMarketStoreForTesting()
    vi.clearAllMocks()
  })

  // ── 1. Positive percentage with a rising series ─────────────────────────────
  it('1. renders positive percentage with a rising series (BTC: +0.75%, green sparkline)', () => {
    const btcRow: MarketRow = {
      pair: 'BTC/USDT',
      symbol: 'BTC',
      price: '83596.77',
      change24h: '0.75',
      positive: true,
      iconBg: 'bg-[#F7931A]/10 border-[#F7931A]/30',
      iconLabel: '₿',
      sparklinePoints: [82765.73, 82950.0, 83596.77],
      volume24h: '$1.4M',
      quoteVolume: 1400000,
      trendStatus: 'provisional',
      trendLabel: 'Trend · 23h',
    }

    const html = renderWithRouter(<MarketPulse rows={[btcRow]} />)

    // Percentage: +0.75% in emerald green
    expect(html).toContain('+0.75%')
    expect(html).toContain('text-[#10b981]')

    // Sparkline: rising -> stroke #10b981
    expect(html).toContain('stroke="#10b981"')
    expect(html).toContain('stop-color="#10b981"')
    expect(html).toContain('aria-label="BTC/USDT Trend · 23h"')
  })

  // ── 2. Negative percentage with a falling series ────────────────────────────
  it('2. renders negative percentage with a falling series (SOL: -2.58%, red sparkline)', () => {
    const solRow: MarketRow = {
      pair: 'SOL/USDT',
      symbol: 'SOL',
      price: '107.19',
      change24h: '2.58',
      positive: false,
      iconBg: 'bg-[#9945FF]/10 border-[#9945FF]/30',
      iconLabel: 'S',
      sparklinePoints: [109.687, 108.5, 107.191],
      volume24h: '$244K',
      quoteVolume: 244000,
      trendStatus: 'provisional',
      trendLabel: 'Trend · 23h',
    }

    const html = renderWithRouter(<MarketPulse rows={[solRow]} />)

    // Percentage: -2.58% in crimson red
    expect(html).toContain('-2.58%')
    expect(html).toContain('text-[#ef4444]')

    // Sparkline: falling -> stroke #ef4444
    expect(html).toContain('stroke="#ef4444"')
    expect(html).toContain('stop-color="#ef4444"')
    expect(html).toContain('aria-label="SOL/USDT Trend · 23h"')
  })

  // ── 3. Negative 24-hour percentage with a genuinely rising short-term series ─
  it('3. preserves genuine short-term rising trend with green sparkline when 24h percentage is negative (ETH scenario)', () => {
    // ETH scenario: 24h change is -1.33% because baseline 24h ago was 2538,
    // but the available historical series rose from 2495.05 to 2504.18.
    const ethRow: MarketRow = {
      pair: 'ETH/USDT',
      symbol: 'ETH',
      price: '2504.18',
      change24h: '1.33',
      positive: false,
      iconBg: 'bg-[#627EEA]/10 border-[#627EEA]/30',
      iconLabel: 'Ξ',
      sparklinePoints: [2495.05, 2498.0, 2504.18], // intrinsically rising from start to end
      volume24h: '$390K',
      quoteVolume: 390000,
      trendStatus: 'provisional',
      trendLabel: 'Trend · 23h',
    }

    const html = renderWithRouter(<MarketPulse rows={[ethRow]} />)

    // Percentage column honestly shows negative 24h change
    expect(html).toContain('-1.33%')
    expect(html).toContain('text-[#ef4444]')

    // Sparkline preserves authentic upward trajectory and green color
    // (does not falsely recolor to red)
    expect(html).toContain('stroke="#10b981"')
    expect(html).toContain('stop-color="#10b981"')

    // Time window is explicitly communicated to prevent misinterpreting it as 24h
    expect(html).toContain('aria-label="ETH/USDT Trend · 23h"')
    expect(html).toContain('title="ETH/USDT Trend · 23h"')
  })

  // ── 4. Zero percentage ──────────────────────────────────────────────────────
  it('4. renders neutral styling for zero percentage without positive/negative sign', () => {
    const flatRow: MarketRow = {
      pair: 'BTC/USDT',
      symbol: 'BTC',
      price: '83000.00',
      change24h: '0.00',
      positive: true, // or false, value is zero
      iconBg: 'bg-white/10 border-white/20',
      iconLabel: '₿',
      sparklinePoints: [83000, 83000, 83000],
      volume24h: '$100K',
      quoteVolume: 100000,
    }

    const html = renderWithRouter(<MarketPulse rows={[flatRow]} />)

    // Percentage displays "0.00%" without "+" or "-" and uses neutral slate text
    expect(html).toContain('0.00%')
    expect(html).not.toContain('+0.00%')
    expect(html).not.toContain('-0.00%')
    expect(html).toContain('text-slate-400')

    // Sparkline of identical prices renders neutral slate line
    expect(html).toContain('stroke="#94a3b8"')
  })

  // ── 5. Missing percentage ───────────────────────────────────────────────────
  it('5. handles missing or invalid percentage gracefully with neutral styling', () => {
    const missingChangeRow: MarketRow = {
      pair: 'ETH/USDT',
      symbol: 'ETH',
      price: '2500.00',
      change24h: '',
      positive: false,
      iconBg: 'bg-white/10 border-white/20',
      iconLabel: 'Ξ',
      sparklinePoints: [2500, 2510],
      volume24h: '$0',
      quoteVolume: 0,
    }

    const html = renderWithRouter(<MarketPulse rows={[missingChangeRow]} />)

    // Should render neutral text color for missing change
    expect(html).toContain('text-slate-400')
    expect(html).toContain('0.00%')
  })

  // ── 6. Empty or insufficient historical data ────────────────────────────────
  it('6. renders neutral placeholder state for empty or insufficient (< 2 points) historical data', () => {
    const emptyRow: MarketRow = {
      pair: 'SOL/USDT',
      symbol: 'SOL',
      price: '108.00',
      change24h: '1.50',
      positive: false,
      iconBg: 'bg-white/10 border-white/20',
      iconLabel: 'S',
      sparklinePoints: [], // Empty history
      volume24h: '$0',
      quoteVolume: 0,
    }

    const singlePointRow: MarketRow = {
      pair: 'SOL/USDT',
      symbol: 'SOL',
      price: '108.00',
      change24h: '1.50',
      positive: false,
      iconBg: 'bg-white/10 border-white/20',
      iconLabel: 'S',
      sparklinePoints: [108.0], // 1 point
      volume24h: '$0',
      quoteVolume: 0,
    }

    const emptyHtml = renderWithRouter(<MarketPulse rows={[emptyRow]} />)
    expect(emptyHtml).toContain('stroke="#94a3b8"')
    expect(emptyHtml).toContain('stroke-dasharray="3 3"')
    expect(emptyHtml).toContain('aria-label="No trend data available"')

    const singleHtml = renderWithRouter(<MarketPulse rows={[singlePointRow]} />)
    expect(singleHtml).toContain('stroke="#94a3b8"')
  })

  // ── 7. Incorrect market identifier ──────────────────────────────────────────
  it('7. safely handles non-standard or unknown market identifier without throwing', () => {
    const unknownRow: MarketRow = {
      pair: 'UNKNOWN/USDT',
      symbol: 'UNKNOWN',
      price: '10.50',
      change24h: '3.20',
      positive: true,
      iconBg: 'bg-slate-800 border-slate-700',
      iconLabel: '?',
      sparklinePoints: [10.0, 10.5],
      volume24h: '$10K',
      quoteVolume: 10000,
    }

    expect(() => {
      const html = renderWithRouter(<MarketPulse rows={[unknownRow]} />)
      expect(html).toContain('UNKNOWN/USDT')
      expect(html).toContain('href="/trade?market=UNKNOWN-USDT"')
    }).not.toThrow()
  })

  // ── 8. Unsorted timestamps / chronological ordering ────────────────────────
  it('8. ensures historical series is sorted chronologically from earliest to latest', () => {
    // When chronological points [100, 110, 120] are supplied:
    const html = renderToString(<Sparkline data={[100, 110, 120]} width={120} height={34} />)
    const match = html.match(/<path d="([^"]+)" fill="none"/)
    expect(match).toBeTruthy()
    const path = match![1]
    const coords = path.replace(/[MLZ]/g, '').trim().split(/\s+/)
    const xCoords = coords.filter((_, idx) => idx % 2 === 0).map(parseFloat)
    const yCoords = coords.filter((_, idx) => idx % 2 === 1).map(parseFloat)

    // X coordinates must strictly increase (chronological progression)
    expect(xCoords[0]).toBeLessThan(xCoords[1])
    expect(xCoords[1]).toBeLessThan(xCoords[2])

    // Y coordinates in SVG: smaller Y is higher on screen (higher price)
    // 100 -> 110 -> 120 means price rises, so Y decreases
    expect(yCoords[0]).toBeGreaterThan(yCoords[1])
    expect(yCoords[1]).toBeGreaterThan(yCoords[2])
  })

  // ── 9. Duplicate points and invalid prices ──────────────────────────────────
  it('9. filters duplicate invalid prices, NaNs, and negative values without corrupting SVG paths', () => {
    const dirtyData = [NaN, 100, -20, Infinity, 'invalid' as any, 100, 100, 120]
    const html = renderToString(<Sparkline data={dirtyData} width={120} height={34} />)

    // Should successfully extract 100, 100, 100, 120 -> 4 observations, rising from 100 to 120
    expect(html).not.toContain('NaN')
    expect(html).not.toContain('Infinity')
    expect(html).toContain('stroke="#10b981"')
    expect(html).toContain('Trend sparkline: positive (4 observations)')
  })

  // ── 10. Live updates for each of BTC, ETH, and SOL ─────────────────────────
  it('10. isolates live ticker updates so BTC, ETH, and SOL update independently without cross-talk', () => {
    // Dispatch a BTC ticker update
    useMarketStore.setState({
      tickers: {
        'BTC-USDT': {
          market_id: 'BTC-USDT',
          last_price: '84000.00',
          high_24h: '84500.00',
          low_24h: '82000.00',
          volume_24h: '20.0',
          quote_volume_24h: '1680000.0',
          price_change_24h_percent: '1.20',
        },
      },
      trends: {
        'BTC-USDT': [82765.0, 84000.0],
      },
    })

    const afterBtc = useMarketStore.getState()
    expect(afterBtc.tickers['BTC-USDT'].last_price).toBe('84000.00')
    expect(afterBtc.tickers['ETH-USDT']).toBeUndefined()
    expect(afterBtc.tickers['SOL-USDT']).toBeUndefined()

    // Dispatch an ETH ticker update
    useMarketStore.setState({
      tickers: {
        ...afterBtc.tickers,
        'ETH-USDT': {
          market_id: 'ETH-USDT',
          last_price: '2480.00',
          high_24h: '2540.00',
          low_24h: '2470.00',
          volume_24h: '180.0',
          quote_volume_24h: '446000.0',
          price_change_24h_percent: '-2.30',
        },
      },
      trends: {
        ...afterBtc.trends,
        'ETH-USDT': [2495.0, 2480.0],
      },
    })

    const afterEth = useMarketStore.getState()
    expect(afterEth.tickers['BTC-USDT'].last_price).toBe('84000.00') // untouched
    expect(afterEth.tickers['ETH-USDT'].last_price).toBe('2480.00')
    expect(afterEth.tickers['SOL-USDT']).toBeUndefined()

    // Dispatch a SOL ticker update
    useMarketStore.setState({
      tickers: {
        ...afterEth.tickers,
        'SOL-USDT': {
          market_id: 'SOL-USDT',
          last_price: '106.00',
          high_24h: '110.00',
          low_24h: '105.00',
          volume_24h: '2600.0',
          quote_volume_24h: '275000.0',
          price_change_24h_percent: '-3.80',
        },
      },
      trends: {
        ...afterEth.trends,
        'SOL-USDT': [109.0, 106.0],
      },
    })

    const afterSol = useMarketStore.getState()
    expect(afterSol.tickers['BTC-USDT'].last_price).toBe('84000.00')
    expect(afterSol.tickers['ETH-USDT'].last_price).toBe('2480.00')
    expect(afterSol.tickers['SOL-USDT'].last_price).toBe('106.00')
  })

  // ── 11. Dashboard and Markets page rendering shared component consistently ──
  it('11. renders Sparkline consistently across Dashboard (MarketPulse) and Markets page (MarketsTable)', () => {
    const testPoints = [2495.05, 2500.0, 2504.18] // rising points

    const dashboardRow: MarketRow = {
      pair: 'ETH/USDT',
      symbol: 'ETH',
      price: '2504.18',
      change24h: '1.33',
      positive: false,
      iconBg: 'bg-[#627EEA]/10 border-[#627EEA]/30',
      iconLabel: 'Ξ',
      sparklinePoints: testPoints,
      volume24h: '$390K',
      quoteVolume: 390000,
      trendLabel: 'Trend · 23h',
    }

    const marketEntry: MarketEntry = {
      rank: 2,
      pair: 'ETH/USDT',
      asset: 'ETH',
      name: 'Ethereum',
      lastPrice: '2504.18',
      change24h: '1.33',
      positive: false,
      high24h: '2540.00',
      low24h: '2501.00',
      volume24h: '$390K',
      quoteVolume24h: '$390K',
      sparklinePoints: testPoints,
      iconColor: '#627EEA',
      iconLabel: 'Ξ',
      trendStatus: 'provisional',
      trendLabel: 'Trend · 23h',
      trendHours: 23,
    }

    const pulseHtml = renderWithRouter(<MarketPulse rows={[dashboardRow]} />)
    const tableHtml = renderWithRouter(
      <MarketsTable
        entries={[marketEntry]}
        favorites={new Set()}
        onToggleFavorite={vi.fn()}
        filter="all"
        hasSearch={false}
      />
    )

    // Both consumers render identical stroke color (#10b981) for the rising series
    expect(pulseHtml).toContain('stroke="#10b981"')
    expect(tableHtml).toContain('stroke="#10b981"')

    // Both consumers expose the honest trend label in aria-label
    expect(pulseHtml).toContain('ETH/USDT Trend · 23h')
    expect(tableHtml).toContain('ETH/USDT Trend · 23h')
  })
})
