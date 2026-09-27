import { useState, useEffect } from 'react'
import { wsService, WsChannels } from '../api/ws'
import type { OrderBookSnapshot, OrderBookLevel } from '../types/trade'
import { getMockOrderBook } from '../data/tradeMock'
import { toDecimal } from '../utils/decimal'

function processLevels(rawLevels: [string, string][], reverse = false): OrderBookLevel[] {
  let runningTotal = toDecimal(0)
  const levels: { price: string; quantity: string; total: string; numTotal: number }[] = []

  const sorted = [...rawLevels].sort((a, b) => {
    const pA = parseFloat(a[0])
    const pB = parseFloat(b[0])
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

const EMPTY_ORDER_BOOK: OrderBookSnapshot = {
  asks: [],
  bids: [],
  spread: '0.00',
  spreadPercent: '0.000',
  lastPrice: '0.00',
}

export function useTradeOrderBook(marketId: string) {
  const [orderBook, setOrderBook] = useState<OrderBookSnapshot>(EMPTY_ORDER_BOOK)
  const [isDemoData, setIsDemoData] = useState(false)
  const [precision, setPrecision] = useState(2)

  useEffect(() => {
    let mounted = true
    setIsDemoData(false)
    setOrderBook(EMPTY_ORDER_BOOK)

    const stream = WsChannels.orderbook(marketId)
    const unsubscribe = wsService.subscribe(stream, (data: any) => {
      if (!mounted) return
      if (data && (data.bids || data.asks)) {
        try {
          const rawBids: [string, string][] = data.bids || []
          const rawAsks: [string, string][] = data.asks || []

          const processedBids = processLevels(rawBids, false).slice(0, 8)
          const processedAsks = processLevels(rawAsks, true).slice(0, 8)

          const bestBid = processedBids[0]?.price ? parseFloat(processedBids[0].price) : 0
          const bestAsk = processedAsks[0]?.price ? parseFloat(processedAsks[0].price) : 0
          const spread = bestAsk > 0 && bestBid > 0 ? (bestAsk - bestBid).toFixed(precision) : '0.00'
          const spreadPercent =
            bestBid > 0 && bestAsk > 0 ? (((bestAsk - bestBid) / bestBid) * 100).toFixed(3) : '0.000'
          const lastPrice = data.last_price || (bestBid > 0 ? bestBid.toFixed(precision) : '0.00')

          setOrderBook({
            asks: processedAsks,
            bids: processedBids,
            spread,
            spreadPercent,
            lastPrice,
          })
          setIsDemoData(false)
        } catch (err) {
          console.error('Error processing orderbook stream', err)
        }
      }
    })

    // Timeout fallback: if no WS event received within 1500ms, mark as demo fallback
    const timer = setTimeout(() => {
      if (mounted) {
        setIsDemoData(true)
      }
    }, 1500)

    return () => {
      mounted = false
      clearTimeout(timer)
      unsubscribe()
    }
  }, [marketId, precision])

  return {
    orderBook,
    isDemoData,
    precision,
    setPrecision,
  }
}
