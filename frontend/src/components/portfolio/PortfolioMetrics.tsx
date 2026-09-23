import { Eye, EyeOff, TrendingUp, TrendingDown, Info } from 'lucide-react'
import { useState } from 'react'
import { formatUSDT, formatPercentage, formatCompact } from '../../utils/formatters'
import { toDecimal } from '../../utils/decimal'
import type { PortfolioExecutiveMetrics } from '../../types/portfolio'

interface Props {
  metrics: PortfolioExecutiveMetrics
  loading: boolean
}

export default function PortfolioMetrics({ metrics, loading }: Props) {
  const [hideTotalValue, setHideTotalValue] = useState(false)

  function pnlColor(value: string): string {
    try {
      const sanitized = String(value || '0').replace(/[^\d.\-]/g, '').replace(/^-?$/, '0')
      const d = toDecimal(sanitized)
      if (d.gt(0)) return 'text-[#10b981]'
      if (d.lt(0)) return 'text-[#ef4444]'
      return 'text-[#f5f7fa]'
    } catch {
      return 'text-[#f5f7fa]'
    }
  }

  const sk = (w = 'w-24', h = 'h-6') => (
    <div className={w + ' ' + h + ' bg-[#1e2530] animate-pulse rounded'} />
  )

  return (
    <div className="bg-[#111318] border border-[#1e2530] rounded-xl overflow-hidden">
      <div className="grid grid-cols-2 lg:grid-cols-4 divide-x divide-[#1e2530] divide-y lg:divide-y-0">

        {/* Total Portfolio Value */}
        <div className="px-5 py-4 flex flex-col gap-1">
          <div className="flex items-center justify-between">
            <span className="text-xs text-[#94a3b8] font-medium">Total Portfolio Value</span>
            <button onClick={() => setHideTotalValue(v => !v)} className="text-[#475569] hover:text-[#94a3b8] transition-colors">
              {hideTotalValue ? <EyeOff className="w-3.5 h-3.5" /> : <Eye className="w-3.5 h-3.5" />}
            </button>
          </div>
          {loading ? <>{sk('w-36', 'h-7')}{sk('w-28', 'h-4')}</> : (
            <>
              <p className="text-[22px] font-bold text-[#f5f7fa] font-mono leading-tight tracking-tight">
                {hideTotalValue ? '------' : formatUSDT(metrics.totalValue)}
              </p>
              <div className="flex items-center gap-1.5">
                {toDecimal(metrics.dailyChangeValue || '0').gte(0)
                  ? <TrendingUp className="w-3 h-3 text-[#10b981] flex-shrink-0" />
                  : <TrendingDown className="w-3 h-3 text-[#ef4444] flex-shrink-0" />}
                <span className={'text-xs font-mono font-medium ' + pnlColor(metrics.dailyChangeValue)}>
                  {formatCompact(metrics.dailyChangeValue)} ({formatPercentage(metrics.dailyChangePct)})
                </span>
              </div>
            </>
          )}
        </div>

        {/* Unrealized PnL */}
        <div className="px-5 py-4 flex flex-col gap-1">
          <div className="flex items-center gap-1.5">
            <span className="text-xs text-[#94a3b8] font-medium">Unrealized PnL</span>
            <Info className="w-3 h-3 text-[#475569]" />
          </div>
          {loading ? <>{sk('w-32', 'h-7')}{sk('w-20', 'h-4')}</> : (
            <>
              <p className={'text-[22px] font-bold font-mono leading-tight tracking-tight ' + pnlColor(metrics.unrealizedPnl)}>
                {formatCompact(metrics.unrealizedPnl)} <span className="text-sm font-normal text-[#94a3b8]">USDT</span>
              </p>
              <span className={'text-xs font-mono font-medium ' + pnlColor(metrics.unrealizedPnlPct)}>
                ({formatPercentage(metrics.unrealizedPnlPct)})
              </span>
              <span className="text-[11px] text-[#475569]">Open holdings</span>
            </>
          )}
        </div>

        {/* Realized PnL */}
        <div className="px-5 py-4 flex flex-col gap-1">
          <div className="flex items-center gap-1.5">
            <span className="text-xs text-[#94a3b8] font-medium">Realized PnL</span>
            <Info className="w-3 h-3 text-[#475569]" />
          </div>
          {loading ? <>{sk('w-32', 'h-7')}{sk('w-24', 'h-4')}</> : (
            <>
              <p className={'text-[22px] font-bold font-mono leading-tight tracking-tight ' + pnlColor(metrics.realizedPnl)}>
                {formatCompact(metrics.realizedPnl)} <span className="text-sm font-normal text-[#94a3b8]">USDT</span>
              </p>
              <span className="text-[11px] text-[#475569]">All-time closed trades</span>
            </>
          )}
        </div>

        {/* 24h PnL */}
        <div className="px-5 py-4 flex flex-col gap-1">
          <div className="flex items-center gap-1.5">
            <span className="text-xs text-[#94a3b8] font-medium">24h PnL</span>
            <Info className="w-3 h-3 text-[#475569]" />
          </div>
          {loading ? <>{sk('w-32', 'h-7')}{sk('w-20', 'h-4')}</> : (
            <>
              <p className={'text-[22px] font-bold font-mono leading-tight tracking-tight ' + pnlColor(metrics.pnl24h)}>
                {formatCompact(metrics.pnl24h)} <span className="text-sm font-normal text-[#94a3b8]">USDT</span>
              </p>
              <span className={'text-xs font-mono font-medium ' + pnlColor(metrics.pnl24hPct)}>
                ({formatPercentage(metrics.pnl24hPct)})
              </span>
            </>
          )}
        </div>

      </div>
    </div>
  )
}