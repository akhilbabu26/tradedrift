import { Flame, Sparkles, Activity } from 'lucide-react'
import type { ProfileInsightItem } from '../../types/profile'
import { getAssetMetadata } from '../../utils/marketMetadata'

interface ProfileInsightsSectionProps {
  insights: ProfileInsightItem[]
}

export default function ProfileInsightsSection({ insights }: ProfileInsightsSectionProps) {
  const btcMeta = getAssetMetadata('BTC')
  const solMeta = getAssetMetadata('SOL')

  return (
    <div>
      <div className="flex items-center gap-2 mb-3">
        <Sparkles size={15} className="text-emerald-400" />
        <h3 className="text-xs font-semibold text-slate-300 uppercase tracking-wider">
          Trading Profile Insights
        </h3>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-3 gap-3.5">
        {insights.map((item) => {
          let icon = <Activity size={16} className="text-emerald-400" />
          let assetBadge = null

          if (item.id === 'most-traded') {
            icon = <Flame size={16} className="text-[#f7931a]" />
            assetBadge = (
              <span className={`w-6 h-6 rounded ${btcMeta.iconBg} ${btcMeta.iconBorder} border flex items-center justify-center text-xs font-bold ${btcMeta.iconColor}`}>
                {btcMeta.symbol}
              </span>
            )
          } else if (item.id === 'best-performing') {
            icon = <Sparkles size={16} className="text-[#9945ff]" />
            assetBadge = (
              <span className={`w-6 h-6 rounded ${solMeta.iconBg} ${solMeta.iconBorder} border flex items-center justify-center text-xs font-bold ${solMeta.iconColor}`}>
                {solMeta.symbol}
              </span>
            )
          }

          return (
            <div
              key={item.id}
              className="p-4 bg-[#111318] border border-[#1e2530] rounded-xl hover:border-slate-700 transition-colors flex items-start justify-between gap-3"
            >
              <div>
                <span className="block text-[11px] font-medium text-slate-400 mb-1">
                  {item.label}
                </span>
                <div className="flex items-center gap-2">
                  <span className="text-base font-bold text-[#f5f7fa] font-mono">
                    {item.value}
                  </span>
                  {item.badge && (
                    <span className="text-[10px] font-medium px-2 py-0.5 rounded-full bg-emerald-500/10 text-emerald-400 border border-emerald-500/20">
                      {item.badge}
                    </span>
                  )}
                </div>
                {item.subValue && (
                  <p className="text-[11px] text-slate-400 mt-1">
                    {item.subValue}
                  </p>
                )}
              </div>

              <div className="flex-shrink-0 mt-0.5">
                {assetBadge || (
                  <div className="w-7 h-7 rounded bg-[#0a0b0e] border border-[#1e2530] flex items-center justify-center">
                    {icon}
                  </div>
                )}
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}
