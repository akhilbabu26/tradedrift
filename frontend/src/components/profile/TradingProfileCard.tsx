import { TrendingUp, BarChart3, Coins, Target } from 'lucide-react'
import type { TradingProfileMetrics } from '../../types/profile'

interface TradingProfileCardProps {
  metrics: TradingProfileMetrics
}

export default function TradingProfileCard({ metrics }: TradingProfileCardProps) {
  return (
    <div className="bg-[#111318] border border-[#1e2530] rounded-xl p-5 flex flex-col justify-between transition-colors">
      {/* Header */}
      <div className="flex items-center justify-between mb-4">
        <div>
          <div className="flex items-center gap-2">
            <div className="w-2 h-2 rounded-full bg-emerald-500" />
            <h2 className="text-sm font-semibold text-[#f5f7fa] tracking-tight">Trading Profile</h2>
          </div>
          <p className="text-xs text-slate-400 mt-0.5">Your activity and performance at a glance.</p>
        </div>
      </div>

      {/* 4 Metric Cards Grid */}
      <div className="grid grid-cols-2 gap-3">
        {/* Total Trades */}
        <div className="bg-[#0a0b0e] border border-[#1e2530] rounded-lg p-3.5 hover:border-slate-700 transition-colors">
          <div className="flex items-center justify-between text-slate-400 mb-1.5">
            <span className="text-[11px] font-medium">Total Trades</span>
            <BarChart3 size={13} className="text-slate-500" />
          </div>
          <div className="text-lg font-bold text-[#f5f7fa] font-mono tracking-tight">
            {metrics.totalTrades}
          </div>
          <span className="text-[10px] text-emerald-400 mt-1 inline-block">
            +27.3% activity
          </span>
        </div>

        {/* Total Volume */}
        <div className="bg-[#0a0b0e] border border-[#1e2530] rounded-lg p-3.5 hover:border-slate-700 transition-colors">
          <div className="flex items-center justify-between text-slate-400 mb-1.5">
            <span className="text-[11px] font-medium">Total Volume</span>
            <Coins size={13} className="text-slate-500" />
          </div>
          <div className="text-lg font-bold text-[#f5f7fa] font-mono tracking-tight truncate">
            {metrics.totalVolume} <span className="text-xs text-slate-400 font-sans">{metrics.totalVolumeUnit}</span>
          </div>
          <span className="text-[10px] text-emerald-400 mt-1 inline-block">
            +19.6% volume
          </span>
        </div>

        {/* Realized PnL */}
        <div className="bg-[#0a0b0e] border border-[#1e2530] rounded-lg p-3.5 hover:border-slate-700 transition-colors">
          <div className="flex items-center justify-between text-slate-400 mb-1.5">
            <span className="text-[11px] font-medium">Realized PnL</span>
            <TrendingUp size={13} className="text-[#10b981]" />
          </div>
          <div className="text-lg font-bold text-[#10b981] font-mono tracking-tight truncate">
            {metrics.realizedPnl} <span className="text-xs text-emerald-500/80 font-sans">{metrics.realizedPnlUnit}</span>
          </div>
          <span className="text-[10px] text-emerald-400 mt-1 inline-block">
            +12.4% net gain
          </span>
        </div>

        {/* Win Rate */}
        <div className="bg-[#0a0b0e] border border-[#1e2530] rounded-lg p-3.5 hover:border-slate-700 transition-colors">
          <div className="flex items-center justify-between text-slate-400 mb-1.5">
            <span className="text-[11px] font-medium">Win Rate</span>
            <Target size={13} className="text-emerald-400" />
          </div>
          <div className="text-lg font-bold text-[#f5f7fa] font-mono tracking-tight">
            {metrics.winRate}
          </div>
          <span className="text-[10px] text-slate-400 mt-1 inline-block">
            28 profitable fills
          </span>
        </div>
      </div>
    </div>
  )
}
