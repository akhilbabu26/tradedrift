import { useState, useEffect, useMemo } from 'react'
import { marketApi, type Market, type Ticker24h, type MarketOverview } from '../api/market'
import { useMarketStore } from '../store/marketStore'
import { getMarketMetadata } from '../utils/marketMetadata'
import type { SelectedMarket } from '../types/trade'

export function useTradeMarket(initialMarketId = 'BTC-USDT') {
  const [markets, setMarkets] = useState<Market[]>([])
  const [selectedMarketId, setSelectedMarketId] = useState<string>(initialMarketId)
  const [ticker, setTicker] = useState<Ticker24h | null>(null)
  const [marketTrends, setMarketTrends] = useState<Record<string, number[]>>({})
  const [isDemoData, setIsDemoData] = useState(false)
  const [loading, setLoading] = useState(true)

  const storeTicker = useMarketStore((s) => s.tickers[selectedMarketId])
  const storeTrends = useMarketStore((s) => s.trends)
  const subscribeStoreTicker = useMarketStore((s) => s.subscribeTicker)

  // 1. Fetch market list and shared 24h overview trends
  useEffect(() => {
    let mounted = true
    async function loadMarkets() {
      try {
        const [list, overviewList] = await Promise.all([
          marketApi.getMarkets(),
          marketApi.getMarketsOverview().catch(() => [] as MarketOverview[]),
        ])
        if (!mounted) return
        if (list && list.length > 0) {
          setMarkets(list)
        }

        const trends: Record<string, number[]> = {}
        overviewList.forEach((ov) => {
          if (ov.trend && ov.trend.length > 0) {
            const validFloats = ov.trend
              .map((v) => parseFloat(v))
              .filter((v) => !isNaN(v) && isFinite(v) && v > 0)
            if (validFloats.length > 0) {
              trends[ov.market_id] = validFloats.slice(-24)
            }
          }
        })

        // If any market is missing trend from overview, fetch candles fallback
        const targetList = list && list.length > 0 ? list : [{ id: 'BTC-USDT' }, { id: 'ETH-USDT' }, { id: 'SOL-USDT' }]
        const missing = targetList.filter((m) => !trends[m.id] || trends[m.id].length === 0)
        if (missing.length > 0) {
          const candleResults = await Promise.allSettled(
            missing.map((m) => marketApi.getCandles(m.id, '1h', 24))
          )
          missing.forEach((m, i) => {
            const r = candleResults[i]
            if (r.status === 'fulfilled' && r.value.length > 0) {
              const valid = r.value
                .map((c) => parseFloat(c.close))
                .filter((v) => !isNaN(v) && isFinite(v) && v > 0)
              if (valid.length > 0) {
                trends[m.id] = valid.slice(-24)
              }
            }
          })
        }

        if (mounted) {
          setMarketTrends(trends)
        }
      } catch (err) {
        if (!mounted) return
        console.warn('Failed to fetch markets', err)
      } finally {
        if (mounted) setLoading(false)
      }
    }
    loadMarkets()
    return () => {
      mounted = false
    }
  }, [])

  // 2. Fetch initial ticker & subscribe to centralized live ticker WS stream
  useEffect(() => {
    let mounted = true
    setIsDemoData(false)
    // Market Isolation Invariant: Invalidate previous market's ticker immediately
    setTicker(null)

    const targetMarket = selectedMarketId

    async function loadTicker() {
      try {
        const data = await marketApi.getTicker(targetMarket)
        if (!mounted || targetMarket !== selectedMarketId) return
        if (data && data.last_price) {
          setTicker({
            ...data,
            market_id: targetMarket,
          })
          setIsDemoData(false)
        }
      } catch (err) {
        if (!mounted || targetMarket !== selectedMarketId) return
        console.warn(`Failed to fetch ticker for ${targetMarket}`, err)
      }
    }

    loadTicker()

    // Subscribe to centralized WS ticker stream in marketStore
    const unsubTicker = subscribeStoreTicker(targetMarket)

    return () => {
      mounted = false
      unsubTicker()
    }
  }, [selectedMarketId, subscribeStoreTicker])

  const selectedMarket = useMemo<SelectedMarket>(() => {
    const meta = getMarketMetadata(selectedMarketId)
    const baseMarket = markets.find((m) => m.id === selectedMarketId) || {
      id: selectedMarketId,
      base_asset: meta.base,
      quote_asset: meta.quote,
      tick_size: '0.01',
      lot_size: '0.0001',
      status: 'ACTIVE',
      min_quantity: '0.0001',
      created_at: '',
      updated_at: '',
    }

    return {
      ...baseMarket,
      pair: meta.pair,
      base: meta.base,
      quote: meta.quote,
      name: meta.name,
      symbol: meta.baseMeta.symbol,
      baseMeta: meta.baseMeta,
      quoteMeta: meta.quoteMeta,
    }
  }, [markets, selectedMarketId])

  return {
    markets,
    selectedMarketId,
    setSelectedMarketId,
    selectedMarket,
    ticker: storeTicker || ticker,
    marketTrends: Object.keys(storeTrends).length > 0 ? { ...marketTrends, ...storeTrends } : marketTrends,
    isDemoData,
    loading,
  }
}
