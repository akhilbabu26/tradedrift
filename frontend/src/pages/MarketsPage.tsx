import { useEffect, useState, useCallback } from 'react'
import AppLayout from '../components/layout/AppLayout'
import MarketHighlightCards from '../components/markets/MarketHighlightCards'
import SpotMarketsTable from '../components/markets/SpotMarketsTable'
import { marketApi, type Ticker24h } from '../api/market'
import { wsService } from '../api/ws'

export default function MarketsPage() {
  const [liveTickers, setLiveTickers] = useState<Record<string, Ticker24h>>({})

  // Fetch initial tickers for all supported pairs
  const fetchTickers = useCallback(async () => {
    const pairs = ['BTC-USDT', 'ETH-USDT', 'SOL-USDT']
    try {
      const results = await Promise.allSettled(pairs.map((id) => marketApi.getTicker(id)))
      const map: Record<string, Ticker24h> = {}
      results.forEach((res, index) => {
        if (res.status === 'fulfilled' && res.value) {
          map[pairs[index]] = res.value
        }
      })
      setLiveTickers(map)
    } catch {
      // Keep initial reference values
    }
  }, [])

  useEffect(() => {
    fetchTickers()

    // Subscribe to live WebSocket ticker channels
    const pairs = ['BTC-USDT', 'ETH-USDT', 'SOL-USDT']
    const unsubs = pairs.map((id) =>
      wsService.subscribe(`market:ticker:${id}`, (ticker: Ticker24h) => {
        if (ticker) {
          setLiveTickers((prev) => ({
            ...prev,
            [id]: ticker,
          }))
        }
      })
    )

    return () => {
      unsubs.forEach((unsub) => unsub())
    }
  }, [fetchTickers])

  return (
    <AppLayout>
      <div className="flex flex-col space-y-6 max-w-[1920px] mx-auto select-none pb-12">
        {/* ── 1. Market Highlight Cards (Top Gainer, 24h Volume Leader, New Listing) ── */}
        <MarketHighlightCards tickers={liveTickers} />

        {/* ── 2. Spot Markets Section (Categories, Table, Sparklines, Pagination) ── */}
        <SpotMarketsTable liveTickers={liveTickers} />
      </div>
    </AppLayout>
  )
}
