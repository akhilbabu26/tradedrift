import { Layers, BarChart2, Wifi } from 'lucide-react'
import type { MarketStats } from '../../types/markets'
import StatusIndicator from '../dashboard/shared/StatusIndicator'

interface MarketStatsStripProps {
  stats: MarketStats
}

/**
 * Three-item summary strip shown in the top-right area of the Markets page header.
 * Mirrors the reference screenshot layout: Markets Listed | 24h Volume | Live Data.
 */
export default function MarketStatsStrip({ stats }: MarketStatsStripProps) {
  return (
    <div className="flex items-center gap-0 divide-x divide-[#1e2530]">
      {/* Markets Listed */}
      <div className="flex items-center gap-2.5 pr-6">
        <div className="w-7 h-7 rounded bg-[#111318] border border-[#1e2530] flex items-center justify-center flex-shrink-0">
          <Layers size={13} className="text-[#10b981]" />
        </div>
        <div className="flex flex-col leading-tight">
          <span className="text-sm font-bold text-[#f5f7fa] font-mono tabular-nums">
            {stats.marketsListed}
          </span>
          <span className="text-[10px] text-slate-500 font-medium uppercase tracking-wider">
            Markets Listed
          </span>
        </div>
      </div>

      {/* 24h Volume */}
      <div className="flex items-center gap-2.5 px-6">
        <div className="w-7 h-7 rounded bg-[#111318] border border-[#1e2530] flex items-center justify-center flex-shrink-0">
          <BarChart2 size={13} className="text-[#10b981]" />
        </div>
        <div className="flex flex-col leading-tight">
          <span className="text-sm font-bold text-[#f5f7fa] font-mono tabular-nums">
            {stats.volume24h}
          </span>
          <span className="text-[10px] text-slate-500 font-medium uppercase tracking-wider">
            24h Volume (USDT)
          </span>
        </div>
      </div>

      {/* Live Data */}
      <div className="flex items-center gap-2.5 pl-6">
        <div className="w-7 h-7 rounded bg-[#111318] border border-[#1e2530] flex items-center justify-center flex-shrink-0">
          <Wifi size={13} className="text-[#10b981]" />
        </div>
        <div className="flex flex-col leading-tight">
          <div className="flex items-center gap-1.5">
            <StatusIndicator
              status={stats.wsStatus ?? 'live'}
              showPing={stats.wsStatus === 'live' || stats.wsStatus === 'online' || !stats.wsStatus}
            />
            <span className="text-sm font-bold text-[#f5f7fa]">
              {stats.wsStatus === 'offline' ? 'Offline' : stats.wsStatus === 'connecting' ? 'Connecting' : 'Live Data'}
            </span>
          </div>
          <span className="text-[10px] text-slate-500 font-medium">
            {stats.liveDataLabel}
          </span>
        </div>
      </div>
    </div>
  )
}
