/**
 * Analytics & Journal — TypeScript Types
 *
 * All financial values are kept as strings or typed numbers compatible with
 * Decimal.js and TradeDrift formatters.
 */

import type { AssetMetadata } from '../utils/marketMetadata'

export type DateRangeOption = 'Last 30 Days' | 'Last 7 Days' | 'Last 90 Days' | 'Year to Date'

export interface KpiCardItem {
  id: string
  label: string
  value: string
  unit?: string
  trendValue: string
  trendPeriod: string
  isPositive: boolean
  visualType: 'sparkline' | 'bars'
}

export interface AssetPerformanceItem {
  asset: 'BTC' | 'ETH' | 'SOL' | 'USDT'
  name: string
  meta: AssetMetadata
  holdings: string
  avgEntryPrice: string
  currentPrice: string
  unrealizedPnl: string
  unrealizedPnlPct: string
  allocationPct: number
  barColor: string
}

export type TradingActivityTab = 'side' | 'asset' | 'month'

export interface BySideData {
  totalFills: number
  buyFills: number
  buyPct: number
  sellFills: number
  sellPct: number
}

export interface ByAssetFillItem {
  asset: 'BTC' | 'ETH' | 'SOL'
  name: string
  meta: AssetMetadata
  fillsCount: number
  percentage: number
  color: string
}

export interface ByMonthFillItem {
  month: string
  fillsCount: number
  percentage: number
}

export interface ExecutionFillItem {
  id: string
  time: string
  tradeId: string
  pair: string
  side: 'BUY' | 'SELL'
  price: string
  filledAmount: string
  totalValue: string
}
