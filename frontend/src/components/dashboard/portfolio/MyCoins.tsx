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

// Single source of truth for column template across header and all data rows
const GRID_TEMPLATE =
  'grid grid-cols-[1.4fr_1fr_1.25fr_1.1fr_0.9fr_1.35fr] items-center gap-3 px-2'

/**
 * My Coins table — third column of Portfolio Overview summary row.
 * Compact grid table: Asset | Holdings | Value | PnL | PnL% | Allocation bar.
 * Uses exact matching CSS Grid definitions to ensure perfect horizontal alignment.
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

      {holdings.length === 0 ? (
        <div className="py-6 px-4 text-center flex flex-col items-center justify-center border border-dashed border-[#1e2530] rounded-md my-1">
          <p className="text-xs text-slate-300 font-medium mb-1">No crypto holdings yet</p>
          <p className="text-[11px] text-slate-500 mb-2">Simulator cash balance is 100% in USDT.</p>
          <Link
            to="/markets"
            className="text-xs text-[#10b981] hover:text-[#34d399] transition-colors"
          >
            Explore Spot Markets →
          </Link>
        </div>
      ) : (
        <div className="overflow-x-auto">
          <div className="min-w-[500px] w-full text-xs" role="table" aria-label="My Coins">
            {/* Header Row */}
            <div
              className={`${GRID_TEMPLATE} pb-2 text-slate-500 text-[10px] uppercase tracking-wider font-medium border-b border-[#1e2530]/40`}
              role="row"
            >
            <div role="columnheader" className="text-left">Asset</div>
            <div role="columnheader" className="text-right">Holdings</div>
            <div role="columnheader" className="text-right">Value (USDT)</div>
            <div role="columnheader" className="text-right">PnL (USDT)</div>
            <div role="columnheader" className="text-right">PnL %</div>
            <div role="columnheader" className="text-right">Allocation</div>
          </div>

          {/* Data Rows */}
          <div className="divide-y divide-[#1e2530]/40" role="rowgroup">
            {holdings.map((coin) => {
              const pnlNum = Number(coin.pnlUsdt)
              const pnlPercentNum = Number(coin.pnlPercent)
              const isPnlPositive = pnlNum >= 0
              const isPnlPercentPositive = pnlPercentNum >= 0

              return (
                <div
                  key={coin.asset}
                  className={`${GRID_TEMPLATE} py-2 hover:bg-white/[0.02] transition-colors`}
                  role="row"
                >
                  {/* 1. Asset */}
                  <div role="cell" className="flex items-center gap-1.5 min-w-0">
                    <span
                      className="w-5 h-5 rounded-full flex items-center justify-center text-[10px] font-bold flex-shrink-0"
                      style={{
                        backgroundColor: `${coin.color}25`,
                        color: coin.color,
                        border: `1px solid ${coin.color}40`,
                      }}
                    >
                      {coin.asset[0]}
                    </span>
                    <span className="font-semibold text-[#f5f7fa] truncate">{coin.asset}</span>
                  </div>

                  {/* 2. Holdings */}
                  <div role="cell" className="text-right font-mono text-slate-300 truncate">
                    {formatQuantity(coin.holdings)}
                  </div>

                  {/* 3. Value (USDT) */}
                  <div role="cell" className="text-right font-mono text-slate-300 truncate">
                    {formatPrice(coin.valueUsdt)}
                  </div>

                  {/* 4. PnL (USDT) */}
                  <div
                    role="cell"
                    className={`text-right font-mono font-medium truncate ${
                      isPnlPositive ? 'text-[#10b981]' : 'text-[#ef4444]'
                    }`}
                  >
                    {formatCompact(coin.pnlUsdt)}
                  </div>

                  {/* 5. PnL % */}
                  <div
                    role="cell"
                    className={`text-right font-mono font-medium truncate ${
                      isPnlPercentPositive ? 'text-[#10b981]' : 'text-[#ef4444]'
                    }`}
                  >
                    {formatPercentage(coin.pnlPercent)}
                  </div>

                  {/* 6. Allocation */}
                  <div role="cell" className="flex items-center gap-1.5 justify-end min-w-0">
                    <span className="text-slate-400 text-[10px] font-mono text-right flex-shrink-0">
                      {coin.allocation}%
                    </span>
                    <div className="w-14 h-1 rounded-full bg-[#0a0b0e] overflow-hidden flex-shrink-0">
                      <div
                        className="h-full rounded-full transition-all duration-500"
                        style={{
                          width: `${Math.min(100, coin.allocation * 4)}%`,
                          backgroundColor: coin.color,
                        }}
                      />
                    </div>
                  </div>
                </div>
              )
            })}
          </div>
        </div>
      </div>
    )}
  </div>
)
}
