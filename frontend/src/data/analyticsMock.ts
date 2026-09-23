/**
 * Analytics & Journal — Deterministic Mock Data
 *
 * Exact values matching the TradeDrift reference design screenshot.
 * Hardcoded constants for stability across all sessions.
 */

import { getAssetMetadata } from '../utils/marketMetadata'
import type {
  KpiCardItem,
  AssetPerformanceItem,
  BySideData,
  ByAssetFillItem,
  ByMonthFillItem,
  ExecutionFillItem,
} from '../types/analytics'

export const ANALYTICS_KPIS: KpiCardItem[] = [
  {
    id: 'realized-pnl',
    label: 'Realized PnL',
    value: '+1,450.20',
    unit: 'USDT',
    trendValue: '+12.4%',
    trendPeriod: 'from last 30 days',
    isPositive: true,
    visualType: 'sparkline',
  },
  {
    id: 'unrealized-pnl',
    label: 'Unrealized PnL',
    value: '+3,120.00',
    unit: 'USDT',
    trendValue: '+8.7%',
    trendPeriod: 'from last 30 days',
    isPositive: true,
    visualType: 'sparkline',
  },
  {
    id: 'total-trades',
    label: 'Total Trades (Fills)',
    value: '42',
    trendValue: '+27.3%',
    trendPeriod: 'from last 30 days',
    isPositive: true,
    visualType: 'bars',
  },
  {
    id: 'total-volume',
    label: 'Total Volume Traded',
    value: '28,400.00',
    unit: 'USDT',
    trendValue: '+19.6%',
    trendPeriod: 'from last 30 days',
    isPositive: true,
    visualType: 'bars',
  },
]

export const ASSET_PERFORMANCE_LIST: AssetPerformanceItem[] = [
  {
    asset: 'BTC',
    name: 'Bitcoin',
    meta: getAssetMetadata('BTC'),
    holdings: '0.1200',
    avgEntryPrice: '$62,500.00',
    currentPrice: '$67,200.00',
    unrealizedPnl: '+564.00 USDT',
    unrealizedPnlPct: '+7.52%',
    allocationPct: 38,
    barColor: '#10b981', // emerald green bar
  },
  {
    asset: 'ETH',
    name: 'Ethereum',
    meta: getAssetMetadata('ETH'),
    holdings: '1.5000',
    avgEntryPrice: '$3,420.00',
    currentPrice: '$3,640.00',
    unrealizedPnl: '+330.00 USDT',
    unrealizedPnlPct: '+6.43%',
    allocationPct: 28,
    barColor: '#3b82f6', // blue bar
  },
  {
    asset: 'SOL',
    name: 'Solana',
    meta: getAssetMetadata('SOL'),
    holdings: '10.0000',
    avgEntryPrice: '$150.20',
    currentPrice: '$181.40',
    unrealizedPnl: '+312.00 USDT',
    unrealizedPnlPct: '+20.76%',
    allocationPct: 22,
    barColor: '#a855f7', // purple bar
  },
  {
    asset: 'USDT',
    name: 'Tether',
    meta: getAssetMetadata('USDT'),
    holdings: '4,250.00',
    avgEntryPrice: '$1.00',
    currentPrice: '$1.00',
    unrealizedPnl: '0.00 USDT',
    unrealizedPnlPct: '0.00%',
    allocationPct: 12,
    barColor: '#64748b', // slate gray bar
  },
]

export const TRADING_ACTIVITY_BY_SIDE: BySideData = {
  totalFills: 42,
  buyFills: 28,
  buyPct: 66.7,
  sellFills: 14,
  sellPct: 33.3,
}

// By Asset shows BTC, ETH, and SOL fills. USDT is excluded because it is the quote currency.
export const TRADING_ACTIVITY_BY_ASSET: ByAssetFillItem[] = [
  {
    asset: 'BTC',
    name: 'Bitcoin',
    meta: getAssetMetadata('BTC'),
    fillsCount: 18,
    percentage: 42.9,
    color: '#f7931a',
  },
  {
    asset: 'ETH',
    name: 'Ethereum',
    meta: getAssetMetadata('ETH'),
    fillsCount: 14,
    percentage: 33.3,
    color: '#627eea',
  },
  {
    asset: 'SOL',
    name: 'Solana',
    meta: getAssetMetadata('SOL'),
    fillsCount: 10,
    percentage: 23.8,
    color: '#9945ff',
  },
]

export const TRADING_ACTIVITY_BY_MONTH: ByMonthFillItem[] = [
  { month: 'July 2026', fillsCount: 10, percentage: 23.8 },
  { month: 'August 2026', fillsCount: 14, percentage: 33.3 },
  { month: 'September 2026', fillsCount: 18, percentage: 42.9 },
]

export const TRADING_ACTIVITY_SUMMARY = {
  currentPortfolioValue: '24,150.80',
  currentPortfolioValueSubtext: 'Realized + Unrealized',
  activeAssetsCount: 4,
  activeAssetsSubtext: 'Different assets in portfolio',
}

export const RECENT_EXECUTION_FILLS: ExecutionFillItem[] = [
  {
    id: 'trd_9a1f2c',
    time: 'Sep 14, 2026 12:45:11',
    tradeId: 'trd_9a1f2c',
    pair: 'BTC/USDT',
    side: 'BUY',
    price: '67,150.00',
    filledAmount: '0.0500 BTC',
    totalValue: '3,357.50',
  },
  {
    id: 'trd_4d7e8b',
    time: 'Sep 14, 2026 11:20:32',
    tradeId: 'trd_4d7e8b',
    pair: 'ETH/USDT',
    side: 'BUY',
    price: '3,640.50',
    filledAmount: '1.0000 ETH',
    totalValue: '3,640.50',
  },
  {
    id: 'trd_2c9f11',
    time: 'Sep 13, 2026 16:02:18',
    tradeId: 'trd_2c9f11',
    pair: 'SOL/USDT',
    side: 'SELL',
    price: '181.20',
    filledAmount: '2.5000 SOL',
    totalValue: '453.00',
  },
  {
    id: 'trd_8e2a4d',
    time: 'Sep 12, 2026 14:33:45',
    tradeId: 'trd_8e2a4d',
    pair: 'BTC/USDT',
    side: 'SELL',
    price: '68,500.00',
    filledAmount: '0.0300 BTC',
    totalValue: '2,055.00',
  },
  {
    id: 'trd_67fb3c',
    time: 'Sep 12, 2026 10:11:09',
    tradeId: 'trd_67fb3c',
    pair: 'ETH/USDT',
    side: 'BUY',
    price: '3,580.00',
    filledAmount: '0.5000 ETH',
    totalValue: '1,790.00',
  },
]
