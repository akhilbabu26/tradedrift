/**
 * Portfolio Mock Data — deterministic, stable constants for development fallback.
 *
 * These values are HARDCODED. No random generation. Identical on every render/refresh.
 * This mock is used ONLY when the backend Portfolio API is genuinely unavailable.
 *
 * IMPORTANT: AVAX does NOT appear in MOCK_HOLDINGS.
 * AVAX is referenced only in MOCK_INSIGHTS as a historical closed-trade performer.
 * Holdings = BTC, ETH, SOL, USDT only. BNB is NOT included.
 */

import type {
  PortfolioHolding,
  PortfolioExecutiveMetrics,
  AssetAllocationItem,
  PerformanceDataPoint,
  PortfolioInsightsData,
} from '../types/portfolio'
import { getAssetMetadata } from '../utils/marketMetadata'

// ── Mock Holdings ────────────────────────────────────────────────────────────
// Only: BTC, ETH, SOL, USDT  (BNB excluded per product spec)
// AVAX is NOT in this list.

export const MOCK_HOLDINGS: PortfolioHolding[] = [
  {
    asset: 'BTC',
    name: 'Bitcoin',
    meta: getAssetMetadata('BTC'),
    quantity: '0.271250',
    averageCost: '57500.00',
    currentPrice: '68450.00',
    marketValue: '18567.40',
    unrealizedPnl: '+2970.52',
    unrealizedPnlPct: '+19.04',
    realizedPnl: '+680.00',
    weightPct: '54.2',
  },
  {
    asset: 'ETH',
    name: 'Ethereum',
    meta: getAssetMetadata('ETH'),
    quantity: '2.430000',
    averageCost: '2950.00',
    currentPrice: '3680.00',
    marketValue: '8942.30',
    unrealizedPnl: '+1773.80',
    unrealizedPnlPct: '+24.74',
    realizedPnl: '+420.00',
    weightPct: '26.1',
  },
  {
    asset: 'SOL',
    name: 'Solana',
    meta: getAssetMetadata('SOL'),
    quantity: '22.730000',
    averageCost: '105.00',
    currentPrice: '185.40',
    marketValue: '4214.20',
    unrealizedPnl: '+1827.55',
    unrealizedPnlPct: '+76.57',
    realizedPnl: '+70.00',
    weightPct: '12.3',
  },
  {
    asset: 'USDT',
    name: 'Tether',
    meta: getAssetMetadata('USDT'),
    quantity: '2526.90',
    averageCost: '1.00',
    currentPrice: '1.00',
    marketValue: '2526.90',
    unrealizedPnl: '0.00',
    unrealizedPnlPct: '0.00',
    realizedPnl: '+0.00',
    weightPct: '7.4',
  },
]

// ── Mock Executive Metrics ────────────────────────────────────────────────────
// These are the exact values shown in the reference screenshot.

export const MOCK_EXECUTIVE_METRICS: PortfolioExecutiveMetrics = {
  totalValue: '34250.80',
  dailyChangeValue: '+1120.40',
  dailyChangePct: '+3.38',
  unrealizedPnl: '+3410.20',
  unrealizedPnlPct: '+11.20',
  realizedPnl: '+1170.00',
  pnl24h: '+540.50',
  pnl24hPct: '+1.60',
}

// ── Mock Asset Allocation ─────────────────────────────────────────────────────
// Derived from mock holdings. Donut chart + breakdown list.

export const MOCK_ALLOCATION: AssetAllocationItem[] = [
  { asset: 'BTC',  name: 'Bitcoin',  percentage: '54.2', valueUSDT: '18567.40', hexColor: '#f7931a', exposureType: 'crypto' },
  { asset: 'ETH',  name: 'Ethereum', percentage: '26.1', valueUSDT: '8942.30',  hexColor: '#627eea', exposureType: 'crypto' },
  { asset: 'SOL',  name: 'Solana',   percentage: '12.3', valueUSDT: '4214.20',  hexColor: '#9945ff', exposureType: 'crypto' },
  { asset: 'USDT', name: 'Tether',   percentage: '7.4',  valueUSDT: '2526.90',  hexColor: '#26a17b', exposureType: 'cash'   },
]

// ── Mock Performance Chart Data ───────────────────────────────────────────────
// All data is HARDCODED. No Math.random(). Same values on every render.
//
// Unix timestamps (seconds). Sep 2026 dates.
// Sep 8  = 1757289600
// Sep 14 = 1757808000

function ts(dateStr: string): number {
  return Math.floor(new Date(dateStr).getTime() / 1000)
}

export const MOCK_PERFORMANCE: Record<string, PerformanceDataPoint[]> = {
  '24H': [
    { time: ts('2026-09-14T00:00:00Z'), portfolioValue: 33100, btcBenchmarkValue: 33100 },
    { time: ts('2026-09-14T01:00:00Z'), portfolioValue: 33180, btcBenchmarkValue: 33090 },
    { time: ts('2026-09-14T02:00:00Z'), portfolioValue: 33050, btcBenchmarkValue: 33020 },
    { time: ts('2026-09-14T03:00:00Z'), portfolioValue: 33240, btcBenchmarkValue: 33100 },
    { time: ts('2026-09-14T04:00:00Z'), portfolioValue: 33380, btcBenchmarkValue: 33200 },
    { time: ts('2026-09-14T05:00:00Z'), portfolioValue: 33290, btcBenchmarkValue: 33180 },
    { time: ts('2026-09-14T06:00:00Z'), portfolioValue: 33420, btcBenchmarkValue: 33240 },
    { time: ts('2026-09-14T07:00:00Z'), portfolioValue: 33550, btcBenchmarkValue: 33300 },
    { time: ts('2026-09-14T08:00:00Z'), portfolioValue: 33680, btcBenchmarkValue: 33390 },
    { time: ts('2026-09-14T09:00:00Z'), portfolioValue: 33720, btcBenchmarkValue: 33420 },
    { time: ts('2026-09-14T10:00:00Z'), portfolioValue: 33810, btcBenchmarkValue: 33500 },
    { time: ts('2026-09-14T11:00:00Z'), portfolioValue: 33900, btcBenchmarkValue: 33550 },
    { time: ts('2026-09-14T12:00:00Z'), portfolioValue: 34020, btcBenchmarkValue: 33580 },
    { time: ts('2026-09-14T13:00:00Z'), portfolioValue: 33980, btcBenchmarkValue: 33560 },
    { time: ts('2026-09-14T14:00:00Z'), portfolioValue: 34080, btcBenchmarkValue: 33640 },
    { time: ts('2026-09-14T15:00:00Z'), portfolioValue: 34100, btcBenchmarkValue: 33700 },
    { time: ts('2026-09-14T16:00:00Z'), portfolioValue: 34060, btcBenchmarkValue: 33680 },
    { time: ts('2026-09-14T17:00:00Z'), portfolioValue: 34150, btcBenchmarkValue: 33720 },
    { time: ts('2026-09-14T18:00:00Z'), portfolioValue: 34080, btcBenchmarkValue: 33690 },
    { time: ts('2026-09-14T19:00:00Z'), portfolioValue: 34200, btcBenchmarkValue: 33760 },
    { time: ts('2026-09-14T20:00:00Z'), portfolioValue: 34180, btcBenchmarkValue: 33780 },
    { time: ts('2026-09-14T21:00:00Z'), portfolioValue: 34220, btcBenchmarkValue: 33800 },
    { time: ts('2026-09-14T22:00:00Z'), portfolioValue: 34240, btcBenchmarkValue: 33820 },
    { time: ts('2026-09-14T23:00:00Z'), portfolioValue: 34250, btcBenchmarkValue: 33840 },
  ],
  '7D': [
    { time: ts('2026-09-08T00:00:00Z'), portfolioValue: 31200, btcBenchmarkValue: 31200 },
    { time: ts('2026-09-09T00:00:00Z'), portfolioValue: 31800, btcBenchmarkValue: 31500 },
    { time: ts('2026-09-10T00:00:00Z'), portfolioValue: 32100, btcBenchmarkValue: 31700 },
    { time: ts('2026-09-11T00:00:00Z'), portfolioValue: 31900, btcBenchmarkValue: 31600 },
    { time: ts('2026-09-12T00:00:00Z'), portfolioValue: 32600, btcBenchmarkValue: 32000 },
    { time: ts('2026-09-13T00:00:00Z'), portfolioValue: 33400, btcBenchmarkValue: 32600 },
    { time: ts('2026-09-14T00:00:00Z'), portfolioValue: 34250, btcBenchmarkValue: 33100 },
  ],
  '30D': [
    { time: ts('2026-08-15T00:00:00Z'), portfolioValue: 27500, btcBenchmarkValue: 27500 },
    { time: ts('2026-08-17T00:00:00Z'), portfolioValue: 28100, btcBenchmarkValue: 27700 },
    { time: ts('2026-08-19T00:00:00Z'), portfolioValue: 28600, btcBenchmarkValue: 27900 },
    { time: ts('2026-08-21T00:00:00Z'), portfolioValue: 28300, btcBenchmarkValue: 27800 },
    { time: ts('2026-08-23T00:00:00Z'), portfolioValue: 29000, btcBenchmarkValue: 28200 },
    { time: ts('2026-08-25T00:00:00Z'), portfolioValue: 29500, btcBenchmarkValue: 28600 },
    { time: ts('2026-08-27T00:00:00Z'), portfolioValue: 30100, btcBenchmarkValue: 29000 },
    { time: ts('2026-08-29T00:00:00Z'), portfolioValue: 30800, btcBenchmarkValue: 29500 },
    { time: ts('2026-08-31T00:00:00Z'), portfolioValue: 31200, btcBenchmarkValue: 29900 },
    { time: ts('2026-09-02T00:00:00Z'), portfolioValue: 31000, btcBenchmarkValue: 29800 },
    { time: ts('2026-09-04T00:00:00Z'), portfolioValue: 31600, btcBenchmarkValue: 30200 },
    { time: ts('2026-09-06T00:00:00Z'), portfolioValue: 32100, btcBenchmarkValue: 30700 },
    { time: ts('2026-09-08T00:00:00Z'), portfolioValue: 31900, btcBenchmarkValue: 30600 },
    { time: ts('2026-09-10T00:00:00Z'), portfolioValue: 32600, btcBenchmarkValue: 31100 },
    { time: ts('2026-09-12T00:00:00Z'), portfolioValue: 33400, btcBenchmarkValue: 31800 },
    { time: ts('2026-09-14T00:00:00Z'), portfolioValue: 34250, btcBenchmarkValue: 32400 },
  ],
  '90D': [
    { time: ts('2026-06-16T00:00:00Z'), portfolioValue: 18000, btcBenchmarkValue: 18000 },
    { time: ts('2026-06-30T00:00:00Z'), portfolioValue: 19500, btcBenchmarkValue: 18800 },
    { time: ts('2026-07-14T00:00:00Z'), portfolioValue: 21000, btcBenchmarkValue: 19600 },
    { time: ts('2026-07-28T00:00:00Z'), portfolioValue: 22500, btcBenchmarkValue: 20400 },
    { time: ts('2026-08-11T00:00:00Z'), portfolioValue: 25000, btcBenchmarkValue: 22000 },
    { time: ts('2026-08-25T00:00:00Z'), portfolioValue: 27500, btcBenchmarkValue: 23800 },
    { time: ts('2026-09-08T00:00:00Z'), portfolioValue: 31200, btcBenchmarkValue: 26000 },
    { time: ts('2026-09-14T00:00:00Z'), portfolioValue: 34250, btcBenchmarkValue: 27800 },
  ],
  'ALL': [
    { time: ts('2026-01-01T00:00:00Z'), portfolioValue: 10000, btcBenchmarkValue: 10000 },
    { time: ts('2026-02-01T00:00:00Z'), portfolioValue: 12000, btcBenchmarkValue: 11200 },
    { time: ts('2026-03-01T00:00:00Z'), portfolioValue: 14500, btcBenchmarkValue: 12800 },
    { time: ts('2026-04-01T00:00:00Z'), portfolioValue: 13800, btcBenchmarkValue: 12400 },
    { time: ts('2026-05-01T00:00:00Z'), portfolioValue: 16000, btcBenchmarkValue: 14000 },
    { time: ts('2026-06-01T00:00:00Z'), portfolioValue: 18500, btcBenchmarkValue: 15500 },
    { time: ts('2026-07-01T00:00:00Z'), portfolioValue: 21000, btcBenchmarkValue: 17200 },
    { time: ts('2026-08-01T00:00:00Z'), portfolioValue: 24500, btcBenchmarkValue: 19800 },
    { time: ts('2026-09-01T00:00:00Z'), portfolioValue: 29500, btcBenchmarkValue: 23200 },
    { time: ts('2026-09-14T00:00:00Z'), portfolioValue: 34250, btcBenchmarkValue: 26800 },
  ],
}

// ── Mock Portfolio Insights ────────────────────────────────────────────────────
// AVAX appears here as a historical closed-trade underperformer.
// It is NOT in MOCK_HOLDINGS and must NOT be added to the Holdings table.

export const MOCK_INSIGHTS: PortfolioInsightsData = {
  topPerformer: {
    asset: 'SOL',
    name: 'Solana',
    roiPct: '+34.82',
    direction: 'top',
  },
  underPerformer: {
    asset: 'AVAX',
    name: 'Avalanche',
    roiPct: '-4.20',
    direction: 'under',
  },
  totalInvested: '29670.60',
  last7DaysPct: '+3.38',
  quote: 'Consistent actions create exceptional results.',
}
