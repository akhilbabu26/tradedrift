import { useState, useEffect, useRef, useCallback } from 'react'
import axios from 'axios'
import { marketApi, type Candle } from '../api/market'
import type { Timeframe } from '../types/trade'

export function useTradeCandles(marketId: string, timeframe: Timeframe = '1m') {
  // Market-scoped candle state: guarantees state is strictly bound to marketId + timeframe
  const [candleState, setCandleState] = useState<{
    marketId: string
    timeframe: Timeframe
    candles: Candle[]
  }>({
    marketId: '',
    timeframe: '1m',
    candles: [],
  })

  const [loading, setLoading] = useState(true)
  const [isDemoData] = useState(false)

  const reqIdRef = useRef(0)
  const abortControllerRef = useRef<AbortController | null>(null)
  const pollTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const consecutiveErrorsRef = useRef(0)
  const lastSignatureRef = useRef<string>('')
  const mountedRef = useRef(true)

  const scheduleNextPoll = useCallback((delayMs: number, fetchFn: () => void) => {
    if (pollTimerRef.current) clearTimeout(pollTimerRef.current)
    if (!mountedRef.current) return
    pollTimerRef.current = setTimeout(() => {
      if (mountedRef.current) fetchFn()
    }, delayMs)
  }, [])

  const fetchCandles = useCallback(async (isInitial = false) => {
    if (!marketId) return

    // 1. Abort any previous in-flight request
    if (abortControllerRef.current) {
      abortControllerRef.current.abort()
    }
    const controller = new AbortController()
    abortControllerRef.current = controller

    // 2. Capture generation identifiers
    const thisReqId = ++reqIdRef.current
    const targetMarket = marketId
    const targetTf = timeframe

    if (isInitial) setLoading(true)

    try {
      const data = await marketApi.getCandles(targetMarket, targetTf, 500, controller.signal)

      // Guard: Stale response protection
      if (
        !mountedRef.current ||
        thisReqId !== reqIdRef.current ||
        targetMarket !== marketId ||
        targetTf !== timeframe
      ) {
        return
      }

      // Reset error backoff on successful response
      consecutiveErrorsRef.current = 0

      if (Array.isArray(data) && data.length > 0) {
        const last = data[data.length - 1]
        const sig = `${data.length}_${last.start_time}_${last.open}_${last.high}_${last.low}_${last.close}_${last.volume}`
        if (sig !== lastSignatureRef.current) {
          lastSignatureRef.current = sig
          setCandleState({
            marketId: targetMarket,
            timeframe: targetTf,
            candles: data,
          })
        }
      } else if (isInitial) {
        // Initial load for this market genuinely returned zero candles
        setCandleState({
          marketId: targetMarket,
          timeframe: targetTf,
          candles: [],
        })
        lastSignatureRef.current = ''
      }
      // Note: If not initial load and data is empty, preserve last-known-good candles

      // Schedule next poll on success: 5 seconds
      scheduleNextPoll(5000, () => fetchCandles(false))
    } catch (err: any) {
      // Ignore user-aborted / cancelled requests
      if (
        axios.isCancel(err) ||
        err?.name === 'CanceledError' ||
        err?.name === 'AbortError' ||
        controller.signal.aborted
      ) {
        return
      }

      // Guard: Stale response check
      if (
        !mountedRef.current ||
        thisReqId !== reqIdRef.current ||
        targetMarket !== marketId ||
        targetTf !== timeframe
      ) {
        return
      }

      consecutiveErrorsRef.current++
      console.warn(
        `[useTradeCandles] Failed to fetch candles for ${targetMarket} (attempt ${consecutiveErrorsRef.current})`,
        err
      )

      // INVARIANT: Last-known-good data must NEVER be destroyed by temporary network/429/500 failures!
      // Do NOT clear candles on fetch error.

      // Exponential backoff: 10s -> 20s -> 30s max
      const backoffMs = Math.min(30000, 5000 * Math.pow(2, consecutiveErrorsRef.current))
      scheduleNextPoll(backoffMs, () => fetchCandles(false))
    } finally {
      if (
        mountedRef.current &&
        isInitial &&
        thisReqId === reqIdRef.current &&
        targetMarket === marketId &&
        targetTf === timeframe
      ) {
        setLoading(false)
      }
    }
  }, [marketId, timeframe, scheduleNextPoll])

  useEffect(() => {
    mountedRef.current = true

    // On market switch or timeframe change:
    // 1. Abort previous in-flight requests and clear polling timers
    if (abortControllerRef.current) {
      abortControllerRef.current.abort()
    }
    if (pollTimerRef.current) {
      clearTimeout(pollTimerRef.current)
    }

    // 2. Reset tracking
    consecutiveErrorsRef.current = 0
    lastSignatureRef.current = ''
    setLoading(true)

    // 3. Clear previous market's candle state immediately (Market Isolation Invariant)
    setCandleState({
      marketId,
      timeframe,
      candles: [],
    })

    // 4. Trigger initial fetch
    fetchCandles(true)

    return () => {
      mountedRef.current = false
      if (abortControllerRef.current) {
        abortControllerRef.current.abort()
      }
      if (pollTimerRef.current) {
        clearTimeout(pollTimerRef.current)
      }
    }
  }, [marketId, timeframe, fetchCandles])

  const refetch = useCallback(() => {
    fetchCandles(false)
  }, [fetchCandles])

  // Guarantee: only return candles if they strictly belong to the currently active market and timeframe
  const candles =
    candleState.marketId === marketId && candleState.timeframe === timeframe
      ? candleState.candles
      : []

  return {
    candles,
    loading,
    isDemoData,
    refetch,
  }
}
