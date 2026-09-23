import { Lightbulb, TrendingUp, TrendingDown, DollarSign, Calendar, Quote } from 'lucide-react'
import { getAssetMetadata } from '../../utils/marketMetadata'
import { formatUSDT, formatPercentage } from '../../utils/formatters'
import { toDecimal } from '../../utils/decimal'
import type { PortfolioInsightsData } from '../../types/portfolio'

interface Props {
  insights: PortfolioInsightsData
}

/**
 * Portfolio Insights bottom bar.
 *
 * IMPORTANT: The "Underperformer" metric (AVAX -4.20% ROI) is a historical
 * closed-trade insight only. AVAX does NOT appear in the Holdings table —
 * this component is the only place AVAX is referenced in the portfolio page.
 *
 * All text uses TradeDrift light tokens. No dark inherited classes.
 */
export default function PortfolioInsights({ insights }: Props) {
  const topMeta = getAssetMetadata(insights.topPerformer.asset)
  const underMeta = getAssetMetadata(insights.underPerformer.asset)

  // Clean 7d percentage string to avoid duplicate ++ sign
  const raw7d = (insights.last7DaysPct || '0').replace(/^[+]/, '')
  const is7dPos = toDecimal(raw7d).gte(0)
  const display7d = `${is7dPos ? '+' : ''}${raw7d}%`

  return (
    <div className="bg-[#111318] border border-[#1e2530] rounded-xl overflow-hidden">
      <div className="flex flex-col lg:flex-row lg:items-center divide-y lg:divide-y-0 lg:divide-x divide-[#1e2530]">

        {/* Label */}
        <div className="flex items-center gap-2.5 px-5 py-3.5 flex-shrink-0 bg-[#0a0b0e]/30">
          <Lightbulb className="w-4 h-4 text-[#10b981] flex-shrink-0" />
          <span className="text-xs font-semibold text-[#f5f7fa] uppercase tracking-wider whitespace-nowrap">
            Portfolio Insights
          </span>
        </div>

        {/* Top Performer */}
        <div className="flex items-center gap-3 px-5 py-3.5 flex-1 min-w-0">
          <div className="w-7 h-7 rounded-lg bg-[#10b981]/10 flex items-center justify-center flex-shrink-0">
            <TrendingUp className="w-4 h-4 text-[#10b981]" />
          </div>
          <div className="min-w-0">
            <p className="text-[10px] text-[#94a3b8] uppercase tracking-wider font-medium">Top Performer</p>
            <div className="flex items-center gap-1.5 mt-0.5 whitespace-nowrap">
              <span className={'text-xs font-bold ' + topMeta.iconColor}>
                {insights.topPerformer.asset}
              </span>
              <span className="text-xs text-[#10b981] font-mono font-medium">
                (+{formatPercentage(insights.topPerformer.roiPct, false)} ROI)
              </span>
            </div>
          </div>
        </div>

        {/* Underperformer */}
        <div className="flex items-center gap-3 px-5 py-3.5 flex-1 min-w-0">
          <div className="w-7 h-7 rounded-lg bg-[#ef4444]/10 flex items-center justify-center flex-shrink-0">
            <TrendingDown className="w-4 h-4 text-[#ef4444]" />
          </div>
          <div className="min-w-0">
            <p className="text-[10px] text-[#94a3b8] uppercase tracking-wider font-medium">Underperformer</p>
            <div className="flex items-center gap-1.5 mt-0.5 whitespace-nowrap">
              <span className={'text-xs font-bold ' + underMeta.iconColor} title={underMeta.name + ' (closed position)'}>
                {insights.underPerformer.asset}
              </span>
              <span className="text-xs text-[#ef4444] font-mono font-medium">
                ({insights.underPerformer.roiPct}% ROI)
              </span>
            </div>
          </div>
        </div>

        {/* Total Invested */}
        <div className="flex items-center gap-3 px-5 py-3.5 flex-1 min-w-0">
          <div className="w-7 h-7 rounded-lg bg-[#1e2530] flex items-center justify-center flex-shrink-0">
            <DollarSign className="w-4 h-4 text-[#94a3b8]" />
          </div>
          <div className="min-w-0">
            <p className="text-[10px] text-[#94a3b8] uppercase tracking-wider font-medium whitespace-nowrap">Total Cost Basis</p>
            <p className="text-xs text-[#cbd5e1] font-mono font-medium mt-0.5 whitespace-nowrap">
              {formatUSDT(insights.totalInvested)}
            </p>
          </div>
        </div>

        {/* Last 7 Days */}
        <div className="flex items-center gap-3 px-5 py-3.5 flex-1 min-w-0">
          <div className="w-7 h-7 rounded-lg bg-[#1e2530] flex items-center justify-center flex-shrink-0">
            <Calendar className="w-4 h-4 text-[#94a3b8]" />
          </div>
          <div className="min-w-0">
            <p className="text-[10px] text-[#94a3b8] uppercase tracking-wider font-medium whitespace-nowrap">Last 7 Days</p>
            <p className={'text-xs font-mono font-medium mt-0.5 whitespace-nowrap ' + (is7dPos ? 'text-[#10b981]' : 'text-[#ef4444]')}>
              {display7d}
            </p>
          </div>
        </div>

        {/* Quote */}
        <div className="hidden xl:flex items-center gap-2 px-5 py-3.5 flex-shrink-0 max-w-[280px]">
          <Quote className="w-3.5 h-3.5 text-[#64748b] flex-shrink-0" />
          <p className="text-[11px] text-[#94a3b8] italic leading-relaxed">
            {insights.quote}
          </p>
        </div>

      </div>
    </div>
  )
}
