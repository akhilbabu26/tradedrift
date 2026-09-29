// ─── Dashboard Types ─────────────────────────────────────────────────────────
// All financial values are strings at API boundaries to preserve precision.
// Use src/utils/decimal.ts for arithmetic operations on these values.

export interface CoinHolding {
  asset: string           // "BTC"
  symbol: string          // "BTC/USDT"
  holdings: string        // "0.1200"
  valueUsdt: string       // "8140.20"
  pnlUsdt: string         // "+450.20"
  pnlPercent: string      // "+5.85"
  allocation: number      // 23.8  (percentage 0–100)
  color: string           // "#f7931a"
}

export interface MarketRow {
  pair: string            // "BTC/USDT"
  symbol: string          // "BTC"
  price: string           // "68450.00"
  change24h: string       // "+2.40"
  positive: boolean
  iconBg: string          // Tailwind classes for icon background
  iconLabel: string       // "₿"
  sparklinePoints: number[] // normalized 0–1 values for sparkline
  volume24h?: string      // "124.5M"
  quoteVolume?: number    // numeric for sorting
}

export interface ActiveOrder {
  id: string
  type: 'Limit Buy' | 'Limit Sell' | 'Market Buy' | 'Market Sell' | string
  side: 'BUY' | 'SELL'
  pair: string            // "BTC/USDT"
  price: string           // "64200.00"
  amount: string          // "0.05"
  status: 'Open' | 'Partial' | 'Cancelled'
}

export interface ServiceStatus {
  name: string            // "Matching Engine"
  status: 'Online' | 'Degraded' | 'Offline'
  latencyMs: number       // 4
}

export interface RecentFill {
  id: string
  type: 'Buy' | 'Sell'
  pair: string            // "BTC/USDT"
  price: string           // "67500.00"
  amount: string          // "0.05"
  timeIso: string         // ISO 8601 timestamp
}

export interface PortfolioDataPoint {
  // lightweight-charts expects time as a UTC timestamp (seconds) or DateString
  time: number            // unix timestamp in seconds
  value: number           // portfolio value in USDT
}

export type PeriodTab = '7D' | '30D' | '90D' | '1Y'

export interface DashboardData {
  portfolioValue: string          // "34250.80"
  portfolioChangeUsdt: string     // "+1120.40"
  portfolioChangePercent: string  // "+3.38"
  simulatorBalance: string        // "23975.56"
  availablePercent: number        // 70
  inOrdersPercent: number         // 30
  availableUsdt: string           // "23975.56"
  inOrdersUsdt: string            // "10275.24"
  holdings: CoinHolding[]
  chartData: PortfolioDataPoint[]
  allTimeProfit: string           // "+14250.80"
  winRate: number                 // 68
  totalTrades: number             // 342
  marketRows: MarketRow[]
  activeOrders: ActiveOrder[]
  services: ServiceStatus[]
  recentFills: RecentFill[]
}
