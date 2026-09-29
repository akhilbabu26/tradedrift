import { useState, useEffect, useMemo } from 'react'
import { marketApi, type Market, type Ticker24h } from '../api/market'
import { wsService, WsChannels } from '../api/ws'
import { getMarketMetadata } from '../utils/marketMetadata'
import type { SelectedMarket } from '../types/trade'

export function useTradeMarket(initialMarketId = 'BTC-USDT') {
  const [markets, setMarkets] = useState<Market[]>([])
  const [selectedMarketId, setSelectedMarketId] = useState<string>(initialMarketId)
  const [ticker, setTicker] = useState<Ticker24h | null>(null)
  const [isDemoData, setIsDemoData] = useState(false)
  const [loading, setLoading] = useState(true)

  // 1. Fetch market list
  useEffect(() => {
    let mounted = true
    async function loadMarkets() {
      try {
        const list = await marketApi.getMarkets()
        if (!mounted) return
        if (list && list.length > 0) {
          setMarkets(list)
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

  // 2. Fetch initial ticker & subscribe to live ticker WS stream
  useEffect(() => {
    let mounted = true
    setIsDemoData(false)

    async function loadTicker() {
      try {
        const data = await marketApi.getTicker(selectedMarketId)
        if (!mounted) return
        if (data && data.last_price) {
          setTicker(data)
          setIsDemoData(false)
        }
      } catch (err) {
        if (!mounted) return
        console.warn(`Failed to fetch ticker for ${selectedMarketId}`, err)
      }
    }

    loadTicker()

    // Subscribe to WS stream
    const stream = WsChannels.ticker(selectedMarketId)
    const unsubscribe = wsService.subscribe(stream, (liveData: any) => {
      if (!mounted) return
      if (liveData) {
        setTicker((prev) => {
          const lastPrice = liveData.lastPrice ?? liveData.last_price ?? liveData.price
          const high = liveData.high24h ?? liveData.high_24h ?? liveData.high
          const low = liveData.low24h ?? liveData.low_24h ?? liveData.low
          const vol = liveData.volume24h ?? liveData.volume_24h ?? liveData.volume
          const quoteVol = liveData.quoteVolume24h ?? liveData.quote_volume_24h ?? liveData.quote_volume
          const change = liveData.priceChange24hPercent ?? liveData.price_change_24h_percent ?? liveData.change_24h

          return {
            market_id: selectedMarketId,
            last_price: lastPrice !== undefined && lastPrice !== null ? String(lastPrice) : (prev?.last_price ?? '0'),
            high_24h: high !== undefined && high !== null ? String(high) : (prev?.high_24h ?? '0'),
            low_24h: low !== undefined && low !== null ? String(low) : (prev?.low_24h ?? '0'),
            volume_24h: vol !== undefined && vol !== null ? String(vol) : (prev?.volume_24h ?? '0'),
            quote_volume_24h: quoteVol !== undefined && quoteVol !== null ? String(quoteVol) : (prev?.quote_volume_24h ?? '0'),
            price_change_24h_percent: change !== undefined && change !== null ? String(change) : (prev?.price_change_24h_percent ?? '0'),
          }
        })
        setIsDemoData(false)
      }
    })

    return () => {
      mounted = false
      unsubscribe()
    }
  }, [selectedMarketId])

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
    ticker,
    isDemoData,
    loading,
  }
}
