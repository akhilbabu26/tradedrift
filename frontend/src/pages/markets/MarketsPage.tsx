import { useMarkets } from '../../hooks/useMarkets'
import MarketStatsStrip     from '../../components/markets/MarketStatsStrip'
import MarketHighlightCards from '../../components/markets/MarketHighlightCards'
import MarketControls       from '../../components/markets/MarketControls'
import MarketsTable         from '../../components/markets/MarketsTable'
import MarketsFooter        from '../../components/markets/MarketsFooter'

/**
 * Markets page — authenticated route at /markets.
 *
 * Layout is provided by MainLayout (App.tsx) via <Outlet />.
 * This component renders only Markets-specific page content.
 *
 * Data flows through useMarkets(), which currently reads from marketsMock.
 * Replacing mock data with real API + WebSocket calls requires only updating
 * useMarkets() — all sub-components are decoupled from the data source.
 */
export default function MarketsPage() {
  const {
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
  } = useMarkets()

  return (
    <div className="flex-1 overflow-y-auto">
    <div className="max-w-[1600px] mx-auto px-4 lg:px-6 py-5">

      {/* ── Page Header ─────────────────────────────────────────────────────── */}
      <div className="flex flex-col sm:flex-row sm:items-start sm:justify-between gap-4 mb-5">
        <div>
          <h1 className="text-xl font-bold text-[#f5f7fa] tracking-tight">Markets</h1>
          <p className="text-xs text-slate-500 mt-1">
            Explore digital assets. Real-time prices, trends and market data.
          </p>
        </div>

        {/* Stats strip — top right of header row on ≥sm */}
        <div className="flex-shrink-0">
          <MarketStatsStrip stats={stats} />
        </div>
      </div>

      {/* ── Highlight Cards ──────────────────────────────────────────────────── */}
      <MarketHighlightCards highlights={highlights} />

      {/* ── Controls ────────────────────────────────────────────────────────── */}
      <MarketControls
        search={search}
        onSearchChange={setSearch}
        filter={filter}
        onFilterChange={setFilter}
        quoteCurrency={quoteCurrency}
        onQuoteCurrencyChange={setQuoteCurrency}
      />

      {/* ── Markets Table ────────────────────────────────────────────────────── */}
      <MarketsTable
        entries={filteredEntries}
        favorites={favorites}
        onToggleFavorite={toggleFavorite}
        filter={filter}
        hasSearch={search.trim().length > 0}
      />

      {/* ── Footer ──────────────────────────────────────────────────────────── */}
      <MarketsFooter />

    </div>
    </div>
  )
}
