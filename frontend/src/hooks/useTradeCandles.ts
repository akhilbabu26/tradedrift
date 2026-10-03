import { useState, useEffect, useRef, useCallback } from 'react'
import { marketApi, type Candle } from '../api/market'
import type { Timeframe } from '../types/trade'

export function useTradeCandles(marketId: string, timeframe: Timeframe = '1m') {
  const [candles, setCandles] = useState<Candle[]>([])
  const [loading, setLoading] = useState(true)
  const [isDemoData] = useState(false)
  const lastSignatureRef = useRef<string>('')
  const mountedRef = useRef<boolean>(true)

  const fetchCandles = useCallback(async (isInitial = false) => {
    if (!marketId) return
    try {
      if (isInitial) setLoading(true)
      const data = await marketApi.getCandles(marketId, timeframe, 100)
      if (!mountedRef.current) return

      if (Array.isArray(data) && data.length > 0) {
        const last = data[data.length - 1]
        const sig = `${data.length}_${last.start_time}_${last.open}_${last.high}_${last.low}_${last.close}_${last.volume}`
        if (sig !== lastSignatureRef.current) {
          lastSignatureRef.current = sig
          setCandles(data)
        }
      } else {
        setCandles([])
        lastSignatureRef.current = ''
      }
    } catch (err) {
      if (!mountedRef.current) return
      console.warn(`Failed to fetch candles for ${marketId}`, err)
      setCandles([])
      lastSignatureRef.current = ''
    } finally {
      if (mountedRef.current && isInitial) setLoading(false)
    }
  }, [marketId, timeframe])

  useEffect(() => {
    mountedRef.current = true

    // 1. Immediately reset state on market or timeframe switch (Market Isolation)
    setCandles([])
    setLoading(true)
    lastSignatureRef.current = ''

    // Initial load
    fetchCandles(true)

    // Periodic synchronization every 5s
    const interval = setInterval(() => {
      fetchCandles(false)
    }, 5000)

    return () => {
      mountedRef.current = false
      clearInterval(interval)
    }
  }, [marketId, timeframe, fetchCandles])

  return {
    candles,
    loading,
    isDemoData,
    refetch: () => fetchCandles(false),
  }
}
