import { describe, it, expect } from 'vitest'
import { renderToString } from 'react-dom/server'
import Sparkline from './Sparkline'
import { evaluateTrendCoverage } from '../../../utils/trendCoverage'

describe('Sparkline Component', () => {
  it('renders an empty state placeholder gracefully when data is empty', () => {
    const html = renderToString(<Sparkline data={[]} width={120} height={34} />)
    expect(html).toContain('width="120"')
    expect(html).toContain('height="34"')
    expect(html).toContain('stroke-dasharray="3 3"')
    expect(html).toContain('stroke="#94a3b8"')
    expect(html).toContain('role="img"')
    expect(html).toContain('aria-label="No trend data available"')
  })

  it('renders a green upward trend when last price > first price', () => {
    const data = [82730.0, 82750.5, 82820.0]
    const html = renderToString(<Sparkline data={data} width={120} height={34} />)
    expect(html).toContain('stroke="#10b981"')
    expect(html).toContain('stop-color="#10b981"')
    expect(html).toContain('aria-label="Trend sparkline: positive (3 observations)"')
  })

  it('renders a red downward trend when last price < first price', () => {
    const data = [82820.0, 82750.5, 82730.0]
    const html = renderToString(<Sparkline data={data} width={120} height={34} />)
    expect(html).toContain('stroke="#ef4444"')
    expect(html).toContain('stop-color="#ef4444"')
    expect(html).toContain('aria-label="Trend sparkline: negative (3 observations)"')
  })

  it('renders neutral styling when all prices are equal (flat market)', () => {
    const data = [100.0, 100.0, 100.0]
    const html = renderToString(<Sparkline data={data} width={120} height={34} />)
    expect(html).toContain('stroke="#94a3b8"')
    expect(html).toContain('aria-label="Trend sparkline: neutral (3 observations)"')
  })

  it('renders a neutral line when only 1 valid point exists', () => {
    const data = [82750.0]
    const html = renderToString(<Sparkline data={data} width={120} height={34} />)
    expect(html).toContain('stroke="#94a3b8"')
    expect(html).toContain('aria-label="Trend sparkline: neutral (1 observations)"')
  })

  it('safely filters invalid, non-finite, and zero values', () => {
    const dirtyData = [NaN, 100, -50, Infinity, 'invalid' as any, 120]
    const html = renderToString(<Sparkline data={dirtyData} width={120} height={34} />)
    // 100 to 120 -> upward -> green
    expect(html).toContain('stroke="#10b981"')
    expect(html).toContain('aria-label="Trend sparkline: positive (2 observations)"')
  })

  it('honors explicit positive prop override', () => {
    const data = [100, 120] // intrinsically upward
    const html = renderToString(<Sparkline data={data} positive={false} width={120} height={34} />)
    // Override says positive=false -> red
    expect(html).toContain('stroke="#ef4444"')
  })

  it('includes custom accessible label when provided', () => {
    const html = renderToString(
      <Sparkline data={[100, 120]} ariaLabel="BTC/USDT 24h trend" />
    )
    expect(html).toContain('aria-label="BTC/USDT 24h trend"')
    expect(html).toContain('role="img"')
  })

  it('prevents micro-movements from hitting SVG top and bottom boundaries (proportional scaling)', () => {
    // 0.26% micro-movement on BTC: 82732.84 to 82950.98 in a 34px container (innerH=28, padY=3, padX=2)
    const btcPoints = [82765.73, 82732.84, 82950.98, 82864.76]
    const html = renderToString(<Sparkline data={btcPoints} width={120} height={34} />)

    // Extract the line path (fill="none")
    const match = html.match(/<path d="([^"]+)" fill="none"/)
    expect(match).toBeTruthy()
    const path = match![1]

    // Verify all Y coordinates stay comfortably within vertical center (between ~14px and ~20px)
    const yValues = path
      .replace(/[MLZ]/g, '')
      .trim()
      .split(/\s+/)
      .filter((_, idx) => idx % 2 === 1)
      .map(parseFloat)

    expect(yValues).toHaveLength(4)
    yValues.forEach((y) => {
      // Must not touch ceiling (3) or floor (31)
      expect(y).toBeGreaterThan(10)
      expect(y).toBeLessThan(25)
    })
  })

  it('generates distinct SVG path coordinates for BTC, ETH, and SOL reflecting their genuine price histories', () => {
    const btcData = [82765.73, 82732.84, 82950.98, 82864.76] // +0.24% with dip at point 1
    const ethData = [2495.05, 2496.23, 2508.87, 2507.58]     // +0.55% with rise at point 1
    const solData = [109.687, 109.797, 110.541, 110.370]     // +0.61% with steeper rise

    const btcHtml = renderToString(<Sparkline data={btcData} width={120} height={34} />)
    const ethHtml = renderToString(<Sparkline data={ethData} width={120} height={34} />)
    const solHtml = renderToString(<Sparkline data={solData} width={120} height={34} />)

    const getLinePath = (html: string) => html.match(/<path d="([^"]+)" fill="none"/)![1]
    const btcPath = getLinePath(btcHtml)
    const ethPath = getLinePath(ethHtml)
    const solPath = getLinePath(solHtml)

    // Paths must not be identical
    expect(btcPath).not.toBe(ethPath)
    expect(ethPath).not.toBe(solPath)
    expect(btcPath).not.toBe(solPath)
  })

  it('maps points chronologically across the width of the sparkline', () => {
    const points = [100, 105, 102, 110]
    const html = renderToString(<Sparkline data={points} width={120} height={34} />)
    const match = html.match(/<path d="([^"]+)" fill="none"/)
    expect(match).toBeTruthy()
    const coords = match![1]
      .replace(/[MLZ]/g, '')
      .trim()
      .split(/\s+/)

    const xValues = coords.filter((_, idx) => idx % 2 === 0).map(parseFloat)
    expect(xValues).toHaveLength(4)
    // Points must start at padX (2) and end at width - padX (118) in strictly increasing order
    expect(xValues[0]).toBe(2)
    expect(xValues[xValues.length - 1]).toBe(118)
    for (let i = 1; i < xValues.length; i++) {
      expect(xValues[i]).toBeGreaterThan(xValues[i - 1])
    }
  })
})

describe('Trend Coverage Evaluation', () => {
  it('marks >= 2 points as sufficient for sparklines', () => {
    const res = evaluateTrendCoverage(3)
    expect(res.isSufficient).toBe(true)
    expect(res.hoursCovered).toBe(3)
    expect(res.status).toBe('provisional')
  })

  it('marks < 2 points as accumulating data', () => {
    const res0 = evaluateTrendCoverage(0)
    expect(res0.isSufficient).toBe(false)
    expect(res0.status).toBe('accumulating')

    const res1 = evaluateTrendCoverage(1)
    expect(res1.isSufficient).toBe(false)
    expect(res1.status).toBe('accumulating')
  })

  it('correctly classifies multi-day coverage', () => {
    const res24 = evaluateTrendCoverage(24)
    expect(res24.status).toBe('partial')
    expect(res24.isSufficient).toBe(true)

    const res168 = evaluateTrendCoverage(168)
    expect(res168.status).toBe('full')
    expect(res168.label).toBe('7D Trend')
  })
})
