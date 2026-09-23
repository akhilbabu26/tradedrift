import type { Candle, OrderBookSnapshot, Ticker24h, Market } from '../types/trade'

export const MOCK_TRADE_MARKETS: Market[] = [
  {
    id: 'BTC-USDT',
    base_asset: 'BTC',
    quote_asset: 'USDT',
    tick_size: '0.01',
    lot_size: '0.0001',
    status: 'ACTIVE',
    min_quantity: '0.0001',
    created_at: '2025-01-01T00:00:00Z',
    updated_at: '2025-01-01T00:00:00Z',
  },
  {
    id: 'ETH-USDT',
    base_asset: 'ETH',
    quote_asset: 'USDT',
    tick_size: '0.01',
    lot_size: '0.001',
    status: 'ACTIVE',
    min_quantity: '0.001',
    created_at: '2025-01-01T00:00:00Z',
    updated_at: '2025-01-01T00:00:00Z',
  },
  {
    id: 'SOL-USDT',
    base_asset: 'SOL',
    quote_asset: 'USDT',
    tick_size: '0.01',
    lot_size: '0.01',
    status: 'ACTIVE',
    min_quantity: '0.01',
    created_at: '2025-01-01T00:00:00Z',
    updated_at: '2025-01-01T00:00:00Z',
  },
]

export const MOCK_TRADE_TICKERS: Record<string, Ticker24h> = {
  'BTC-USDT': {
    market_id: 'BTC-USDT',
    last_price: '68450.00',
    high_24h: '69200.00',
    low_24h: '67150.00',
    volume_24h: '1245.82',
    quote_volume_24h: '85284000.00',
    price_change_24h_percent: '3.45',
  },
  'ETH-USDT': {
    market_id: 'ETH-USDT',
    last_price: '3520.50',
    high_24h: '3580.00',
    low_24h: '3440.00',
    volume_24h: '8420.15',
    quote_volume_24h: '29643000.00',
    price_change_24h_percent: '2.18',
  },
  'SOL-USDT': {
    market_id: 'SOL-USDT',
    last_price: '152.80',
    high_24h: '158.40',
    low_24h: '146.20',
    volume_24h: '45210.00',
    quote_volume_24h: '6908000.00',
    price_change_24h_percent: '-1.25',
  },
}

export const MOCK_ORDERBOOK_BTC: OrderBookSnapshot = {
  lastPrice: '68450.00',
  spread: '0.50',
  spreadPercent: '0.001',
  asks: [
    { price: '68454.00', quantity: '0.4520', total: '1.9820', depthPct: 100 },
    { price: '68453.50', quantity: '0.3120', total: '1.5300', depthPct: 77 },
    { price: '68453.00', quantity: '0.1850', total: '1.2180', depthPct: 61 },
    { price: '68452.50', quantity: '0.2400', total: '1.0330', depthPct: 52 },
    { price: '68452.00', quantity: '0.1250', total: '0.7930', depthPct: 40 },
    { price: '68451.50', quantity: '0.3500', total: '0.6680', depthPct: 34 },
    { price: '68451.00', quantity: '0.1980', total: '0.3180', depthPct: 16 },
    { price: '68450.50', quantity: '0.1200', total: '0.1200', depthPct: 6 },
  ],
  bids: [
    { price: '68450.00', quantity: '0.2450', total: '0.2450', depthPct: 12 },
    { price: '68449.50', quantity: '0.3800', total: '0.6250', depthPct: 30 },
    { price: '68449.00', quantity: '0.1500', total: '0.7750', depthPct: 37 },
    { price: '68448.50', quantity: '0.4200', total: '1.1950', depthPct: 57 },
    { price: '68448.00', quantity: '0.2800', total: '1.4750', depthPct: 70 },
    { price: '68447.50', quantity: '0.1900', total: '1.6650', depthPct: 79 },
    { price: '68447.00', quantity: '0.2200', total: '1.8850', depthPct: 90 },
    { price: '68446.50', quantity: '0.2100', total: '2.0950', depthPct: 100 },
  ],
}

export const MOCK_ORDERBOOK_ETH: OrderBookSnapshot = {
  lastPrice: '3520.50',
  spread: '0.20',
  spreadPercent: '0.006',
  asks: [
    { price: '3522.50', quantity: '3.4200', total: '16.8500', depthPct: 100 },
    { price: '3522.00', quantity: '2.8500', total: '13.4300', depthPct: 80 },
    { price: '3521.80', quantity: '1.9200', total: '10.5800', depthPct: 63 },
    { price: '3521.50', quantity: '2.4000', total: '8.6600',  depthPct: 51 },
    { price: '3521.20', quantity: '1.6500', total: '6.2600',  depthPct: 37 },
    { price: '3521.00', quantity: '2.1000', total: '4.6100',  depthPct: 27 },
    { price: '3520.80', quantity: '1.3800', total: '2.5100',  depthPct: 15 },
    { price: '3520.60', quantity: '1.1300', total: '1.1300',  depthPct: 7 },
  ],
  bids: [
    { price: '3520.40', quantity: '1.8500', total: '1.8500',  depthPct: 11 },
    { price: '3520.00', quantity: '2.4500', total: '4.3000',  depthPct: 26 },
    { price: '3519.80', quantity: '1.9000', total: '6.2000',  depthPct: 37 },
    { price: '3519.50', quantity: '3.1000', total: '9.3000',  depthPct: 56 },
    { price: '3519.20', quantity: '2.2500', total: '11.5500', depthPct: 69 },
    { price: '3519.00', quantity: '1.8000', total: '13.3500', depthPct: 80 },
    { price: '3518.50', quantity: '2.1500', total: '15.5000', depthPct: 93 },
    { price: '3518.00', quantity: '1.2000', total: '16.7000', depthPct: 100 },
  ],
}

export const MOCK_ORDERBOOK_SOL: OrderBookSnapshot = {
  lastPrice: '152.80',
  spread: '0.05',
  spreadPercent: '0.033',
  asks: [
    { price: '153.30', quantity: '45.20', total: '248.60', depthPct: 100 },
    { price: '153.20', quantity: '38.50', total: '203.40', depthPct: 82 },
    { price: '153.10', quantity: '29.00', total: '164.90', depthPct: 66 },
    { price: '153.00', quantity: '34.80', total: '135.90', depthPct: 55 },
    { price: '152.95', quantity: '28.20', total: '101.10', depthPct: 41 },
    { price: '152.90', quantity: '31.50', total: '72.90',  depthPct: 29 },
    { price: '152.85', quantity: '22.40', total: '41.40',  depthPct: 17 },
    { price: '152.82', quantity: '19.00', total: '19.00',  depthPct: 8 },
  ],
  bids: [
    { price: '152.77', quantity: '24.50', total: '24.50',  depthPct: 10 },
    { price: '152.70', quantity: '32.00', total: '56.50',  depthPct: 23 },
    { price: '152.65', quantity: '28.50', total: '85.00',  depthPct: 34 },
    { price: '152.60', quantity: '42.00', total: '127.00', depthPct: 51 },
    { price: '152.55', quantity: '35.20', total: '162.20', depthPct: 65 },
    { price: '152.50', quantity: '27.80', total: '190.00', depthPct: 76 },
    { price: '152.40', quantity: '33.50', total: '223.50', depthPct: 90 },
    { price: '152.30', quantity: '25.00', total: '248.50', depthPct: 100 },
  ],
}

export const MOCK_ORDERBOOKS: Record<string, OrderBookSnapshot> = {
  'BTC-USDT': MOCK_ORDERBOOK_BTC,
  'ETH-USDT': MOCK_ORDERBOOK_ETH,
  'SOL-USDT': MOCK_ORDERBOOK_SOL,
}

/**
 * Returns mock orderbook snapshot for a given market ID.
 * NEVER defaults to BTC for non-BTC markets.
 */
export function getMockOrderBook(marketId: string): OrderBookSnapshot {
  if (MOCK_ORDERBOOKS[marketId]) {
    return MOCK_ORDERBOOKS[marketId]
  }
  if (marketId.includes('ETH')) return MOCK_ORDERBOOK_ETH
  if (marketId.includes('SOL')) return MOCK_ORDERBOOK_SOL
  return MOCK_ORDERBOOK_BTC
}

/**
 * Returns mock ticker for a given market ID.
 * NEVER defaults to BTC for non-BTC markets.
 */
export function getMockTicker(marketId: string): Ticker24h {
  if (MOCK_TRADE_TICKERS[marketId]) {
    return MOCK_TRADE_TICKERS[marketId]
  }
  return {
    market_id: marketId,
    last_price: '0.00',
    high_24h: '0.00',
    low_24h: '0.00',
    volume_24h: '0.00',
    quote_volume_24h: '0.00',
    price_change_24h_percent: '0.00',
  }
}

/**
 * Generate realistic deterministic candlestick data for BTC/USDT, ETH/USDT, SOL/USDT
 */
export function generateMockCandles(marketId: string, count = 100): Candle[] {
  const candles: Candle[] = []
  const now = Math.floor(Date.now() / 1000)
  const intervalSeconds = 3600 // 1 hour

  let basePrice = 68450
  let volatility = 180

  if (marketId === 'ETH-USDT') {
    basePrice = 3520
    volatility = 20
  } else if (marketId === 'SOL-USDT') {
    basePrice = 152
    volatility = 2.5
  }

  // Generate backwards from (now - count * intervalSeconds)
  const startTime = now - count * intervalSeconds
  let currentClose = basePrice - 1200

  for (let i = 0; i < count; i++) {
    const t = startTime + i * intervalSeconds
    // Sine-wave + pseudo-random pattern
    const sinFactor = Math.sin(i * 0.15) * (volatility * 1.5)
    const noise = ((i * 17 + 7) % 31 - 15) * (volatility * 0.1)
    const open = currentClose
    const change = sinFactor + noise
    const close = Math.max(1, open + change)
    const high = Math.max(open, close) + Math.abs((i * 13) % 20) * (volatility * 0.08)
    const low = Math.min(open, close) - Math.abs((i * 11) % 20) * (volatility * 0.08)
    const vol = (15 + ((i * 23) % 45)) * (basePrice > 10000 ? 1 : 10)

    candles.push({
      start_time: new Date(t * 1000).toISOString(),
      open: open.toFixed(2),
      high: high.toFixed(2),
      low: low.toFixed(2),
      close: close.toFixed(2),
      volume: vol.toFixed(4),
      quote_volume: (vol * close).toFixed(2),
    })

    currentClose = close
  }

  // Ensure last candle is close to current price
  if (candles.length > 0) {
    const last = candles[candles.length - 1]
    last.close = basePrice.toFixed(2)
    last.high = Math.max(parseFloat(last.high), basePrice + volatility * 0.2).toFixed(2)
  }

  return candles
}
