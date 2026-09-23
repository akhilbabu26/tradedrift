import { formatCompact,  } from '../../../utils/formatters'

interface PortfolioMetricsProps {
  allTimeProfit: string
  winRate: number
  totalTrades: number
}

/**
 * Three horizontal metrics below the portfolio chart:
 * All-Time Profit | Win Rate | Total Trades
 */
export default function PortfolioMetrics({
  allTimeProfit,
  winRate,
  totalTrades,
}: PortfolioMetricsProps) {
  return (
    <div className="flex items-stretch border-t border-[#1e2530] pt-4 mt-1">
      {/* All-Time Profit */}
      <div className="flex-1 px-4 first:pl-0">
        <p className="text-[11px] font-medium text-slate-500 uppercase tracking-wider mb-1">
          All-Time Profit
        </p>
        <p className="text-lg font-bold text-[#10b981] font-mono">
          {formatCompact(allTimeProfit)} USDT
        </p>
      </div>

      <div className="w-px bg-[#1e2530]" aria-hidden="true" />

      {/* Win Rate */}
      <div className="flex-1 px-4">
        <p className="text-[11px] font-medium text-slate-500 uppercase tracking-wider mb-1">
          Win Rate
        </p>
        <p className="text-lg font-bold text-[#f5f7fa] font-mono">{winRate}%</p>
      </div>

      <div className="w-px bg-[#1e2530]" aria-hidden="true" />

      {/* Total Trades */}
      <div className="flex-1 px-4">
        <p className="text-[11px] font-medium text-slate-500 uppercase tracking-wider mb-1">
          Total Trades
        </p>
        <p className="text-lg font-bold text-[#f5f7fa] font-mono">{totalTrades}</p>
      </div>
    </div>
  )
}
