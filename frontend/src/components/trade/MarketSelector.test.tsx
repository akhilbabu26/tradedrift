import { describe, it, expect, vi } from 'vitest'
import { renderToString } from 'react-dom/server'
import MarketSelector from './MarketSelector'
import type { Market } from '../../types/trade'

describe('MarketSelector Component', () => {
  const mockMarkets: Market[] = [
    {
      id: 'BTC-USDT',
      base_asset: 'BTC',
      quote_asset: 'USDT',
      tick_size: '0.01',
      lot_size: '0.0001',
      status: 'ACTIVE',
      min_quantity: '0.0001',
      created_at: '',
      updated_at: '',
    },
    {
      id: 'ETH-USDT',
      base_asset: 'ETH',
      quote_asset: 'USDT',
      tick_size: '0.01',
      lot_size: '0.001',
      status: 'ACTIVE',
      min_quantity: '0.001',
      created_at: '',
      updated_at: '',
    },
  ]

  const mockTrends: Record<string, number[]> = {
    'BTC-USDT': [96000, 96200, 96450],
    'ETH-USDT': [2800, 2790, 2780],
  }

  it('renders closed trigger by default with selected pair name', () => {
    const html = renderToString(
      <MarketSelector
        markets={mockMarkets}
        selectedMarketId="BTC-USDT"
        onSelect={vi.fn()}
      />
    )
    expect(html).toContain('id="market-selector-trigger"')
    expect(html).toContain('BTC/USDT')
    expect(html).not.toContain('role="listbox"')
  })

  it('renders dropdown with sparklines for each market when open', () => {
    const html = renderToString(
      <MarketSelector
        markets={mockMarkets}
        selectedMarketId="BTC-USDT"
        onSelect={vi.fn()}
        marketTrends={mockTrends}
        defaultOpen={true}
      />
    )
    expect(html).toContain('role="listbox"')
    expect(html).toContain('aria-label="Market selection"')
    // BTC Sparkline (upward -> positive)
    expect(html).toContain('aria-label="BTC/USDT 24h trend"')
    expect(html).toContain('width="64"')
    expect(html).toContain('height="22"')
    // ETH Sparkline (downward -> negative)
    expect(html).toContain('aria-label="ETH/USDT 24h trend"')
  })

  it('renders placeholder sparklines when market trend data is not yet available', () => {
    const html = renderToString(
      <MarketSelector
        markets={mockMarkets}
        selectedMarketId="BTC-USDT"
        onSelect={vi.fn()}
        marketTrends={{}}
        defaultOpen={true}
      />
    )
    expect(html).toContain('role="listbox"')
    expect(html).toContain('aria-label="BTC/USDT 24h trend"')
    expect(html).toContain('stroke-dasharray="3 3"')
  })
})
