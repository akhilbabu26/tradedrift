// ─── Markets Types ────────────────────────────────────────────────────────────
// All financial values are strings at API boundaries to preserve precision.
// These types mirror the backend market/ticker API in src/api/market.ts so that
// replacing mock data with real API/WebSocket calls is a drop-in swap.

/** A single row in the Markets table */
export interface MarketEntry {
  rank: number
  pair: string            // "BTC/USDT"
  asset: string           // "BTC"
  name: string            // "Bitcoin"
  lastPrice: string       // "67842.50"
  change24h: string       // "2.45"  (absolute value; sign from `positive`)
  positive: boolean
  high24h: string         // "68400.00"
  low24h: string          // "66120.00"
  volume24h: string       // "$1.42B"  (pre-formatted for display)
  sparklinePoints: number[] // normalized 0–1 values for 7D sparkline
  iconColor: string       // "#f7931a"
  iconLabel: string       // "₿"
}

/** Data for a single highlight card (Top Gainer / Top Loser / Volume Leader) */
export interface MarketHighlight {
  type: 'gainer' | 'loser' | 'volume'
  label: string           // "Top Gainer"
  pair: string            // "SOL/USDT"
  name: string            // "Solana"
  price: string           // "184.20"
  change: string          // "14.20"  (absolute value; sign from `positive`)
  positive: boolean
  /** Only present on the volume leader card */
  extraLabel?: string     // "$1.42B 24h Volume"
  sparklinePoints: number[]
  iconColor: string
  iconLabel: string
}

/** Summary statistics shown at the top of the page */
export interface MarketStats {
  marketsListed: number
  volume24h: string       // "$2.95B"  (pre-formatted)
  liveDataLabel: string   // "via WebSocket"
}

/** Root shape returned by useMarkets() / the mock */
export interface MarketsData {
  stats: MarketStats
  highlights: MarketHighlight[]
  entries: MarketEntry[]
}
