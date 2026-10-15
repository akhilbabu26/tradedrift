import { TrendingUp, TrendingDown, Minus } from 'lucide-react'
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
  const numChange = parseFloat(changeUsdt || '0')
  const numPercent = parseFloat((changePercent || '0').replace(/%/g, ''))

  const isZero = (isNaN(numChange) || numChange === 0) && (isNaN(numPercent) || numPercent === 0)
  const isPositive = !isZero && (numChange > 0 || (numChange === 0 && numPercent > 0))

  const colorClass = isZero ? 'text-slate-400' : isPositive ? 'text-[#10b981]' : 'text-[#ef4444]'

  return (
    <div className="flex flex-col gap-2 min-w-0">
      <p className="text-[11px] font-medium text-slate-400 uppercase tracking-wider">
        Total Portfolio Value
      </p>
      <p className="text-3xl font-bold text-[#f5f7fa] tracking-tight leading-none font-mono">
        {formatUSDT(portfolioValue)}
      </p>
      <div
        className={`flex items-center gap-1 text-sm font-semibold ${colorClass}`}
      >
        {isZero ? (
          <Minus size={14} aria-label="No change" />
        ) : isPositive ? (
          <TrendingUp size={14} aria-label="Positive change" />
        ) : (
          <TrendingDown size={14} aria-label="Negative change" />
        )}
        <span>
          {formatCompact(changeUsdt)} ({formatPercentage(changePercent)}) today
        </span>
      </div>
    </div>
  )
}
