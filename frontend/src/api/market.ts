/**
 * src/api/market.ts
 *
 * Market REST API service.
 * SOURCE OF TRUTH: frontend/docs/all backend apis.md
 *
 * All market endpoints are PUBLIC (no auth required).
 */
import client from './client'

const isDev = import.meta.env.DEV
function apiLog(msg: string) {
  if (isDev) console.debug(`[API] ${msg}`)
}

export interface Market {
  id: string
  base_asset: string
  quote_asset: string
  tick_size: string
  lot_size: string
  status: string
  min_quantity: string
  created_at: string
  updated_at: string
}

export interface Ticker24h {
  market_id: string
  last_price: string
  high_24h: string
  low_24h: string
  volume_24h: string
  quote_volume_24h: string
  price_change_24h_percent: string
}

export interface Candle {
  start_time: string
  open: string
  high: string
  low: string
  close: string
  volume: string
  quote_volume: string
}

/** Enriched market overview entry from /api/v1/markets/overview */
export interface MarketOverview {
  market: Market
  ticker: Ticker24h
  last_candle?: Candle
}

/** Public trade tape entry from /api/v1/markets/{id}/trades */
export interface MarketTrade {
  id: string
  market_id: string
  price: string
  quantity: string
  taker_side: string   // "BUY" | "SELL"
  created_at: string
}

export const marketApi = {
  // GET /api/v1/markets — List all markets
  getMarkets: async (): Promise<Market[]> => {
    apiLog('GET /api/v1/markets')
    const res = await client.get<{ markets: Market[] }>('/api/v1/markets')
    return res.data?.markets || []
  },

  // GET /api/v1/markets/{id} — Get market details
  getMarket: async (id: string): Promise<Market> => {
    apiLog(`GET /api/v1/markets/${id}`)
    const res = await client.get<Market>(`/api/v1/markets/${id}`)
    return res.data
  },

  // GET /api/v1/markets/{id}/ticker — 24h ticker
  getTicker: async (id: string): Promise<Ticker24h> => {
    apiLog(`GET /api/v1/markets/${id}/ticker`)
    const res = await client.get<Ticker24h>(`/api/v1/markets/${id}/ticker`)
    return res.data
  },

  // GET /api/v1/markets/{id}/candles — Candlestick bars
  getCandles: async (id: string, resolution = '1h', limit = 100): Promise<Candle[]> => {
    const res = await client.get<{ candles: Candle[] }>(`/api/v1/markets/${id}/candles`, {
      params: { resolution, limit },
    })
    return res.data?.candles || []
  },

  // GET /api/v1/markets/overview — all markets enriched with ticker + candle data
  getMarketsOverview: async (): Promise<MarketOverview[]> => {
    apiLog('GET /api/v1/markets/overview')
    const res = await client.get<{ markets: MarketOverview[] }>('/api/v1/markets/overview')
    return res.data?.markets || []
  },

  // GET /api/v1/markets/{id}/trades — public market trade tape
  getMarketTrades: async (id: string, limit = 50, cursor?: string): Promise<MarketTrade[]> => {
    apiLog(`GET /api/v1/markets/${id}/trades`)
    const params: Record<string, unknown> = { limit }
    if (cursor) params.cursor = cursor
    const res = await client.get<{ trades: MarketTrade[] }>(`/api/v1/markets/${id}/trades`, { params })
    return res.data?.trades || []
  },
}
