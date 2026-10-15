import { describe, it, expect, vi } from 'vitest'
import { renderToString } from 'react-dom/server'
import ChartToolbar from './ChartToolbar'
import { DEFAULT_ACTIVE_INDICATORS } from '../../utils/indicators'

describe('ChartToolbar Component', () => {
  const defaultProps = {
    timeframe: '1h' as const,
    onTimeframeChange: vi.fn(),
    chartType: 'candlestick' as const,
    onChartTypeChange: vi.fn(),
    isDemoData: false,
    activeIndicators: DEFAULT_ACTIVE_INDICATORS,
    onToggleIndicator: vi.fn(),
  }

  it('renders timeframe buttons with active state', () => {
    const html = renderToString(<ChartToolbar {...defaultProps} />)
    expect(html).toContain('1H')
    expect(html).toContain('1m')
    expect(html).toContain('1D')
    expect(html).toContain('aria-pressed="true"')
  })

  it('renders Indicators button', () => {
    const html = renderToString(<ChartToolbar {...defaultProps} />)
    expect(html).toContain('Indicators')
    expect(html).toContain('aria-label="Toggle indicators menu"')
    expect(html).toContain('aria-expanded="false"')
  })

  it('displays active badge counter when indicators are enabled', () => {
    const activeIndicators = {
      ema20: true,
      ema50: true,
      ema200: false,
      bollinger: false,
    }
    const html = renderToString(<ChartToolbar {...defaultProps} activeIndicators={activeIndicators} />)
    expect(html).toContain('Indicators')
    expect(html).toContain('bg-[#10b981]')
    expect(html).toContain('>2</span>')
  })

  it('renders screenshot camera button and fullscreen button with accessible labels', () => {
    const onScreenshot = vi.fn()
    const onFullscreen = vi.fn()
    const html = renderToString(
      <ChartToolbar
        {...defaultProps}
        onScreenshot={onScreenshot}
        onFullscreen={onFullscreen}
      />
    )
    expect(html).toContain('aria-label="Save chart as image"')
    expect(html).toContain('title="Screenshot"')
    expect(html).toContain('aria-label="Toggle fullscreen chart"')
    expect(html).toContain('title="Fullscreen"')
  })
})
