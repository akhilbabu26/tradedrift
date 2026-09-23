import { useState, useMemo } from 'react'
import type { MarketEntry, MarketHighlight, MarketStats } from '../types/markets'
import { MARKETS_MOCK } from '../data/marketsMock'

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
}

/**
 * Data boundary for the Markets page.
 *
 * Currently returns mock data from MARKETS_MOCK.
 * To switch to real data, replace the source of `stats`, `highlights`, and
 * `allEntries` with API / WebSocket calls — all consuming components remain
 * unchanged because they only depend on this hook's return type.
 *
 * Search matches:
 *   - pair      "BTC/USDT"
 *   - asset     "BTC"
 *   - name      "Bitcoin"
 *   (case-insensitive, partial match)
 */
export function useMarkets(): UseMarketsReturn {
  const [favorites, setFavorites]         = useState<Set<string>>(new Set(['BTC/USDT']))
  const [search, setSearch]               = useState('')
  const [filter, setFilter]               = useState<MarketFilter>('all')
  const [quoteCurrency, setQuoteCurrency] = useState('USDT')

  // ── Data source (swap with API/WS here) ─────────────────────────────────
  const stats:      MarketStats      = MARKETS_MOCK.stats
  const highlights: MarketHighlight[] = MARKETS_MOCK.highlights
  const allEntries: MarketEntry[]    = MARKETS_MOCK.entries

  const filteredEntries = useMemo(() => {
    let rows = allEntries

    // Favorites filter
    if (filter === 'favorites') {
      rows = rows.filter((r) => favorites.has(r.pair))
    }

    // Search filter: pair | asset | name
    const q = search.trim().toLowerCase()
    if (q) {
      rows = rows.filter(
        (r) =>
          r.pair.toLowerCase().includes(q) ||
          r.asset.toLowerCase().includes(q) ||
          r.name.toLowerCase().includes(q),
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
    stats,
    highlights,
    filteredEntries,
    favorites,
    toggleFavorite,
    search,
    setSearch,
    filter,
    setFilter,
    quoteCurrency,
    setQuoteCurrency,
  }
}
