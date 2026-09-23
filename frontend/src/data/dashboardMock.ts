// ─── Dashboard Mock Data ─────────────────────────────────────────────────────
// Single source of truth for all dashboard display data.
// Data shapes match the API contract in src/types/dashboard.ts.
// Replace this with real API/WebSocket calls when the backend is ready.
// All financial values are strings (API-ready) — no raw JS floats.

import type { DashboardData, PortfolioDataPoint } from '../types/dashboard'

// Portfolio chart: 7-day data, Sep 8–14 (unix seconds, UTC midnight)
const chartData: PortfolioDataPoint[] = [
  { time: 1725753600, value: 28400 },   // Sep 8
  { time: 1725840000, value: 29200 },   // Sep 9
  { time: 1725926400, value: 30100 },   // Sep 10
  { time: 1726012800, value: 31500 },   // Sep 11
  { time: 1726099200, value: 30800 },   // Sep 12
  { time: 1726185600, value: 32400 },   // Sep 13
  { time: 1726272000, value: 34250.80 },// Sep 14
]

// Timestamps relative to "now" for recent fills
const now = Date.now()
const minsAgo = (m: number) => new Date(now - m * 60_000).toISOString()

export const DASHBOARD_MOCK: DashboardData = {
  // ── Portfolio Header ──────────────────────────────────────────────────────
  portfolioValue:          '34250.80',
  portfolioChangeUsdt:     '1120.40',
  portfolioChangePercent:  '3.38',

  // ── Simulator Balance ─────────────────────────────────────────────────────
  simulatorBalance:  '23975.56',
  availablePercent:  70,
  inOrdersPercent:   30,
  availableUsdt:     '23975.56',
  inOrdersUsdt:      '10275.24',

  // ── My Coins ──────────────────────────────────────────────────────────────
  holdings: [
    {
      asset:       'BTC',
      symbol:      'BTC/USDT',
      holdings:    '0.1200',
      valueUsdt:   '8140.20',
      pnlUsdt:     '450.20',
      pnlPercent:  '5.85',
      allocation:  23.8,
      color:       '#f7931a',
    },
    {
      asset:       'ETH',
      symbol:      'ETH/USDT',
      holdings:    '1.5000',
      valueUsdt:   '5130.18',
      pnlUsdt:     '320.40',
      pnlPercent:  '6.66',
      allocation:  15.0,
      color:       '#627eea',
    },
    {
      asset:       'SOL',
      symbol:      'SOL/USDT',
      holdings:    '10.0000',
      valueUsdt:   '1819.20',
      pnlUsdt:     '210.10',
      pnlPercent:  '13.08',
      allocation:  5.3,
      color:       '#9945ff',
    },
  ],

  // ── Portfolio Chart ───────────────────────────────────────────────────────
  chartData,

  // ── Portfolio Metrics ─────────────────────────────────────────────────────
  allTimeProfit: '14250.80',
  winRate:       68,
  totalTrades:   342,

  // ── Market Pulse (Watchlist) — BTC / ETH / SOL only ──────────────────────
  marketRows: [
    {
      pair:     'BTC/USDT',
      symbol:   'BTC',
      price:    '68450.00',
      change24h: '2.40',
      positive:  true,
      iconBg:    'bg-[#f7931a]/15 border-[#f7931a]/30 text-[#f7931a]',
      iconLabel: '₿',
      sparklinePoints: [0.45, 0.50, 0.48, 0.55, 0.62, 0.58, 0.70, 0.75, 0.72, 0.80],
    },
    {
      pair:     'ETH/USDT',
      symbol:   'ETH',
      price:    '3420.12',
      change24h: '1.80',
      positive:  true,
      iconBg:    'bg-[#627eea]/15 border-[#627eea]/30 text-[#627eea]',
      iconLabel: 'Ξ',
      sparklinePoints: [0.40, 0.45, 0.50, 0.55, 0.52, 0.60, 0.65, 0.63, 0.68, 0.72],
    },
    {
      pair:     'SOL/USDT',
      symbol:   'SOL',
      price:    '181.92',
      change24h: '0.70',
      positive:  false,
      iconBg:    'bg-[#9945ff]/15 border-[#9945ff]/30 text-[#9945ff]',
      iconLabel: 'S',
      sparklinePoints: [0.70, 0.65, 0.68, 0.60, 0.55, 0.50, 0.45, 0.48, 0.42, 0.38],
    },
  ],

  // ── Live Operations (Active Orders) ───────────────────────────────────────
  activeOrders: [
    {
      id:     'ord-001',
      type:   'Limit Buy',
      side:   'BUY',
      pair:   'BTC/USDT',
      price:  '64200.00',
      amount: '0.05',
      status: 'Open',
    },
    {
      id:     'ord-002',
      type:   'Limit Sell',
      side:   'SELL',
      pair:   'ETH/USDT',
      price:  '3600.00',
      amount: '0.50',
      status: 'Open',
    },
    {
      id:     'ord-003',
      type:   'Limit Buy',
      side:   'BUY',
      pair:   'SOL/USDT',
      price:  '170.00',
      amount: '5.00',
      status: 'Open',
    },
  ],

  // ── System Status ─────────────────────────────────────────────────────────
  services: [
    { name: 'Matching Engine', status: 'Online', latencyMs: 4  },
    { name: 'Market Data',     status: 'Online', latencyMs: 12 },
    { name: 'Order Service',   status: 'Online', latencyMs: 8  },
    { name: 'Wallet Service',  status: 'Online', latencyMs: 11 },
  ],

  // ── Recent Fills ──────────────────────────────────────────────────────────
  recentFills: [
    { id: 'fill-001', type: 'Buy',  pair: 'BTC/USDT', price: '67500.00', amount: '0.05', timeIso: minsAgo(12)  },
    { id: 'fill-002', type: 'Sell', pair: 'SOL/USDT', price: '181.20',   amount: '2.00', timeIso: minsAgo(28)  },
    { id: 'fill-003', type: 'Buy',  pair: 'ETH/USDT', price: '3380.00',  amount: '0.30', timeIso: minsAgo(60)  },
    { id: 'fill-004', type: 'Sell', pair: 'SOL/USDT', price: '180.50',   amount: '1.00', timeIso: minsAgo(120) },
  ],
}
