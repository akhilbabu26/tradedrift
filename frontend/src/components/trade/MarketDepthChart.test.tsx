import { describe, it, expect, vi } from 'vitest'
import { renderToString } from 'react-dom/server'
import MarketDepthChart from './MarketDepthChart'
import OrderBook from './OrderBook'
import type { OrderBookSnapshot } from '../../types/trade'

const mockOrderBook: OrderBookSnapshot = {
  bids: [
    { price: '64000.00', quantity: '1.5', total: '1.5', depthPct: 38 },
    { price: '63900.00', quantity: '2.0', total: '3.5', depthPct: 88 },
    { price: '63800.00', quantity: '0.5', total: '4.0', depthPct: 100 },
  ],
  asks: [
    { price: '64100.00', quantity: '1.0', total: '1.0', depthPct: 20 },
    { price: '64200.00', quantity: '2.5', total: '3.5', depthPct: 70 },
    { price: '64300.00', quantity: '1.5', total: '5.0', depthPct: 100 },
  ],
  lastPrice: '64050.00',
  spread: '100.00',
  spreadPercent: '0.16',
}

describe('MarketDepthChart Component', () => {
  it('renders empty state when order book has no bids and no asks', () => {
    const emptyBook: OrderBookSnapshot = {
      bids: [],
      asks: [],
      lastPrice: '0',
      spread: '0',
      spreadPercent: '0',
    }
    const html = renderToString(
      <MarketDepthChart
        orderBook={emptyBook}
        baseAsset="BTC"
        quoteAsset="USDT"
        lastPrice="0"
      />
    )
    expect(html).toContain('Waiting for market depth data')
  })

  it('renders depth chart with sentiment ratio, mid price and SVG definitions', () => {
    const html = renderToString(
      <MarketDepthChart
        orderBook={mockOrderBook}
        baseAsset="BTC"
        quoteAsset="USDT"
        lastPrice="64050.00"
      />
    )

    // Sentiment ratio and labels
    expect(html).toContain('Bids')
    expect(html).toContain('Asks')
    expect(html).toContain('BTC')
    // SVG and gradients
    expect(html).toContain('<svg')
    expect(html).toContain('depthBidGrad')
    expect(html).toContain('depthAskGrad')
    expect(html).toContain('#10b981')
    expect(html).toContain('#ef4444')
  })
})

describe('OrderBook with MarketDepthChart Integration', () => {
  it('renders Order Book by default with tab controls and precision control', () => {
    const html = renderToString(
      <OrderBook
        orderBook={mockOrderBook}
        isDemoData={false}
        precision={2}
        onPrecisionChange={vi.fn()}
        lastPrice="64050.00"
        baseAsset="BTC"
        quoteAsset="USDT"
      />
    )

    expect(html).toContain('Order Book')
    expect(html).toContain('Market Depth')
    expect(html).toContain('Price')
    expect(html).toContain('Amount')
    expect(html).toContain('64,050.00')
  })
})
