import { describe, it, expect } from 'vitest'
import { renderToString } from 'react-dom/server'
import AssetAllocation from './AssetAllocation'
import type { AssetAllocationItem } from '../../types/portfolio'

describe('AssetAllocation Component', () => {
  const mockAllocation: AssetAllocationItem[] = [
    {
      asset: 'BTC',
      name: 'Bitcoin',
      percentage: '65.5',
      valueUSDT: '65500.00',
      hexColor: '#f7931a',
      exposureType: 'crypto',
    },
    {
      asset: 'USDT',
      name: 'Tether',
      percentage: '34.5',
      valueUSDT: '34500.00',
      hexColor: '#26a17b',
      exposureType: 'cash',
    },
  ]

  it('renders title, exposure tags, and center total', () => {
    const html = renderToString(
      <AssetAllocation allocation={mockAllocation} totalValue="100000" />
    )
    expect(html).toContain('Asset Allocation')
    expect(html).toContain('Crypto Exposure:')
    expect(html).toContain('Cash:')
    expect(html).toContain('100,000')
  })

  it('renders horizontal scrolling container with trade-scrollbar-x and min-w content', () => {
    const html = renderToString(
      <AssetAllocation allocation={mockAllocation} totalValue="100000" />
    )
    expect(html).toContain('trade-scrollbar-x')
    expect(html).toContain('overflow-x-auto')
    expect(html).toContain('min-w-[440px]')
    expect(html).toContain('aria-label="Asset allocation chart and breakdown"')
    expect(html).toContain('BTC')
    expect(html).toContain('USDT')
    expect(html).toContain('65.5%')
    expect(html).toContain('34.5%')
  })

  it('renders empty state when no items are passed', () => {
    const html = renderToString(
      <AssetAllocation allocation={[]} totalValue="0" />
    )
    expect(html).toContain('No holdings to display.')
  })
})
