import client from './client'

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

export const marketApi = {
  // GET /api/v1/markets — List all markets
  getMarkets: async (): Promise<Market[]> => {
    const res = await client.get<{ markets: Market[] }>('/api/v1/markets')
    return res.data?.markets || []
  },

  // GET /api/v1/markets/{id} — Get market details
  getMarket: async (id: string): Promise<Market> => {
    const res = await client.get<Market>(`/api/v1/markets/${id}`)
    return res.data
  },

  // GET /api/v1/markets/{id}/ticker — 24h ticker
  getTicker: async (id: string): Promise<Ticker24h> => {
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
}
