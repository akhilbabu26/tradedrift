import { FileText, BarChart3, Database, Lock, ArrowUpRight } from 'lucide-react'
import type { OrdersKPIs } from '../../types/orders'
import { formatPrice } from '../../utils/formatters'

interface OrdersStatsProps {
  kpis: OrdersKPIs
  loading?: boolean
}

export default function OrdersStats({ kpis, loading = false }: OrdersStatsProps) {
  if (loading) {
    return (
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        {[1, 2, 3, 4].map((i) => (
          <div
            key={i}
            className="rounded-xl border border-[#1e2530] bg-[#111318] p-5 h-28 animate-pulse"
          />
        ))}
      </div>
    )
  }

  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4 select-none">
      {/* 1. Active Orders */}
      <div className="rounded-xl border border-[#1e2530] bg-[#111318] p-5 flex items-start gap-4 shadow-sm hover:border-[#1e2530]/80 transition-colors">
        <div className="w-11 h-11 rounded-xl bg-white/[0.03] border border-white/10 flex items-center justify-center flex-shrink-0 text-slate-300">
          <FileText size={18} />
        </div>
        <div className="flex flex-col min-w-0">
          <span className="text-xs text-slate-400 font-medium">Active Orders</span>
          <div className="mt-1 flex items-baseline gap-2">
            <span className="text-2xl font-bold font-mono text-[#f5f7fa] tracking-tight">
              {kpis.activeOrders}
            </span>
          </div>
          <span className="text-[11px] text-slate-500 mt-1 truncate">
            Open orders on the order book
          </span>
        </div>
      </div>

      {/* 2. Today's Executions */}
      <div className="rounded-xl border border-[#1e2530] bg-[#111318] p-5 flex items-start gap-4 shadow-sm hover:border-[#1e2530]/80 transition-colors">
        <div className="w-11 h-11 rounded-xl bg-white/[0.03] border border-white/10 flex items-center justify-center flex-shrink-0 text-slate-300">
          <BarChart3 size={18} />
        </div>
        <div className="flex flex-col min-w-0">
          <span className="text-xs text-slate-400 font-medium">Today's Executions</span>
          <div className="mt-1 flex items-center gap-2">
            <span className="text-2xl font-bold font-mono text-[#f5f7fa] tracking-tight">
              {kpis.todayExecutions}
            </span>
            {kpis.todayExecutionsChange && (
              <span className="inline-flex items-center text-xs font-mono font-semibold text-[#10b981]">
                <ArrowUpRight size={13} className="stroke-[2.5]" />
                {kpis.todayExecutionsChange}
              </span>
            )}
          </div>
          <span className="text-[11px] text-slate-500 mt-1 truncate">
            Trades filled today
          </span>
        </div>
      </div>

      {/* 3. 24h Traded Volume */}
      <div className="rounded-xl border border-[#1e2530] bg-[#111318] p-5 flex items-start gap-4 shadow-sm hover:border-[#1e2530]/80 transition-colors">
        <div className="w-11 h-11 rounded-xl bg-white/[0.03] border border-white/10 flex items-center justify-center flex-shrink-0 text-slate-300">
          <Database size={18} />
        </div>
        <div className="flex flex-col min-w-0">
          <span className="text-xs text-slate-400 font-medium">24h Traded Volume</span>
          <div className="mt-1 flex items-baseline gap-1.5 flex-wrap">
            <span className="text-2xl font-bold font-mono text-[#f5f7fa] tracking-tight">
              ${formatPrice(kpis.tradedVolume24h)}
            </span>
            <span className="text-xs font-semibold text-slate-400">USDT</span>
            {kpis.tradedVolume24hChange && (
              <span className="inline-flex items-center text-xs font-mono font-semibold text-[#10b981] ml-1">
                <ArrowUpRight size={13} className="stroke-[2.5]" />
                {kpis.tradedVolume24hChange}
              </span>
            )}
          </div>
          <span className="text-[11px] text-slate-500 mt-1 truncate">
            Total executed volume (24h)
          </span>
        </div>
      </div>

      {/* 4. Funds Locked */}
      <div className="rounded-xl border border-[#1e2530] bg-[#111318] p-5 flex items-start gap-4 shadow-sm hover:border-[#1e2530]/80 transition-colors">
        <div className="w-11 h-11 rounded-xl bg-[#10b981]/10 border border-[#10b981]/20 flex items-center justify-center flex-shrink-0 text-[#10b981]">
          <Lock size={18} />
        </div>
        <div className="flex flex-col min-w-0">
          <span className="text-xs text-slate-400 font-medium">Funds Locked</span>
          <div className="mt-1 flex items-baseline gap-1.5 flex-wrap">
            <span className="text-2xl font-bold font-mono text-[#f5f7fa] tracking-tight">
              ${formatPrice(kpis.fundsLocked)}
            </span>
            <span className="text-xs font-semibold text-slate-400">USDT</span>
          </div>
          <span className="text-[11px] text-slate-500 mt-1 truncate">
            Across {kpis.fundsLockedOrderCount} open order{kpis.fundsLockedOrderCount === 1 ? '' : 's'}
          </span>
        </div>
      </div>
    </div>
  )
}
