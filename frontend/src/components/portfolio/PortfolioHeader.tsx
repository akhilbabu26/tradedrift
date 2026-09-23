import { TrendingUp, Quote } from 'lucide-react'

/**
 * Portfolio page header — title, subtitle, and right-side inspirational quote.
 * Text uses explicit TradeDrift light tokens — no dark/black inherited classes.
 */
export default function PortfolioHeader() {
  return (
    <div className="flex flex-col sm:flex-row sm:items-start sm:justify-between gap-4">
      {/* Left — title + subtitle */}
      <div>
        <div className="flex items-center gap-3 mb-1">
          <div className="w-8 h-8 rounded-lg bg-[#10b981]/15 border border-[#10b981]/30 flex items-center justify-center flex-shrink-0">
            <TrendingUp className="w-4 h-4 text-[#10b981]" />
          </div>
          <h1 className="text-2xl font-bold text-[#f5f7fa] leading-tight">
            Portfolio
          </h1>
        </div>
        <p className="text-sm text-[#94a3b8] ml-11">
          Track your performance and analyse your trading journey.
        </p>
      </div>

      {/* Right — quote */}
      <div className="flex items-start gap-2 sm:max-w-[340px] bg-[#111318] border border-[#1e2530] rounded-lg px-4 py-3 flex-shrink-0">
        <Quote className="w-4 h-4 text-[#10b981] flex-shrink-0 mt-0.5" />
        <p className="text-xs text-[#64748b] italic leading-relaxed">
          Discipline today, a better trader tomorrow.
        </p>
      </div>
    </div>
  )
}
