import { TrendingUp, TrendingDown, BarChart2 } from 'lucide-react'
import type { MarketHighlight } from '../../types/markets'
import { formatPrice } from '../../utils/formatters'
import Sparkline from '../dashboard/shared/Sparkline'

interface MarketHighlightCardsProps {
  highlights: MarketHighlight[]
}

/**
 * Coin icon — colored circle with letter label. Matches the MarketPulse pattern
 * already used in the dashboard (iconBg + iconLabel convention).
 */
function CoinIcon({ color, label }: { color: string; label: string }) {
  return (
    <div
      className="w-9 h-9 rounded-full border flex items-center justify-center flex-shrink-0 text-sm font-bold"
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

function HighlightIcon({ type }: { type: MarketHighlight['type'] }) {
  if (type === 'gainer')
    return <TrendingUp size={13} className="text-[#10b981]" />
  if (type === 'loser')
    return <TrendingDown size={13} className="text-[#ef4444]" />
  return <BarChart2 size={13} className="text-[#3b82f6]" />
}

function HighlightLabelColor({ type }: { type: MarketHighlight['type'] }) {
  if (type === 'gainer') return 'text-[#10b981]'
  if (type === 'loser')  return 'text-[#ef4444]'
  return 'text-[#3b82f6]'
}

/**
 * Three highlight cards: Top Gainer, Top Loser, 24h Volume Leader.
 * Each card shows icon, pair, price, change badge, and a 7D sparkline.
 */
export default function MarketHighlightCards({ highlights }: MarketHighlightCardsProps) {
  return (
    <div className="grid grid-cols-1 md:grid-cols-3 gap-3 mb-4">
      {highlights.map((h) => {
        const changeColor = h.positive ? 'text-[#10b981]' : 'text-[#ef4444]'
        const changeBg    = h.positive ? 'bg-[#10b981]/10 border-[#10b981]/20' : 'bg-[#ef4444]/10 border-[#ef4444]/20'
        const labelColor  = HighlightLabelColor({ type: h.type })

        return (
          <div
            key={h.type}
            className="bg-[#111318] border border-[#1e2530] rounded-lg p-4"
          >
            {/* Card header */}
            <div className="flex items-center gap-1.5 mb-3">
              <HighlightIcon type={h.type} />
              <span className={`text-xs font-semibold ${labelColor}`}>{h.label}</span>
            </div>

            {/* Content row */}
            <div className="flex items-center justify-between gap-3">
              {/* Left: coin + pair info */}
              <div className="flex items-center gap-2.5 min-w-0">
                <CoinIcon color={h.iconColor} label={h.iconLabel} />
                <div className="min-w-0">
                  <div className="text-sm font-semibold text-[#f5f7fa] leading-tight whitespace-nowrap">
                    {h.pair}
                  </div>
                  <div className="text-[11px] text-slate-500 mt-0.5">{h.name}</div>
                  {/* Change badge or extra label */}
                  {h.extraLabel ? (
                    <div className="text-[11px] text-slate-400 mt-1">{h.extraLabel}</div>
                  ) : (
                    <div
                      className={`inline-flex items-center gap-1 mt-1.5 px-1.5 py-0.5 rounded text-[11px] font-bold border ${changeBg} ${changeColor}`}
                    >
                      {h.positive ? '▲' : '▼'} {h.positive ? '+' : '-'}{h.change}%
                    </div>
                  )}
                </div>
              </div>

              {/* Right: price + sparkline */}
              <div className="flex flex-col items-end gap-1 flex-shrink-0">
                <span className="text-[16px] font-bold text-[#f5f7fa] font-mono tabular-nums">
                  ${formatPrice(h.price)}
                </span>
                <Sparkline
                  points={h.sparklinePoints}
                  positive={h.positive}
                  width={80}
                  height={28}
                />
              </div>
            </div>
          </div>
        )
      })}
    </div>
  )
}
