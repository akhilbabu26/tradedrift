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
import { marketApi, type Market, type Ticker24h, type MarketOverview } from '../api/market'
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

const FAVORITES_STORAGE_KEY = 'tradedrift_market_favorites'

function loadSavedFavorites(): Set<string> {
  try {
    const raw = localStorage.getItem(FAVORITES_STORAGE_KEY)
    if (raw) {
      const arr = JSON.parse(raw)
      if (Array.isArray(arr) && arr.length > 0) return new Set(arr)
    }
  } catch { /* ignore */ }
  return new Set(['BTC/USDT'])
}

/** Normalize array of price string points into 0..1 values for Sparkline */
function normalizeTrend(trend: string[] | undefined, targetPoints = 16): number[] {
  if (!trend || trend.length < 2) return [0.5, 0.5]
  const nums = trend.map((v) => parseFloat(v)).filter((v) => !isNaN(v) && v > 0)
  if (nums.length < 2) return [0.5, 0.5]

  let sampled: number[] = []
  if (nums.length <= targetPoints) {
    sampled = nums
  } else {
    const step = (nums.length - 1) / (targetPoints - 1)
    for (let i = 0; i < targetPoints; i++) {
      const idx = Math.min(Math.round(i * step), nums.length - 1)
      sampled.push(nums[idx])
    }
  }

  const min = Math.min(...sampled)
  const max = Math.max(...sampled)
  const range = max - min
  if (range === 0) return sampled.map(() => 0.5)
  return sampled.map((v) => Math.round(((v - min) / range) * 1000) / 1000)
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
  rank: number,
  trendPoints?: number[]
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

  const sparklinePoints = trendPoints
    ?? MARKETS_MOCK.entries?.find(e => e.asset === base)?.sparklinePoints
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
function deriveHighlights(
  entries: MarketEntry[],
  tickers: Record<string, Ticker24h>
): MarketHighlight[] {
  if (entries.length === 0) return MARKETS_MOCK.highlights

  const getSignedChange = (e: MarketEntry) => {
    const val = parseFloat(e.change24h) || 0
    return e.positive ? val : -val
  }

  // Top Gainer: highest signed change
  const sortedGainers = [...entries].sort((a, b) => getSignedChange(b) - getSignedChange(a))
  const gainer = sortedGainers[0]

  // Top Loser: lowest signed change
  const sortedLosers = [...entries].sort((a, b) => getSignedChange(a) - getSignedChange(b))
  const loser = sortedLosers.find((e) => e.pair !== gainer?.pair) || sortedLosers[0]

  // 24h Volume Leader: highest quote volume from real numeric ticker data
  const byVol = [...entries].sort((a, b) => {
    const marketIdA = a.pair.replace('/', '-')
    const marketIdB = b.pair.replace('/', '-')
    const volA = toDecimal(tickers[marketIdA]?.quote_volume_24h || '0')
    const volB = toDecimal(tickers[marketIdB]?.quote_volume_24h || '0')
    return volB.comparedTo(volA)
  })[0]

  const highlights: MarketHighlight[] = []

  if (gainer) {
    const isNegative = getSignedChange(gainer) < 0
    highlights.push({
      type: 'gainer',
      label: isNegative ? 'Top Performer' : 'Top Gainer',
      pair: gainer.pair,
      name: gainer.name,
      price: gainer.lastPrice,
      change: gainer.change24h,
      positive: gainer.positive,
      sparklinePoints: gainer.sparklinePoints,
      iconColor: gainer.iconColor,
      iconLabel: gainer.iconLabel,
    })
  }

  if (loser && loser.pair !== gainer?.pair) {
    highlights.push({
      type: 'loser',
      label: 'Top Loser',
      pair: loser.pair,
      name: loser.name,
      price: loser.lastPrice,
      change: loser.change24h,
      positive: loser.positive,
      sparklinePoints: loser.sparklinePoints,
      iconColor: loser.iconColor,
      iconLabel: loser.iconLabel,
    })
  }

  if (byVol) {
    highlights.push({
      type: 'volume',
      label: '24h Volume Leader',
      pair: byVol.pair,
      name: byVol.name,
      price: byVol.lastPrice,
      change: byVol.change24h,
      positive: byVol.positive,
      extraLabel: `${byVol.volume24h} 24h Volume`,
      sparklinePoints: byVol.sparklinePoints,
      iconColor: byVol.iconColor,
      iconLabel: byVol.iconLabel,
    })
  }

  return highlights
}

export function useMarkets(): UseMarketsReturn {
  const [favorites, setFavorites]         = useState<Set<string>>(loadSavedFavorites)
  const [search, setSearch]               = useState('')
  const [filter, setFilter]               = useState<MarketFilter>('all')
  const [quoteCurrency, setQuoteCurrency] = useState('USDT')

  const [markets, setMarkets]         = useState<Market[]>([])
  const [tickers, setTickers]         = useState<Record<string, Ticker24h>>({})
  const [trends, setTrends]           = useState<Record<string, number[]>>({})
  const [loading, setLoading]         = useState(true)
  const [error, setError]             = useState<string | null>(null)
  const [isDemoData, setIsDemoData]   = useState(false)
  const tickersRef = useRef(tickers)
  tickersRef.current = tickers

  const loadData = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const [marketList, overviewList] = await Promise.all([
        marketApi.getMarkets(),
        marketApi.getMarketsOverview().catch(() => [] as MarketOverview[]),
      ])
      setMarkets(marketList)

      const newTickers: Record<string, Ticker24h> = {}
      const newTrends: Record<string, number[]> = {}

      overviewList.forEach((ov) => {
        newTickers[ov.market_id] = {
          market_id: ov.market_id,
          last_price: ov.last_price,
          high_24h: ov.high_24h,
          low_24h: ov.low_24h,
          volume_24h: ov.volume_24h,
          quote_volume_24h: ov.quote_volume_24h,
          price_change_24h_percent: ov.price_change_24h_percent,
        }
        if (ov.trend && ov.trend.length >= 2) {
          newTrends[ov.market_id] = normalizeTrend(ov.trend)
        }
      })

      // Fallback for any market missing from overview
      const missing = marketList.filter((m) => !newTickers[m.id])
      if (missing.length > 0) {
        const results = await Promise.allSettled(missing.map((m) => marketApi.getTicker(m.id)))
        missing.forEach((m, i) => {
          const r = results[i]
          if (r.status === 'fulfilled') newTickers[m.id] = r.value
        })
      }

      setTickers(newTickers)
      setTrends(newTrends)
      setIsDemoData(false)
    } catch (err) {
      const msg = extractApiError(err)
      setError(msg)
      setIsDemoData(true)
      // Fallback to mock — explicitly labelled
      setMarkets([])
      setTickers({})
      setTrends({})
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
          const raw = payload as any
          if (!raw) return
          const normalized: Partial<Ticker24h> = {
            market_id: marketId,
            last_price: raw.lastPrice ?? raw.last_price,
            high_24h: raw.high24h ?? raw.high_24h,
            low_24h: raw.low24h ?? raw.low_24h,
            volume_24h: raw.volume24h ?? raw.volume_24h,
            quote_volume_24h: raw.quoteVolume24h ?? raw.quote_volume_24h,
            price_change_24h_percent: raw.priceChange24hPercent ?? raw.price_change_24h_percent,
          }
          setTickers((prev) => ({
            ...prev,
            [marketId]: { ...prev[marketId], ...normalized },
          }))
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
    return markets.map((m, i) => buildEntry(m, tickers[m.id], i + 1, trends[m.id]))
  }, [markets, tickers, trends, isDemoData])

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
    return deriveHighlights(allEntries, tickers)
  }, [allEntries, tickers, isDemoData])

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
      try {
        localStorage.setItem(FAVORITES_STORAGE_KEY, JSON.stringify([...next]))
      } catch { /* ignore */ }
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
