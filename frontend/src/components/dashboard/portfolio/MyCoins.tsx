import { ArrowRight } from 'lucide-react'
import { Link } from 'react-router-dom'
import type { CoinHolding } from '../../../types/dashboard'
import {
  formatQuantity,
  formatPrice,
  formatCompact,
  formatPercentage,
} from '../../../utils/formatters'

interface MyCoinsProps {
  holdings: CoinHolding[]
}

/**
 * My Coins table — third column of Portfolio Overview summary row.
 * Compact table: Asset | Holdings | Value | PnL | PnL% | Allocation bar.
 */
export default function MyCoins({ holdings }: MyCoinsProps) {
  return (
    <div className="flex flex-col gap-2 min-w-0 flex-1">
      <div className="flex items-center justify-between">
        <p className="text-[11px] font-medium text-slate-400 uppercase tracking-wider">
          My Coins ({holdings.length})
        </p>
        <Link
          to="/portfolio"
          className="flex items-center gap-0.5 text-[11px] text-[#10b981] hover:text-[#34d399] transition-colors"
        >
          View Portfolio <ArrowRight size={11} />
        </Link>
      </div>

      <div className="overflow-x-auto">
        <table className="w-full text-xs min-w-[480px]">
          <thead>
            <tr className="text-slate-500 text-[10px] uppercase tracking-wider">
              <th className="text-left pb-1.5 font-medium">Asset</th>
              <th className="text-right pb-1.5 font-medium">Holdings</th>
              <th className="text-right pb-1.5 font-medium">Value (USDT)</th>
              <th className="text-right pb-1.5 font-medium">PnL (USDT)</th>
              <th className="text-right pb-1.5 font-medium">PnL %</th>
              <th className="text-right pb-1.5 font-medium">Allocation</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[#1e2530]/40">
            {holdings.map((coin) => (
              <tr key={coin.asset} className="hover:bg-white/[0.02] transition-colors">
                {/* Asset */}
                <td className="py-1.5 pr-2">
                  <div className="flex items-center gap-1.5">
                    <span
                      className="w-5 h-5 rounded-full flex items-center justify-center text-[10px] font-bold flex-shrink-0"
                      style={{ backgroundColor: `${coin.color}25`, color: coin.color, border: `1px solid ${coin.color}40` }}
                    >
                      {coin.asset[0]}
                    </span>
                    <span className="font-medium text-[#f5f7fa]">{coin.asset}</span>
                  </div>
                </td>
                {/* Holdings */}
                <td className="py-1.5 text-right font-mono text-slate-300">
                  {formatQuantity(coin.holdings)}
                </td>
                {/* Value */}
                <td className="py-1.5 text-right font-mono text-slate-300">
                  {formatPrice(coin.valueUsdt)}
                </td>
                {/* PnL USDT */}
                <td className="py-1.5 text-right font-mono font-medium text-[#10b981]">
                  {formatCompact(coin.pnlUsdt)}
                </td>
                {/* PnL % */}
                <td className="py-1.5 text-right font-mono font-medium text-[#10b981]">
                  {formatPercentage(coin.pnlPercent)}
                </td>
                {/* Allocation bar */}
                <td className="py-1.5 pl-3">
                  <div className="flex items-center gap-1.5 justify-end">
                    <span className="text-slate-400 text-[10px] w-8 text-right">
                      {coin.allocation}%
                    </span>
                    <div className="w-14 h-1 rounded-full bg-[#0a0b0e] overflow-hidden">
                      <div
                        className="h-full rounded-full transition-all duration-500"
                        style={{
                          width: `${Math.min(100, coin.allocation * 4)}%`,
                          backgroundColor: coin.color,
                        }}
                      />
                    </div>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
