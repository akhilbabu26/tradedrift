import { useState, useEffect } from 'react'
import { marketApi, type Candle } from '../api/market'
import type { Timeframe } from '../types/trade'
import { generateMockCandles } from '../data/tradeMock'

export function useTradeCandles(marketId: string, timeframe: Timeframe = '1h') {
  const [candles, setCandles] = useState<Candle[]>([])
  const [loading, setLoading] = useState(true)
  const [isDemoData, setIsDemoData] = useState(false)

  useEffect(() => {
    let mounted = true
    setLoading(true)

    async function fetchCandles() {
      try {
        const data = await marketApi.getCandles(marketId, timeframe, 100)
        if (!mounted) return
        if (data && data.length > 0) {
          setCandles(data)
          setIsDemoData(false)
        } else {
          // If exchange database has no historical candles for this pair, provide baseline candles
          setCandles(generateMockCandles(marketId, 80))
          setIsDemoData(true)
        }
      } catch (err) {
        if (!mounted) return
        console.warn(`Failed to fetch candles for ${marketId}, using fallback candles`, err)
        setCandles(generateMockCandles(marketId, 80))
        setIsDemoData(true)
      } finally {
        if (mounted) setLoading(false)
      }
    }

    fetchCandles()

    return () => {
      mounted = false
    }
  }, [marketId, timeframe])

  return {
    candles,
    loading,
    isDemoData,
  }
}
