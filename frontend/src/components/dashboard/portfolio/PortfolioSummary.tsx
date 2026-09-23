import { TrendingUp } from 'lucide-react'
import { formatUSDT, formatCompact, formatPercentage } from '../../../utils/formatters'

interface PortfolioSummaryProps {
  portfolioValue: string
  changeUsdt: string
  changePercent: string
}

/**
 * Total Portfolio Value section — first column of the Portfolio Overview summary row.
 */
export default function PortfolioSummary({
  portfolioValue,
  changeUsdt,
  changePercent,
}: PortfolioSummaryProps) {
  const isPositive = !changePercent.startsWith('-')

  return (
    <div className="flex flex-col gap-2 min-w-0">
      <p className="text-[11px] font-medium text-slate-400 uppercase tracking-wider">
        Total Portfolio Value
      </p>
      <p className="text-3xl font-bold text-[#f5f7fa] tracking-tight leading-none font-mono">
        {formatUSDT(portfolioValue)}
      </p>
      <div
        className={`flex items-center gap-1 text-sm font-semibold ${
          isPositive ? 'text-[#10b981]' : 'text-[#ef4444]'
        }`}
      >
        <TrendingUp size={14} />
        <span>
          {formatCompact(changeUsdt)} ({formatPercentage(changePercent)}) today
        </span>
      </div>
    </div>
  )
}
