/**
 * Portfolio Page — TypeScript Types
 *
 * All financial values are kept as strings to match API contracts and allow
 * safe Decimal.js re-parsing without precision loss.
 */

import type { AssetMetadata } from '../utils/marketMetadata'

// ── Timeframe & Filter Types ────────────────────────────────────────────────

/** Timeframe options for Portfolio Performance chart */
export type TimeframeOption = '24H' | '7D' | '30D' | '90D' | 'ALL'

/** Holdings table filter tab */
export type HoldingsFilterTab = 'all' | 'profit' | 'loss'

// ── Holdings ────────────────────────────────────────────────────────────────

export interface PortfolioHolding {
  /** Ticker symbol e.g. "BTC", "ETH" */
  asset: string
  /** Full asset name e.g. "Bitcoin" */
  name: string
  /** Asset metadata (colors, icon, symbol) */
  meta: AssetMetadata
  /** Raw quantity held */
  quantity: string
  /** Weighted average entry cost in USDT */
  averageCost: string
  /** Current mark price in USDT */
  currentPrice: string
  /** Total market value = quantity × currentPrice */
  marketValue: string
  /** Unrealized PnL in USDT */
  unrealizedPnl: string
  /** Unrealized PnL as percentage of cost basis */
  unrealizedPnlPct: string
  /** Realized PnL from closed portions of this asset */
  realizedPnl: string
  /** Portfolio weight percentage (0-100) */
  weightPct: string
}

// ── Executive Metrics ───────────────────────────────────────────────────────

export interface PortfolioExecutiveMetrics {
  /** Total portfolio valuation in USDT */
  totalValue: string
  /** Absolute daily P&L change in USDT */
  dailyChangeValue: string
  /** Daily P&L change as percentage */
  dailyChangePct: string
  /** Total unrealized PnL across all open positions */
  unrealizedPnl: string
  /** Unrealized PnL as percentage of total cost basis */
  unrealizedPnlPct: string
  /** Total realized PnL from all closed trades */
  realizedPnl: string
  /** 24-hour PnL absolute value */
  pnl24h: string
  /** 24-hour PnL as percentage */
  pnl24hPct: string
}

// ── Asset Allocation ────────────────────────────────────────────────────────

export type AssetExposureType = 'crypto' | 'cash'

export interface AssetAllocationItem {
  asset: string
  name: string
  percentage: string    // e.g. "54.2"
  valueUSDT: string
  hexColor: string
  exposureType: AssetExposureType
}

// ── Performance Chart Data ──────────────────────────────────────────────────

export interface PerformanceDataPoint {
  /** Unix timestamp in seconds (UTCTimestamp-compatible) */
  time: number
  /** Portfolio equity value at this point */
  portfolioValue: number
  /** BTC benchmark value at this point (normalized to same start) */
  btcBenchmarkValue: number
}

// ── Portfolio Insights ──────────────────────────────────────────────────────

export interface InsightEntry {
  asset: string
  name: string
  roiPct: string          // e.g. "+34.82" or "-4.20"
  direction: 'top' | 'under'
}

export interface PortfolioInsightsData {
  topPerformer: InsightEntry
  underPerformer: InsightEntry
  totalInvested: string   // cost basis in USDT
  last7DaysPct: string    // e.g. "+3.38"
  quote: string
}
