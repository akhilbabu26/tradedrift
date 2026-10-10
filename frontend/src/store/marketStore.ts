/**
 * src/store/marketStore.ts
 *
 * Centralized Zustand store for markets, 24h tickers, and trend lines.
 * SOURCE OF TRUTH: frontend/docs/all backend apis.md
 *
 * Phase 2 Architectural Guarantees:
 * 1. Single Live Price Source:
 *    Multiple components (TradeMarketHeader, MarketPulse, MarketsTable, LiveSparkline)
 *    subscribe to and read from this store instead of opening duplicate WS channels.
 * 2. Timestamp Guards against Stale REST Responses:
 *    Tracks epoch timestamps of incoming WebSocket ticks. When a REST snapshot
 *    (e.g. GET /api/v1/markets/overview) resolves, it will NOT overwrite a market's
 *    ticker if a newer WebSocket tick was received after the REST request started.
 * 3. Reference-Counted Subscriptions:
 *    Multiple components can subscribe to the same market's ticker stream;
 *    wsService.subscribe is only activated once and unsubscribed when all unmount.
 */

import { create } from 'zustand'
import { marketApi, type Market, type Ticker24h, type MarketOverview } from '../api/market'
import { wsService, WsChannels } from '../api/ws'
import { extractApiError } from '../utils/apiError'

export interface MarketState {
  markets: Market[]
  tickers: Record<string, Ticker24h>
  trends: Record<string, number[]>
  tickerTimestamps: Record<string, number> // marketId -> timestamp (ms) of latest WS update
  loading: boolean
  error: string | null

  loadMarketsAndOverview: () => Promise<void>
  subscribeTicker: (marketId: string) => () => void
  subscribeAllTickers: () => () => void
  getTicker: (marketId: string) => Ticker24h | null
  getTrend: (marketId: string) => number[]
}

// Subscription reference counting
const subscriberCounts = new Map<string, number>()
const unsubs = new Map<string, () => void>()

function handleIncomingTicker(
  marketId: string,
  raw: any,
  set: (fn: (state: MarketState) => Partial<MarketState>) => void
) {
  if (!raw) return
  const lastPrice = raw.lastPrice ?? raw.last_price ?? raw.price
  if (lastPrice === undefined || lastPrice === null) return

  const numPrice = parseFloat(String(lastPrice))
  if (isNaN(numPrice) || !isFinite(numPrice) || numPrice <= 0) return

  const now = Date.now()

  set((state) => {
    const prevTicker = state.tickers[marketId]
    const high = raw.high24h ?? raw.high_24h ?? raw.high
    const low = raw.low24h ?? raw.low_24h ?? raw.low
    const vol = raw.volume24h ?? raw.volume_24h ?? raw.volume
    const quoteVol = raw.quoteVolume24h ?? raw.quote_volume_24h ?? raw.quote_volume
    const change = raw.priceChange24hPercent ?? raw.price_change_24h_percent ?? raw.change_24h

    const updatedTicker: Ticker24h = {
      market_id: marketId,
      last_price: String(lastPrice),
      high_24h: high !== undefined && high !== null ? String(high) : (prevTicker?.high_24h ?? '0'),
      low_24h: low !== undefined && low !== null ? String(low) : (prevTicker?.low_24h ?? '0'),
      volume_24h: vol !== undefined && vol !== null ? String(vol) : (prevTicker?.volume_24h ?? '0'),
      quote_volume_24h: quoteVol !== undefined && quoteVol !== null ? String(quoteVol) : (prevTicker?.quote_volume_24h ?? '0'),
      price_change_24h_percent: change !== undefined && change !== null ? String(change) : (prevTicker?.price_change_24h_percent ?? '0'),
    }

    // Update trend sparkline series
    const currentTrend = state.trends[marketId] || []
    let updatedTrend = currentTrend
    if (currentTrend.length === 0) {
      updatedTrend = [numPrice]
    } else {
      const lastIdx = currentTrend.length - 1
      if (currentTrend[lastIdx] !== numPrice) {
        updatedTrend = [...currentTrend.slice(0, lastIdx), numPrice]
      }
    }

    return {
      tickers: { ...state.tickers, [marketId]: updatedTicker },
      trends: { ...state.trends, [marketId]: updatedTrend },
      tickerTimestamps: { ...state.tickerTimestamps, [marketId]: now },
    }
  })
}

export const useMarketStore = create<MarketState>((set, get) => ({
  markets: [],
  tickers: {},
  trends: {},
  tickerTimestamps: {},
  loading: false,
  error: null,

  loadMarketsAndOverview: async () => {
    const requestStartTime = Date.now()
    set({ loading: true, error: null })

    try {
      const [marketList, overviewList] = await Promise.all([
        marketApi.getMarkets().catch(() => [] as Market[]),
        marketApi.getMarketsOverview().catch(() => [] as MarketOverview[]),
      ])

      const state = get()
      const newTickers: Record<string, Ticker24h> = { ...state.tickers }
      const newTrends: Record<string, number[]> = { ...state.trends }

      overviewList.forEach((ov) => {
        const lastWsTime = state.tickerTimestamps[ov.market_id] || 0
        // Timestamp guard: If a newer WS tick arrived after our REST request began, do not overwrite it!
        if (lastWsTime < requestStartTime) {
          newTickers[ov.market_id] = {
            market_id: ov.market_id,
            last_price: ov.last_price,
            high_24h: ov.high_24h,
            low_24h: ov.low_24h,
            volume_24h: ov.volume_24h,
            quote_volume_24h: ov.quote_volume_24h,
            price_change_24h_percent: ov.price_change_24h_percent,
          }
        }

        if (ov.trend && ov.trend.length > 0) {
          const validFloats = ov.trend
            .map((v) => parseFloat(v))
            .filter((v) => !isNaN(v) && isFinite(v) && v > 0)
          if (validFloats.length > 0) {
            newTrends[ov.market_id] = validFloats.slice(-24)
          }
        }
      })

      // If any market still has no trend points, initialize with last price
      marketList.forEach((m) => {
        if (!newTrends[m.id] || newTrends[m.id].length === 0) {
          const p = parseFloat(newTickers[m.id]?.last_price || '0')
          if (p > 0) {
            newTrends[m.id] = [p]
          }
        }
      })

      set({
        markets: marketList.length > 0 ? marketList : state.markets,
        tickers: newTickers,
        trends: newTrends,
        loading: false,
        error: null,
      })
    } catch (err) {
      set({ loading: false, error: extractApiError(err) })
    }
  },

  subscribeTicker: (marketId: string) => {
    const currentCount = subscriberCounts.get(marketId) || 0
    subscriberCounts.set(marketId, currentCount + 1)

    if (currentCount === 0) {
      const channel = WsChannels.ticker(marketId)
      const unsub = wsService.subscribe(channel, (payload) => {
        handleIncomingTicker(marketId, payload, set)
      })
      unsubs.set(marketId, unsub)
    }

    return () => {
      const count = subscriberCounts.get(marketId) || 1
      const newCount = Math.max(0, count - 1)
      subscriberCounts.set(marketId, newCount)

      if (newCount === 0) {
        const unsub = unsubs.get(marketId)
        if (unsub) {
          unsub()
          unsubs.delete(marketId)
        }
      }
    }
  },

  subscribeAllTickers: () => {
    const markets = get().markets
    const cleanups: Array<() => void> = []

    markets.forEach((m) => {
      cleanups.push(get().subscribeTicker(m.id))
    })

    return () => {
      cleanups.forEach((c) => c())
    }
  },

  getTicker: (marketId: string) => {
    return get().tickers[marketId] || null
  },

  getTrend: (marketId: string) => {
    return get().trends[marketId] || []
  },
}))

// Auto-resync overview on WebSocket reconnection
wsService.onReconnect(() => {
  useMarketStore.getState().loadMarketsAndOverview()
})
