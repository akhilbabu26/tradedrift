import { useState, useEffect, useMemo } from 'react'
import { wsService, WsChannels } from '../api/ws'
import type { OrderBookSnapshot, OrderBookLevel } from '../types/trade'
import { toDecimal } from '../utils/decimal'

function processLevels(rawLevels: [string, string][], reverse = false): OrderBookLevel[] {
  let runningTotal = toDecimal(0)
  const levels: { price: string; quantity: string; total: string; numTotal: number }[] = []

  const sorted = [...rawLevels].sort((a, b) => {
    const pA = parseFloat(a[0]) || 0
    const pB = parseFloat(b[0]) || 0
    return reverse ? pA - pB : pB - pA
  })

  for (const [p, q] of sorted) {
    const qty = toDecimal(q || 0)
    runningTotal = runningTotal.plus(qty)
    levels.push({
      price: p,
      quantity: qty.toFixed(4),
      total: runningTotal.toFixed(4),
      numTotal: runningTotal.toNumber(),
    })
  }

  const maxTotal = runningTotal.toNumber() || 1
  return levels.map((l) => ({
    price: l.price,
    quantity: l.quantity,
    total: l.total,
    depthPct: Math.min(100, Math.round((l.numTotal / maxTotal) * 100)),
  }))
}

export function useTradeOrderBook(marketId: string) {
  const [precision, setPrecision] = useState(2)
  const [isDemoData, setIsDemoData] = useState(false)
  const [rawSnapshot, setRawSnapshot] = useState<{
    rawBids: [string, string][]
    rawAsks: [string, string][]
    lastPrice: string
  }>({ rawBids: [], rawAsks: [], lastPrice: '0.00' })

  // 1. WS Subscription depends strictly on marketId (not precision)
  useEffect(() => {
    let mounted = true
    setIsDemoData(false)
    // Market Isolation: reset raw snapshot immediately on market switch
    setRawSnapshot({ rawBids: [], rawAsks: [], lastPrice: '0.00' })

    const stream = WsChannels.orderbook(marketId)
    const unsubscribe = wsService.subscribe(stream, (data: any) => {
      if (!mounted) return
      const incomingMarket = data?.market_id || data?.marketId
      if (incomingMarket && incomingMarket !== marketId) return

      if (data && (data.bids || data.asks)) {
        setRawSnapshot({
          rawBids: data.bids || [],
          rawAsks: data.asks || [],
          lastPrice: data.last_price || '0.00',
        })
        setIsDemoData(false)
      }
    })

    return () => {
      mounted = false
      unsubscribe()
    }
  }, [marketId])

  // 2. Format levels and spread on the fly according to current precision
  const orderBook = useMemo<OrderBookSnapshot>(() => {
    const { rawBids, rawAsks, lastPrice } = rawSnapshot
    const processedBids = processLevels(rawBids, false).slice(0, 8)
    const processedAsks = processLevels(rawAsks, true).slice(0, 8)

    const bestBid = processedBids[0]?.price ? parseFloat(processedBids[0].price) : 0
    const bestAsk = processedAsks[0]?.price ? parseFloat(processedAsks[0].price) : 0
    const spread = bestAsk > 0 && bestBid > 0 ? (bestAsk - bestBid).toFixed(precision) : '0.00'
    const spreadPercent =
      bestBid > 0 && bestAsk > 0 ? (((bestAsk - bestBid) / bestBid) * 100).toFixed(3) : '0.000'
    const finalLastPrice = lastPrice !== '0.00' ? lastPrice : (bestBid > 0 ? bestBid.toFixed(precision) : '0.00')

    return {
      marketId,
      asks: processedAsks,
      bids: processedBids,
      spread,
      spreadPercent,
      lastPrice: finalLastPrice,
    }
  }, [marketId, rawSnapshot, precision])

  return {
    orderBook,
    isDemoData,
    precision,
    setPrecision,
  }
}
