/**
 * src/hooks/useMarkets.ts
 *
 * Data boundary for the Markets page.
 * SOURCE OF TRUTH: frontend/docs/all backend apis.md
 *
 * Data sources:
 *   Market list  → GET /api/v1/markets
 *   Tickers      → GET /api/v1/markets/overview (initial enriched snapshot)
 *                  GET /api/v1/markets/{id}/ticker (fallback)
 *                  WS channel market:ticker:{id} (live updates)
 *   7D Trend     → GET /api/v1/markets/overview (trend array)
 *                  GET /api/v1/markets/{id}/candles?resolution=1h&limit=168 (fallback)
 *
 * Derives stats (count, volume24h, wsStatus), highlights (gainer/loser/volume),
 * and allEntries from live backend data.
 *
 * NO MOCK DATA FALLBACKS:
 *   - Clean empty/error states when backend is loading or unavailable.
 *   - Real numeric calculations with decimal safety.
 */

import { useState, useEffect, useMemo } from 'react'
import type { Market, Ticker24h } from '../api/market'
import { wsService, type ConnectionStatus } from '../api/ws'
import { toDecimal } from '../utils/decimal'
import { getAssetMetadata } from '../utils/marketMetadata'
import { evaluateTrendCoverage, type TrendCoverage } from '../utils/trendCoverage'
import { formatTurnover, formatQuantity } from '../utils/formatters'
import { useMarketsData } from '../store/marketStore'
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
export function normalizeTrend(trend: string[] | undefined, targetPoints = 16): number[] {
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

/** Map backend Market + Ticker to MarketEntry UI type */
function buildEntry(
  market: Market,
  ticker: Ticker24h | undefined,
  rank: number,
  trendPoints?: number[],
  coverage?: TrendCoverage
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
  const quoteVol = formatTurnover(ticker?.quote_volume_24h)
  const baseVolNum = parseFloat(ticker?.volume_24h || '0')
  const baseVolStr = baseVolNum > 0 ? `${formatQuantity(ticker?.volume_24h || '0', 2)} ${base}` : undefined
  const pair = `${base}/${market.quote_asset}`

  const sparklinePoints = (trendPoints && trendPoints.length > 0)
    ? trendPoints
    : (price && parseFloat(price) > 0 ? [parseFloat(price)] : [])

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
    volume24h: quoteVol,
    baseVolume24h: baseVolStr,
    quoteVolume24h: quoteVol,
    sparklinePoints,
    iconColor: meta.iconColor,
    iconLabel: meta.iconLabel,
    trendStatus: coverage?.status,
    trendLabel: coverage?.label,
    trendHours: coverage?.hoursCovered,
  }
}

/** Derive highlights from live entries */
function deriveHighlights(
  entries: MarketEntry[],
  tickers: Record<string, Ticker24h>
): MarketHighlight[] {
  if (entries.length === 0) return []

  const getSignedChange = (e: MarketEntry) => {
    const val = parseFloat(e.change24h) || 0
    return e.positive ? val : -val
  }

  // Top Performer / Top Gainer: highest signed change
  const sortedGainers = [...entries].sort((a, b) => getSignedChange(b) - getSignedChange(a))
  const gainer = sortedGainers[0]

  // Top Loser: lowest signed change
  const sortedLosers = [...entries].sort((a, b) => getSignedChange(a) - getSignedChange(b))
  const loser = sortedLosers.find((e) => e.pair !== gainer?.pair)

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
  const [wsStatus, setWsStatus]           = useState<ConnectionStatus>(() => wsService.getStatus())

  // Consume centralized market data & subscriptions from marketStore
  const { markets, tickers, trends, loading, error, refetch } = useMarketsData()

  // Track WebSocket connection status
  useEffect(() => {
    const unsub = wsService.onStatus((_connected, status) => {
      setWsStatus(status)
    })
    return () => {
      unsub()
    }
  }, [])

  // Derived UI entries
  const allEntries = useMemo<MarketEntry[]>(() => {
    if (markets.length === 0) return []
    return markets.map((m, i) => {
      const trendPoints = trends[m.id]
      const coverage = evaluateTrendCoverage(trendPoints?.length || 0)
      return buildEntry(m, tickers[m.id], i + 1, trendPoints, coverage)
    })
  }, [markets, tickers, trends])

  const stats = useMemo<MarketStats>(() => {
    let totalQuoteVol = toDecimal('0')
    Object.values(tickers).forEach((t) => {
      if (t.quote_volume_24h) {
        try {
          totalQuoteVol = totalQuoteVol.plus(toDecimal(t.quote_volume_24h))
        } catch { /* ignore */ }
      }
    })

    const isConnected = wsStatus === 'connected'
    const isConnecting = wsStatus === 'connecting' || wsStatus === 'reconnecting'

    return {
      marketsListed: markets.length,
      volume24h: formatTurnover(totalQuoteVol.toString()),
      liveDataLabel: isConnected ? 'via WebSocket' : isConnecting ? 'Connecting...' : 'Disconnected',
      wsStatus: isConnected ? 'live' : isConnecting ? 'connecting' : 'offline',
    }
  }, [markets, tickers, wsStatus])

  const highlights = useMemo<MarketHighlight[]>(() => {
    if (allEntries.length === 0) return []
    return deriveHighlights(allEntries, tickers)
  }, [allEntries, tickers])

  const filteredEntries = useMemo(() => {
    let rows = allEntries
    if (filter === 'favorites') {
      rows = rows.filter((r) => favorites.has(r.pair))
    }
    if (quoteCurrency && quoteCurrency !== 'ALL') {
      rows = rows.filter((r) => r.pair.endsWith(`/${quoteCurrency}`))
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
  }, [allEntries, filter, search, favorites, quoteCurrency])

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
    loading, error, isDemoData: false,
    refetch,
  }
}

