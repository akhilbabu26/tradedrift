// ─── Markets Mock Data ────────────────────────────────────────────────────────
// Single source of truth for all Markets page display data.
// Data shapes match src/types/markets.ts and mirror the backend API contract
// (src/api/market.ts: Market, Ticker24h, Candle).
//
// REPLACE THIS with real API + WebSocket data by updating useMarkets():
//   - stats     → GET /api/v1/markets (count) + aggregate tickers
//   - highlights → derived from tickers (sort by change / volume)
//   - entries   → GET /api/v1/markets + GET /api/v1/markets/:id/ticker per row
//                 + WebSocket ticker stream for live price updates

import type { MarketsData } from '../types/markets'

export const MARKETS_MOCK: MarketsData = {
  // ── Summary strip ──────────────────────────────────────────────────────────
  stats: {
    marketsListed: 3,
    volume24h:     '$2.95B',
    liveDataLabel: 'via WebSocket',
  },

  // ── Highlight cards (Top Gainer / Top Loser / Volume Leader) ──────────────
  highlights: [
    {
      type:     'gainer',
      label:    'Top Gainer',
      pair:     'SOL / USDT',
      name:     'Solana',
      price:    '184.20',
      change:   '14.20',
      positive: true,
      sparklinePoints: [0.30, 0.35, 0.38, 0.44, 0.50, 0.58, 0.65, 0.74, 0.83, 0.92],
      iconColor: '#9945ff',
      iconLabel: 'S',
    },
    {
      type:     'loser',
      label:    'Top Loser',
      pair:     'ETH / USDT',
      name:     'Ethereum',
      price:    '3,520.40',
      change:   '1.80',
      positive: false,
      sparklinePoints: [0.80, 0.75, 0.72, 0.68, 0.64, 0.60, 0.57, 0.54, 0.51, 0.47],
      iconColor: '#627eea',
      iconLabel: 'Ξ',
    },
    {
      type:        'volume',
      label:       '24h Volume Leader',
      pair:        'BTC / USDT',
      name:        'Bitcoin',
      price:       '67,842.50',
      change:      '2.45',
      positive:    true,
      extraLabel:  '$1.42B 24h Volume',
      sparklinePoints: [0.42, 0.46, 0.50, 0.54, 0.57, 0.61, 0.65, 0.69, 0.73, 0.78],
      iconColor: '#f7931a',
      iconLabel: '₿',
    },
  ],

  // ── Table rows ─────────────────────────────────────────────────────────────
  entries: [
    {
      rank:      1,
      pair:      'BTC/USDT',
      asset:     'BTC',
      name:      'Bitcoin',
      lastPrice: '67842.50',
      change24h: '2.45',
      positive:  true,
      high24h:   '68400.00',
      low24h:    '66120.00',
      volume24h: '$1.42B',
      sparklinePoints: [0.42, 0.46, 0.50, 0.54, 0.57, 0.61, 0.65, 0.69, 0.73, 0.78],
      iconColor: '#f7931a',
      iconLabel: '₿',
    },
    {
      rank:      2,
      pair:      'ETH/USDT',
      asset:     'ETH',
      name:      'Ethereum',
      lastPrice: '3520.40',
      change24h: '1.80',
      positive:  false,
      high24h:   '3580.00',
      low24h:    '3450.00',
      volume24h: '$890M',
      sparklinePoints: [0.80, 0.75, 0.72, 0.68, 0.64, 0.60, 0.57, 0.54, 0.51, 0.47],
      iconColor: '#627eea',
      iconLabel: 'Ξ',
    },
    {
      rank:      3,
      pair:      'SOL/USDT',
      asset:     'SOL',
      name:      'Solana',
      lastPrice: '184.20',
      change24h: '14.20',
      positive:  true,
      high24h:   '186.00',
      low24h:    '161.00',
      volume24h: '$640M',
      sparklinePoints: [0.30, 0.35, 0.38, 0.44, 0.50, 0.58, 0.65, 0.74, 0.83, 0.92],
      iconColor: '#9945ff',
      iconLabel: 'S',
    },
  ],
}
