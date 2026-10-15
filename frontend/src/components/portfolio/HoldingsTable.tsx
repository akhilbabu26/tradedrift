import { useNavigate } from 'react-router-dom'
import { MoreHorizontal, ArrowUpRight } from 'lucide-react'
import { toDecimal } from '../../utils/decimal'
import { formatPrice, formatUSDT } from '../../utils/formatters'
import type { PortfolioHolding } from '../../types/portfolio'
import Sparkline from '../common/Sparkline'

interface Props {
  holdings: PortfolioHolding[]
}

/**
 * Holdings table — financial data grid.
 * Columns: Asset | Holdings | Avg Cost | Current Price | Market Value | Unrealized PnL | Realized PnL | Weight | Action
 *
 * IMPORTANT: AVAX must NOT appear in this table unless passed via the holdings prop from the real API.
 * The mock holdings only contain: BTC, ETH, SOL, BNB, USDT.
 *
 * All text uses TradeDrift light tokens. No text-black/text-gray-900/text-slate-900 classes.
 */
export default function HoldingsTable({ holdings }: Props) {
  const navigate = useNavigate()

  const handleWheel = (e: React.WheelEvent<HTMLDivElement>) => {
    if (e.deltaY !== 0 && e.currentTarget.scrollWidth > e.currentTarget.clientWidth) {
      const { scrollLeft, scrollWidth, clientWidth } = e.currentTarget
      const maxScroll = scrollWidth - clientWidth
      const canScrollRight = e.deltaY > 0 && Math.ceil(scrollLeft) < maxScroll
      const canScrollLeft = e.deltaY < 0 && scrollLeft > 0

      if (canScrollRight || canScrollLeft || e.shiftKey) {
        e.currentTarget.scrollLeft += e.deltaY
      }
    }
  }

  function pnlColor(value: string): string {
    try {
      const sanitized = String(value || '0').replace(/[^\d.-]/g, '').replace(/^-?$/, '0')
      const d = toDecimal(sanitized)
      if (d.gt(0)) return 'text-[#10b981]'
      if (d.lt(0)) return 'text-[#ef4444]'
      return 'text-[#94a3b8]'
    } catch {
      return 'text-[#94a3b8]'
    }
  }

  if (holdings.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center py-12 text-center">
        <p className="text-sm text-[#475569]">No holdings match your current filters.</p>
        <p className="text-xs text-[#1e2530] mt-1">Try adjusting the search or filter options above.</p>
      </div>
    )
  }

  return (
    <div
      onWheel={handleWheel}
      className="w-full min-w-0 overflow-x-auto trade-scrollbar-x select-text"
      tabIndex={0}
      aria-label="Portfolio holdings table"
    >
      <table className="w-full min-w-[920px] text-xs">
        <thead>
          <tr className="border-b border-[#1e2530]">
            <th className="text-left py-3 px-3 text-[#64748b] font-medium whitespace-nowrap">Asset</th>
            <th className="text-right py-3 px-3 text-[#64748b] font-medium whitespace-nowrap">Holdings</th>
            <th className="text-right py-3 px-3 text-[#64748b] font-medium whitespace-nowrap">Avg Cost</th>
            <th className="text-right py-3 px-3 text-[#64748b] font-medium whitespace-nowrap">Current Price</th>
            <th className="text-right py-3 px-3 text-[#64748b] font-medium w-24 whitespace-nowrap">24h Trend</th>
            <th className="text-right py-3 px-3 text-[#64748b] font-medium whitespace-nowrap">Market Value</th>
            <th className="text-right py-3 px-3 text-[#64748b] font-medium whitespace-nowrap">Unrealized PnL</th>
            <th className="text-right py-3 px-3 text-[#64748b] font-medium whitespace-nowrap">Realized PnL</th>
            <th className="text-left py-3 px-3 text-[#64748b] font-medium w-28 whitespace-nowrap">Weight</th>
            <th className="text-right py-3 px-3 text-[#64748b] font-medium whitespace-nowrap">Action</th>
          </tr>
        </thead>
        <tbody>
          {holdings.map((h) => (
            <tr
              key={h.asset}
              className="border-b border-[#0f1117] hover:bg-[#0f1117] transition-colors group"
            >
              {/* Asset */}
              <td className="py-3.5 px-3 whitespace-nowrap">
                <div className="flex items-center gap-2.5">
                  <div
                    className={`w-8 h-8 rounded-full flex items-center justify-center text-sm font-bold border ${h.meta.iconBg} ${h.meta.iconBorder} ${h.meta.iconColor} flex-shrink-0`}
                  >
                    {h.meta.symbol}
                  </div>
                  <div>
                    <p className="text-[#f5f7fa] font-semibold">{h.asset}</p>
                    <p className="text-[#64748b] text-[10px]">{h.name}</p>
                  </div>
                </div>
              </td>

              {/* Holdings */}
              <td className="py-3.5 px-3 text-right whitespace-nowrap">
                <span className="text-[#cbd5e1] font-mono">
                  {h.quantity}
                </span>
                <span className="text-[#475569] ml-1">{h.asset}</span>
              </td>

              {/* Avg Cost */}
              <td className="py-3.5 px-3 text-right whitespace-nowrap">
                <span className="text-[#94a3b8] font-mono">
                  ${formatPrice(h.averageCost)}
                </span>
              </td>

              {/* Current Price */}
              <td className="py-3.5 px-3 text-right whitespace-nowrap">
                <span className="text-[#f5f7fa] font-mono font-medium">
                  ${formatPrice(h.currentPrice)}
                </span>
              </td>

              {/* 24h Trend */}
              <td className="py-3.5 px-3 text-right whitespace-nowrap">
                <div className="flex items-center justify-end">
                  {h.sparklinePoints && h.sparklinePoints.length > 0 ? (
                    <Sparkline
                      data={h.sparklinePoints}
                      width={80}
                      height={26}
                      ariaLabel={`${h.asset} 24h price trend`}
                    />
                  ) : (
                    <span className="text-[#475569] font-mono text-xs pr-2" title="No market pair">—</span>
                  )}
                </div>
              </td>

              {/* Market Value */}
              <td className="py-3.5 px-3 text-right whitespace-nowrap">
                <span className="text-[#cbd5e1] font-mono">
                  {formatUSDT(h.marketValue)}
                </span>
              </td>

              {/* Unrealized PnL */}
              <td className="py-3.5 px-3 text-right whitespace-nowrap">
                <div className="flex flex-col items-end">
                  <span className={`font-mono font-medium ${pnlColor(h.unrealizedPnl)}`}>
                    {h.unrealizedPnl.startsWith('+') || h.unrealizedPnl.startsWith('-')
                      ? h.unrealizedPnl
                      : h.unrealizedPnl}{' '}
                    USDT
                  </span>
                  <span className={`text-[10px] font-mono ${pnlColor(h.unrealizedPnlPct)}`}>
                    {h.unrealizedPnlPct}%
                  </span>
                </div>
              </td>

              {/* Realized PnL */}
              <td className="py-3.5 px-3 text-right whitespace-nowrap">
                <span className={`font-mono font-medium ${pnlColor(h.realizedPnl)}`}>
                  {h.realizedPnl} USDT
                </span>
              </td>

              {/* Weight */}
              <td className="py-3.5 px-3 w-28 whitespace-nowrap">
                <div className="flex flex-col gap-1">
                  <span className="text-[#94a3b8] font-mono text-[10px]">
                    {parseFloat(h.weightPct).toFixed(1)}%
                  </span>
                  <div className="h-1 bg-[#1e2530] rounded-full overflow-hidden w-20">
                    <div
                      className="h-full rounded-full"
                      style={{
                        width: `${Math.min(parseFloat(h.weightPct), 100)}%`,
                        backgroundColor: h.meta.hexColor,
                      }}
                    />
                  </div>
                </div>
              </td>

              {/* Action */}
              <td className="py-3.5 px-3 text-right whitespace-nowrap">
                <div className="flex items-center gap-1.5 justify-end">
                  <button
                    onClick={() =>
                      navigate(h.asset === 'USDT' ? '/trade?market=BTC-USDT' : `/trade?market=${h.asset}-USDT`)
                    }
                    className="flex items-center gap-1 px-2.5 py-1.5 bg-[#10b981]/10 border border-[#10b981]/30 text-[#10b981] rounded-lg text-[11px] font-medium hover:bg-[#10b981]/20 transition-colors"
                  >
                    <ArrowUpRight className="w-3 h-3" />
                    Trade
                  </button>
                  <button
                    className="p-1.5 text-[#475569] hover:text-[#94a3b8] hover:bg-[#1e2530] rounded-lg transition-colors"
                    aria-label={`More options for ${h.asset}`}
                  >
                    <MoreHorizontal className="w-3.5 h-3.5" />
                  </button>
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
