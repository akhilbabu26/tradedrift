import { describe, it, expect } from 'vitest'
import { renderToString } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import HoldingsTable from './HoldingsTable'
import { getAssetMetadata } from '../../utils/marketMetadata'
import type { PortfolioHolding } from '../../types/portfolio'

describe('HoldingsTable Component', () => {
  const mockHoldings: PortfolioHolding[] = [
    {
      asset: 'BTC',
      name: 'Bitcoin',
      meta: getAssetMetadata('BTC'),
      quantity: '0.0200',
      averageCost: '95000.00',
      currentPrice: '96450.00',
      marketValue: '1929.00',
      unrealizedPnl: '+29.00',
      unrealizedPnlPct: '+1.53',
      realizedPnl: '+0.00',
      weightPct: '19.3',
      sparklinePoints: [95000, 95500, 96000, 96450],
    },
    {
      asset: 'USDT',
      name: 'Tether USD',
      meta: getAssetMetadata('USDT'),
      quantity: '8069.47',
      averageCost: '1.00',
      currentPrice: '1.00',
      marketValue: '8069.47',
      unrealizedPnl: '0.00',
      unrealizedPnlPct: '+0.00',
      realizedPnl: '+0.00',
      weightPct: '80.7',
      sparklinePoints: undefined, // No market pair exists for USDT
    },
  ]

  it('renders the 24h Trend column header', () => {
    const html = renderToString(
      <MemoryRouter>
        <HoldingsTable holdings={mockHoldings} />
      </MemoryRouter>
    )
    expect(html).toContain('24h Trend')
  })

  it('renders a genuine Sparkline for supported crypto asset with price history', () => {
    const html = renderToString(
      <MemoryRouter>
        <HoldingsTable holdings={mockHoldings} />
      </MemoryRouter>
    )
    expect(html).toContain('aria-label="BTC 24h price trend"')
    expect(html).toContain('width="80"')
    expect(html).toContain('height="26"')
  })

  it('renders a graceful dash placeholder for unmarketed assets like USDT without fabricating fake curves', () => {
    const html = renderToString(
      <MemoryRouter>
        <HoldingsTable holdings={mockHoldings} />
      </MemoryRouter>
    )
    expect(html).toContain('title="No market pair"')
    expect(html).toContain('—')
  })

  it('renders an empty state when no holdings are passed', () => {
    const html = renderToString(
      <MemoryRouter>
        <HoldingsTable holdings={[]} />
      </MemoryRouter>
    )
    expect(html).toContain('No holdings match your current filters.')
  })

  it('renders horizontal scrolling container with trade-scrollbar-x and min-w table', () => {
    const html = renderToString(
      <MemoryRouter>
        <HoldingsTable holdings={mockHoldings} />
      </MemoryRouter>
    )
    expect(html).toContain('trade-scrollbar-x')
    expect(html).toContain('overflow-x-auto')
    expect(html).toContain('min-w-[920px]')
    expect(html).toContain('aria-label="Portfolio holdings table"')
    expect(html).toContain('whitespace-nowrap')
  })
})
