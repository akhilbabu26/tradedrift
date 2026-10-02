import { useState, useEffect, useRef } from 'react'
import { marketApi, type Candle } from '../api/market'
import { generateMockCandles } from '../data/tradeMock'
import type { Timeframe } from '../types/trade'

export function useTradeCandles(marketId: string, timeframe: Timeframe = '1h') {
  const [candles, setCandles] = useState<Candle[]>([])
  const [loading, setLoading] = useState(true)
  const [isDemoData, setIsDemoData] = useState(false)
  const lastSignatureRef = useRef<string>('')

  useEffect(() => {
    let mounted = true

    // 1. Immediately reset state on market or timeframe switch (Market Isolation)
    setCandles([])
    setLoading(true)
    setIsDemoData(false)
    lastSignatureRef.current = ''

    async function fetchCandles(isInitial = false) {
      try {
        if (isInitial) setLoading(true)
        const data = await marketApi.getCandles(marketId, timeframe, 100)
        if (!mounted) return

        if (Array.isArray(data) && data.length > 0) {
          const last = data[data.length - 1]
          const sig = `${data.length}_${last.start_time}_${last.close}`
          if (sig !== lastSignatureRef.current) {
            lastSignatureRef.current = sig
            setCandles(data)
          }
          setIsDemoData(false)
        } else {
          setCandles([])
          setIsDemoData(false)
        }
      } catch (err) {
        if (!mounted) return
        console.warn(`Failed to fetch candles for ${marketId}`, err)
        try {
          const mock = generateMockCandles(marketId, 100)
          setCandles(mock)
          setIsDemoData(true)
        } catch {
          setCandles([])
          setIsDemoData(false)
        }
      } finally {
        if (mounted && isInitial) setLoading(false)
      }
    }

    // Initial load
    fetchCandles(true)

    // Periodic synchronization / recovery mechanism (every 20s)
    // Non-intrusive: does NOT trigger full-screen loading state
    const interval = setInterval(() => {
      fetchCandles(false)
    }, 20000)

    return () => {
      mounted = false
      clearInterval(interval)
    }
  }, [marketId, timeframe])

  return {
    candles,
    loading,
    isDemoData,
  }
}
