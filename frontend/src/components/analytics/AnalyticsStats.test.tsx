import { describe, it, expect } from 'vitest'
import { renderToString } from 'react-dom/server'
import AnalyticsStats from './AnalyticsStats'
import type { KpiCardItem } from '../../types/analytics'

describe('AnalyticsStats Component', () => {
  const mockKpis: KpiCardItem[] = [
    {
      id: 'net_pnl',
      label: 'Net PnL',
      value: '+$1,450.20',
      trendValue: '+100%',
      trendPeriod: 'vs prior',
      isPositive: true,
      visualType: 'sparkline',
      sparklinePoints: [100, 150, 180, 220],
    },
    {
      id: 'total_volume',
      label: 'Traded Volume',
      value: '$3,858.77',
      trendValue: '5 fills',
      trendPeriod: 'last 30 days',
      isPositive: true,
      visualType: 'bars',
    },
    {
      id: 'win_rate',
      label: 'Win Rate',
      value: '65.0%',
      trendValue: '4W / 2L',
      trendPeriod: 'closed',
      isPositive: true,
      visualType: 'sparkline',
      // Insufficient / unavailable history scenario
      sparklinePoints: undefined,
    },
  ]

  it('renders all KPI labels and values correctly', () => {
    const html = renderToString(<AnalyticsStats kpis={mockKpis} />)
    expect(html).toContain('Net PnL')
    expect(html).toContain('+$1,450.20')
    expect(html).toContain('Traded Volume')
    expect(html).toContain('$3,858.77')
    expect(html).toContain('Win Rate')
    expect(html).toContain('65.0%')
  })

  it('renders shared Sparkline for KPI with authentic historical data', () => {
    const html = renderToString(<AnalyticsStats kpis={mockKpis} />)
    // Positive sparkline has green stroke
    expect(html).toContain('stroke="#10b981"')
    expect(html).toContain('aria-label="Net PnL trend"')
  })

  it('renders neutral placeholder when sparkline points are unavailable or insufficient', () => {
    const html = renderToString(<AnalyticsStats kpis={mockKpis} />)
    // Neutral placeholder sparkline uses neutral slate stroke (#94a3b8)
    expect(html).toContain('stroke="#94a3b8"')
    expect(html).toContain('stroke-dasharray="3 3"')
    expect(html).toContain('aria-label="Win Rate trend"')
  })
})
