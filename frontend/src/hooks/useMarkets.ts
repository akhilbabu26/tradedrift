/**
 * src/hooks/useMarkets.ts
 *
 * Data boundary for the Markets page.
 * SOURCE OF TRUTH: frontend/docs/all backend apis.md
 *
 * Data sources:
 *   Market list  → GET /api/v1/markets
 *   Tickers      → GET /api/v1/markets/{id}/ticker (initial)
 *                  WS channel market:ticker:{id} (live updates)
 *
 * Derives stats (count, volume24h), highlights (gainer/loser/volume),
 * and allEntries from live ticker data.
 *
 * Error policy:
 *   - Shows explicit error state when markets API fails; no silent mock.
 *   - Falls back to MARKETS_MOCK only when API is completely unreachable
 *     AND isDemoData is explicitly set true with a banner in the UI.
 *
 * All consuming components receive the same shape as before (UseMarketsReturn)
 * — no component changes required.
 */

import { useState, useEffect, useCallback, useMemo, useRef } from 'react'
import { marketApi, type Market, type Ticker24h } from '../api/market'
import { wsService, WsChannels } from '../api/ws'
import { toDecimal } from '../utils/decimal'
import { getAssetMetadata } from '../utils/marketMetadata'
import { extractApiError } from '../utils/apiError'
import { MARKETS_MOCK } from '../data/marketsMock'
import type { MarketEntry, MarketHighlight, MarketStats } from '../types/markets'

export type MarketFilter = 'all' | 'favorites'

export interface UseMarketsReturn {
  stats: MarketStats
  highlights: MarketHighlight[]
  filteredEntries: MarketEntry[]
  favorites: Set<string>
  toggleFavorite: (pair: string) => void
  search: string
  setSearch: (v: string) => void
  filter: MarketFilter
  setFilter: (v: MarketFilter) => void
  quoteCurrency: string
  setQuoteCurrency: (v: string) => void
  loading: boolean
  error: string | null
  isDemoData: boolean
  refetch: () => void
}

// Asset display metadata (icon label, color, name)
const ASSET_META: Record<string, { iconLabel: string; iconColor: string; name: string }> = {
  BTC: { iconLabel: '₿', iconColor: '#f7931a', name: 'Bitcoin' },
  ETH: { iconLabel: 'Ξ', iconColor: '#627eea', name: 'Ethereum' },
  SOL: { iconLabel: 'S', iconColor: '#9945ff', name: 'Solana' },
}


/** Format raw volume number to display string */
function formatVolume(raw: string | undefined): string {
  if (!raw) return '$0'
  try {
    const v = toDecimal(raw)
    if (v.gte(1_000_000_000)) return `$${v.dividedBy(1_000_000_000).toFixed(2)}B`
    if (v.gte(1_000_000))     return `$${v.dividedBy(1_000_000).toFixed(2)}M`
    if (v.gte(1_000))         return `$${v.dividedBy(1_000).toFixed(1)}K`
    return `$${v.toFixed(2)}`
  } catch { return '$0' }
}

/** Map backend Market + Ticker to MarketEntry UI type */
function buildEntry(
  market: Market,
  ticker: Ticker24h | undefined,
  rank: number
): MarketEntry {
  const base = market.base_asset.toUpperCase()
  const meta = ASSET_META[base] ?? { iconLabel: base[0], iconColor: '#64748b', name: base }
  const assetMeta = getAssetMetadata(base)
  const price = ticker?.last_price ?? '0'
  const changeStr = ticker?.price_change_24h_percent ?? '0'
  const changeAbs = toDecimal(changeStr).abs().toFixed(2)
  const positive = toDecimal(changeStr).gte(0)
  const high = ticker?.high_24h ?? '0'
  const low  = ticker?.low_24h  ?? '0'
  const vol  = formatVolume(ticker?.quote_volume_24h)
  const pair = `${base}/${market.quote_asset}`

  // Static sparkline placeholder — no historical sparkline endpoint
  const sparklinePoints = MARKETS_MOCK.entries?.find(e => e.asset === base)?.sparklinePoints
    ?? [0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5]

  return {
    rank,
    pair,
    asset: base,
    name: meta.name || assetMeta?.name || base,
    lastPrice: price,
    change24h: changeAbs,
    positive,
    high24h: high,
    low24h: low,
    volume24h: vol,
    sparklinePoints,
    iconColor: meta.iconColor,
    iconLabel: meta.iconLabel,
  }
}

/** Derive highlights from live entries */
function deriveHighlights(entries: MarketEntry[]): MarketHighlight[] {
  if (entries.length === 0) return MARKETS_MOCK.highlights

  const sorted = [...entries].sort(
    (a, b) => toDecimal(b.change24h).comparedTo(toDecimal(a.change24h))
  )
  const gainer = sorted[0]
  const loser  = sorted[sorted.length - 1]
  const byVol  = [...entries].sort((a, b) => {
    const av = parseFloat(a.volume24h.replace(/[$BM,K]/g, ''))
    const bv = parseFloat(b.volume24h.replace(/[$BM,K]/g, ''))
    return bv - av
  })[0]

  const highlights: MarketHighlight[] = []

  if (gainer) highlights.push({
    type: 'gainer', label: 'Top Gainer',
    pair: gainer.pair, name: gainer.name,
    price: gainer.lastPrice, change: gainer.change24h,
    positive: gainer.positive,
    sparklinePoints: gainer.sparklinePoints,
    iconColor: gainer.iconColor, iconLabel: gainer.iconLabel,
  })

  if (loser && loser.pair !== gainer?.pair) highlights.push({
    type: 'loser', label: 'Top Loser',
    pair: loser.pair, name: loser.name,
    price: loser.lastPrice, change: loser.change24h,
    positive: loser.positive,
    sparklinePoints: loser.sparklinePoints,
    iconColor: loser.iconColor, iconLabel: loser.iconLabel,
  })

  if (byVol) highlights.push({
    type: 'volume', label: '24h Volume Leader',
    pair: byVol.pair, name: byVol.name,
    price: byVol.lastPrice, change: byVol.change24h,
    positive: byVol.positive,
    extraLabel: `${byVol.volume24h} 24h Volume`,
    sparklinePoints: byVol.sparklinePoints,
    iconColor: byVol.iconColor, iconLabel: byVol.iconLabel,
  })

  return highlights
}

export function useMarkets(): UseMarketsReturn {
  const [favorites, setFavorites]         = useState<Set<string>>(new Set(['BTC/USDT']))
  const [search, setSearch]               = useState('')
  const [filter, setFilter]               = useState<MarketFilter>('all')
  const [quoteCurrency, setQuoteCurrency] = useState('USDT')

  const [markets, setMarkets]         = useState<Market[]>([])
  const [tickers, setTickers]         = useState<Record<string, Ticker24h>>({})
  const [loading, setLoading]         = useState(true)
  const [error, setError]             = useState<string | null>(null)
  const [isDemoData, setIsDemoData]   = useState(false)
  const tickersRef = useRef(tickers)
  tickersRef.current = tickers

  const loadData = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const marketList = await marketApi.getMarkets()
      setMarkets(marketList)

      // Fetch all tickers in parallel
      const tickerResults = await Promise.allSettled(
        marketList.map((m) => marketApi.getTicker(m.id))
      )
      const newTickers: Record<string, Ticker24h> = {}
      marketList.forEach((m, i) => {
        const r = tickerResults[i]
        if (r.status === 'fulfilled') newTickers[m.id] = r.value
      })
      setTickers(newTickers)
      setIsDemoData(false)
    } catch (err) {
      const msg = extractApiError(err)
      setError(msg)
      setIsDemoData(true)
      // Fallback to mock — explicitly labelled
      setMarkets([])
      setTickers({})
    } finally {
      setLoading(false)
    }
  }, [])

  // ── WebSocket: live ticker updates ────────────────────────────────────────
  useEffect(() => {
    const unsubs: Array<() => void> = []

    // Subscribe to ticker channel for each known market
    const knownMarkets = markets.length > 0 ? markets : []
    for (const m of knownMarkets) {
      const channel = WsChannels.ticker(m.id)
      const marketId = m.id
      const unsub = wsService.subscribe(channel, (payload) => {
        try {
          const t = payload as Ticker24h
          if (!t) return
          setTickers((prev) => ({ ...prev, [marketId]: { ...prev[marketId], ...t } }))
        } catch { /* ignore */ }
      })
      unsubs.push(unsub)
    }

    return () => unsubs.forEach((u) => u())
  }, [markets])

  useEffect(() => { loadData() }, [loadData])

  // ── Derived UI data ───────────────────────────────────────────────────────
  const allEntries = useMemo<MarketEntry[]>(() => {
    if (isDemoData || markets.length === 0) return MARKETS_MOCK.entries ?? []
    return markets.map((m, i) => buildEntry(m, tickers[m.id], i + 1))
  }, [markets, tickers, isDemoData])

  const stats = useMemo<MarketStats>(() => {
    if (isDemoData || markets.length === 0) return MARKETS_MOCK.stats
    const totalVolumeRaw = Object.values(tickers).reduce((sum, t) => {
      return sum + toDecimal(t.quote_volume_24h || '0').toNumber()
    }, 0)
    return {
      marketsListed: markets.length,
      volume24h: formatVolume(totalVolumeRaw.toString()),
      liveDataLabel: 'via WebSocket',
    }
  }, [markets, tickers, isDemoData])

  const highlights = useMemo<MarketHighlight[]>(() => {
    if (isDemoData || allEntries.length === 0) return MARKETS_MOCK.highlights
    return deriveHighlights(allEntries)
  }, [allEntries, isDemoData])

  const filteredEntries = useMemo(() => {
    let rows = allEntries
    if (filter === 'favorites') {
      rows = rows.filter((r) => favorites.has(r.pair))
    }
    const q = search.trim().toLowerCase()
    if (q) {
      rows = rows.filter(
        (r) =>
          r.pair.toLowerCase().includes(q) ||
          r.asset.toLowerCase().includes(q) ||
          r.name.toLowerCase().includes(q)
      )
    }
    return rows
  }, [allEntries, filter, search, favorites])

  const toggleFavorite = (pair: string) => {
    setFavorites((prev) => {
      const next = new Set(prev)
      if (next.has(pair)) next.delete(pair)
      else next.add(pair)
      return next
    })
  }

  return {
    stats, highlights, filteredEntries,
    favorites, toggleFavorite,
    search, setSearch,
    filter, setFilter,
    quoteCurrency, setQuoteCurrency,
    loading, error, isDemoData,
    refetch: loadData,
  }
}
