import { Star, ArrowRight } from 'lucide-react'
import { Link } from 'react-router-dom'
import type { MarketEntry } from '../../types/markets'
import { formatPrice } from '../../utils/formatters'
import Sparkline from '../dashboard/shared/Sparkline'

interface MarketsTableProps {
  entries: MarketEntry[]
  favorites: Set<string>
  onToggleFavorite: (pair: string) => void
  /** Used to show the appropriate empty state */
  filter: 'all' | 'favorites'
  hasSearch: boolean
}

/** Coin icon — same pattern as MarketPulse (colored circle + letter label). */
function CoinIcon({ color, label }: { color: string; label: string }) {
  return (
    <div
      className="w-8 h-8 rounded-full border flex items-center justify-center flex-shrink-0 text-xs font-bold"
      style={{
        backgroundColor: `${color}20`,
        borderColor:      `${color}40`,
        color,
      }}
    >
      {label}
    </div>
  )
}

/**
 * Full markets table with columns:
 * ☆ | # | Asset | Last Price | 24h Change | 24h High / Low | 24h Volume | 7D Trend | Action
 *
 * Search empty state: "No markets found — Try adjusting your search."
 * Favorites empty state: "No favorites yet — Star an asset to add it here."
 */
export default function MarketsTable({
  entries,
  favorites,
  onToggleFavorite,
  filter,
  hasSearch,
}: MarketsTableProps) {
  return (
    <div className="bg-[#111318] border border-[#1e2530] rounded-lg overflow-hidden mb-4">
      <div className="overflow-x-auto">
        <table className="w-full text-xs" aria-label="Markets table">
          {/* ── Header ─────────────────────────────────────────────────────── */}
          <thead>
            <tr className="border-b border-[#1e2530] text-slate-500 text-[10px] uppercase tracking-wider">
              {/* Favorite */}
              <th className="w-10 py-3 pl-4 pr-2 text-center font-medium">
                <Star size={11} className="mx-auto" aria-label="Favorites column" />
              </th>
              {/* Rank */}
              <th className="w-8 py-3 pr-4 text-right font-medium">#</th>
              {/* Asset */}
              <th className="py-3 pr-4 text-left font-medium">Asset</th>
              {/* Last Price */}
              <th className="py-3 pr-4 text-right font-medium">Last Price</th>
              {/* 24h Change */}
              <th className="py-3 pr-4 text-right font-medium">24h Change</th>
              {/* High / Low */}
              <th className="py-3 pr-4 text-right font-medium">24h High / Low</th>
              {/* Volume */}
              <th className="py-3 pr-4 text-right font-medium">24h Volume</th>
              {/* 7D Trend */}
              <th className="py-3 pr-4 text-right font-medium">7D Trend</th>
              {/* Action */}
              <th className="py-3 pr-4 text-right font-medium">Action</th>
            </tr>
          </thead>

          {/* ── Body ───────────────────────────────────────────────────────── */}
          <tbody className="divide-y divide-[#1e2530]/40">
            {entries.length === 0 ? (
              <tr>
                <td colSpan={9} className="py-12 text-center">
                  <div className="flex flex-col items-center gap-1.5">
                    <span className="text-sm font-medium text-slate-400">
                      {filter === 'favorites' && !hasSearch
                        ? 'No favorites yet'
                        : 'No markets found'}
                    </span>
                    <span className="text-xs text-slate-600">
                      {filter === 'favorites' && !hasSearch
                        ? 'Star an asset to add it here.'
                        : 'Try adjusting your search.'}
                    </span>
                  </div>
                </td>
              </tr>
            ) : (
              entries.map((row) => {
                const isFav      = favorites.has(row.pair)
                const changeColor = row.positive ? 'text-[#10b981]' : 'text-[#ef4444]'
                const changeBg    = row.positive
                  ? 'bg-[#10b981]/10 border-[#10b981]/20'
                  : 'bg-[#ef4444]/10 border-[#ef4444]/20'

                return (
                  <tr
                    key={row.pair}
                    className="hover:bg-white/[0.02] transition-colors"
                  >
                    {/* Favorite toggle */}
                    <td className="py-3.5 pl-4 pr-2 text-center">
                      <button
                        type="button"
                        id={`markets-fav-${row.asset}`}
                        onClick={() => onToggleFavorite(row.pair)}
                        aria-label={`${isFav ? 'Remove' : 'Add'} ${row.pair} ${isFav ? 'from' : 'to'} favorites`}
                        className="p-0.5 rounded transition-colors hover:text-amber-400"
                      >
                        <Star
                          size={13}
                          className={
                            isFav
                              ? 'fill-amber-400 text-amber-400'
                              : 'text-slate-600 hover:text-slate-400'
                          }
                        />
                      </button>
                    </td>

                    {/* Rank */}
                    <td className="py-3.5 pr-4 text-right font-mono text-slate-500">
                      {row.rank}
                    </td>

                    {/* Asset */}
                    <td className="py-3.5 pr-4">
                      <div className="flex items-center gap-2.5">
                        <CoinIcon color={row.iconColor} label={row.iconLabel} />
                        <div className="flex flex-col leading-tight">
                          <span className="font-semibold text-[#f5f7fa] text-[11px] tracking-wide">
                            {row.asset}{' '}
                            <span className="text-slate-500 font-normal">/ USDT</span>
                          </span>
                          <span className="text-[10px] text-slate-500 mt-0.5">{row.name}</span>
                        </div>
                      </div>
                    </td>

                    {/* Last Price */}
                    <td className="py-3.5 pr-4 text-right font-mono text-[#f5f7fa] font-semibold tabular-nums">
                      ${formatPrice(row.lastPrice)}
                    </td>

                    {/* 24h Change */}
                    <td className="py-3.5 pr-4 text-right">
                      <span
                        className={`inline-flex items-center gap-0.5 px-1.5 py-0.5 rounded border text-[11px] font-bold ${changeBg} ${changeColor}`}
                      >
                        {row.positive ? '▲' : '▼'} {row.positive ? '+' : '-'}{row.change24h}%
                      </span>
                    </td>

                    {/* 24h High / Low */}
                    <td className="py-3.5 pr-4 text-right">
                      <div className="flex flex-col items-end leading-tight gap-0.5">
                        <span className="font-mono text-slate-300 tabular-nums">
                          ${formatPrice(row.high24h)}
                        </span>
                        <span className="font-mono text-slate-500 tabular-nums">
                          ${formatPrice(row.low24h)}
                        </span>
                      </div>
                    </td>

                    {/* 24h Volume */}
                    <td className="py-3.5 pr-4 text-right font-mono text-slate-300 tabular-nums">
                      {row.volume24h}
                    </td>

                    {/* 7D Sparkline */}
                    <td className="py-3.5 pr-4 text-right">
                      <div className="flex justify-end">
                        <Sparkline
                          points={row.sparklinePoints}
                          positive={row.positive}
                          width={72}
                          height={26}
                        />
                      </div>
                    </td>

                    {/* Action */}
                    <td className="py-3.5 pr-4 text-right">
                      <Link
                        to={`/trade?market=${row.pair.replace('/', '-')}`}
                        id={`markets-trade-${row.asset}`}
                        className="inline-flex items-center gap-1 px-3 py-1.5 rounded-md bg-[#10b981]/10 border border-[#10b981]/30 text-[#10b981] text-[11px] font-semibold hover:bg-[#10b981]/20 transition-colors whitespace-nowrap"
                      >
                        Trade <ArrowRight size={10} />
                      </Link>
                    </td>
                  </tr>
                )
              })
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
