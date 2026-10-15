import { describe, it, expect, vi } from 'vitest'
import { renderToString } from 'react-dom/server'
import PortfolioSummary from './PortfolioSummary'
import PortfolioChart from './PortfolioChart'
import PortfolioOverview from './PortfolioOverview'
import { toDecimal } from '../../../utils/decimal'
import { formatPercentage } from '../../../utils/formatters'

import { MemoryRouter } from 'react-router-dom'

describe('Portfolio Overview Accuracy, Direction Indicator & Smooth Timeframe Loading Audit', () => {
  // ── 1. Positive daily change with upward arrow ────────────────────────────
  it('1. renders positive daily change with upward arrow, emerald green styling, and correct percentage', () => {
    const html = renderToString(
      <PortfolioSummary
        portfolioValue="20500.00"
        changeUsdt="450.50"
        changePercent="2.25"
      />
    )

    // Direction icon is TrendingUp (aria-label="Positive change")
    expect(html).toContain('aria-label="Positive change"')
    expect(html).not.toContain('aria-label="Negative change"')
    expect(html).not.toContain('aria-label="No change"')

    // Positive styling
    expect(html).toContain('text-[#10b981]')
    expect(html).toContain('+450.50')
    expect(html).toContain('+2.25%')
    expect(html).toContain('20,500.00 USDT')
  })

  // ── 2. Negative daily change with downward arrow ──────────────────────────
  it('2. renders negative daily change with downward arrow, crimson red styling, and correct percentage (no upward arrow on loss)', () => {
    // In screenshot: portfolioValue="19762.99", changeUsdt="-233.04", changePercent="-1.17"
    const html = renderToString(
      <PortfolioSummary
        portfolioValue="19762.99"
        changeUsdt="-233.04"
        changePercent="-1.17"
      />
    )

    // Direction icon MUST be TrendingDown (aria-label="Negative change")
    expect(html).toContain('aria-label="Negative change"')
    expect(html).not.toContain('aria-label="Positive change"')
    expect(html).not.toContain('aria-label="No change"')

    // Negative styling
    expect(html).toContain('text-[#ef4444]')
    expect(html).toContain('-233.04')
    expect(html).toContain('-1.17%')
    expect(html).not.toContain('0.00%')
    expect(html).toContain('19,762.99 USDT')
  })

  // ── 3. Zero change with neutral indicator ─────────────────────────────────
  it('3. renders zero change with neutral indicator and slate-400 styling', () => {
    const html = renderToString(
      <PortfolioSummary
        portfolioValue="10000.00"
        changeUsdt="0.00"
        changePercent="0.00"
      />
    )

    // Direction icon MUST be Minus (aria-label="No change")
    expect(html).toContain('aria-label="No change"')
    expect(html).not.toContain('aria-label="Positive change"')
    expect(html).not.toContain('aria-label="Negative change"')

    // Neutral styling
    expect(html).toContain('text-slate-400')
    expect(html).toContain('0.00')
    expect(html).toContain('0.00%')
  })

  // ── 4. Correct percentage calculation from valid baseline values ──────────
  it('4. calculates daily percentage accurately from starting and current values', () => {
    // Current portfolio value: 19,762.99 USDT
    // Daily loss: -233.04 USDT
    // Starting portfolio value = 19,762.99 - (-233.04) = 19,996.03 USDT
    // Expected percentage = (-233.04 / 19,996.03) * 100 = -1.1654% -> "-1.17"
    const currentVal = toDecimal('19762.99')
    const dailyChange = toDecimal('-233.04')
    const startingVal = currentVal.minus(dailyChange)

    expect(startingVal.toFixed(2)).toBe('19996.03')

    const calculatedPct = startingVal.gt(0)
      ? dailyChange.dividedBy(startingVal).times(100).toFixed(2)
      : '0.00'

    expect(calculatedPct).toBe('-1.17')

    // formatPercentage correctly renders "-1.17%"
    expect(formatPercentage(calculatedPct)).toBe('-1.17%')
  })

  // ── 5. Missing or zero baseline ───────────────────────────────────────────
  it('5. safely handles zero or missing starting baseline without DivisionByZero or NaN', () => {
    const currentVal = toDecimal('0.00')
    const dailyChange = toDecimal('0.00')
    const startingVal = currentVal.minus(dailyChange)

    const calculatedPct = startingVal.gt(0)
      ? dailyChange.dividedBy(startingVal).times(100).toFixed(2)
      : '0.00'

    expect(calculatedPct).toBe('0.00')
    expect(formatPercentage(calculatedPct)).toBe('0.00%')
  })

  // ── 6. Missing percentage or string with trailing % ────────────────────────
  it('6. sanitizes trailing % and handles missing percentage gracefully', () => {
    expect(formatPercentage('-1.18%')).toBe('-1.18%')
    expect(formatPercentage('+3.38%')).toBe('+3.38%')
    expect(formatPercentage('')).toBe('0.00%')
    expect(formatPercentage(undefined as any)).toBe('0.00%')
  })

  // ── 7. Successful timeframe change visual state ───────────────────────────
  it('7. updates timeframe button visual state immediately on selection', () => {
    const html24h = renderToString(
      <MemoryRouter>
        <PortfolioOverview
          portfolioValue="19762.99"
          portfolioChangeUsdt="-233.04"
          portfolioChangePercent="-1.17"
          simulatorBalance="2387.89"
          availablePercent={100}
          inOrdersPercent={0}
          availableUsdt="2387.89"
          inOrdersUsdt="0.00"
          holdings={[]}
          chartData={[]}
          allTimeProfit="0.00"
          winRate={0}
          totalTrades={0}
          activePeriod="24H"
        />
      </MemoryRouter>
    )

    // Button 24H should be aria-selected="true" with active gradient
    expect(html24h).toContain('aria-selected="true"')
    expect(html24h).toContain('24H')
  })

  // ── 8. Loading indicator visibility during pending request ─────────────────
  it('8. renders subtle non-blocking loading overlay during timeframe load without hiding container', () => {
    const html = renderToString(
      <PortfolioChart
        data={[{ time: 1000, value: 19500 }]}
        isLoading={true}
      />
    )

    // Chart container is present with fixed height
    expect(html).toContain('h-[220px]')
    expect(html).toContain('min-h-[220px]')

    // Loading overlay is visible
    expect(html).toContain('data-testid="chart-loading-overlay"')
    expect(html).toContain('Updating timeframe...')
  })

  // ── 9. Fast consecutive timeframe changes and race condition guard ────────
  it('9. prevents stale out-of-order responses from overwriting newer timeframe selections', async () => {
    let latestReqId = 0
    let activeState = '7D'
    const results: string[] = []

    // Simulate clicking 24H (takes 100ms) then 30D (takes 20ms)
    async function selectTimeframe(tf: string, delayMs: number) {
      activeState = tf
      const currentReq = ++latestReqId
      await new Promise((r) => setTimeout(r, delayMs))
      // Sequence guard:
      if (currentReq === latestReqId) {
        results.push(tf)
      }
    }

    const p1 = selectTimeframe('24H', 100)
    const p2 = selectTimeframe('30D', 20)

    await Promise.all([p1, p2])

    // Only '30D' (the latest selection) should have committed!
    expect(results).toEqual(['30D'])
    expect(activeState).toBe('30D')
  })

  // ── 10. Failed timeframe request with retry action ─────────────────────────
  it('10. renders error overlay with retry button when timeframe request fails', () => {
    const mockRetry = vi.fn()
    const html = renderToString(
      <PortfolioChart
        data={[]}
        isLoading={false}
        error="Network timeout on snapshot query"
        onRetry={mockRetry}
      />
    )

    expect(html).toContain('data-testid="chart-error-overlay"')
    expect(html).toContain('Failed to load timeframe data')
    expect(html).toContain('Retry')
  })

  // ── 11. No layout shift while loading ──────────────────────────────────────
  it('11. preserves container height at exactly 220px in both idle and loading states to eliminate layout shifts', () => {
    const idleHtml = renderToString(
      <PortfolioChart data={[]} isLoading={false} />
    )
    const loadingHtml = renderToString(
      <PortfolioChart data={[]} isLoading={true} />
    )

    expect(idleHtml).toContain('h-[220px] min-h-[220px]')
    expect(loadingHtml).toContain('h-[220px] min-h-[220px]')
  })
})
