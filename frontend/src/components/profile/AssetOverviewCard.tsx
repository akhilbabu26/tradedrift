import { PieChart } from 'lucide-react'
import type { ProfileAssetAllocation } from '../../types/profile'

interface AssetOverviewCardProps {
  assets: ProfileAssetAllocation[]
}

export default function AssetOverviewCard({ assets }: AssetOverviewCardProps) {
  return (
    <div className="bg-[#111318] border border-[#1e2530] rounded-xl p-5 flex flex-col justify-between transition-colors">
      {/* Header */}
      <div className="flex items-center justify-between mb-4">
        <div>
          <div className="flex items-center gap-2">
            <PieChart size={16} className="text-emerald-400" />
            <h2 className="text-sm font-semibold text-[#f5f7fa] tracking-tight">Asset Overview</h2>
          </div>
          <p className="text-xs text-slate-400 mt-0.5">
            Supported asset holdings and portfolio weights.
          </p>
        </div>
        <span className="text-[11px] font-mono text-slate-400 bg-[#0a0b0e] border border-[#1e2530] px-2 py-0.5 rounded">
          4 Assets
        </span>
      </div>

      {/* Multi-segment Allocation Bar */}
      <div className="mb-4">
        <div className="w-full h-2.5 bg-[#0a0b0e] rounded-full overflow-hidden flex border border-[#1e2530]/50">
          {assets.map((item) => (
            <div
              key={item.asset}
              style={{
                width: `${item.weightPct}%`,
                backgroundColor: item.meta.hexColor,
              }}
              className="h-full transition-all duration-300"
              title={`${item.asset}: ${item.weightPct}%`}
            />
          ))}
        </div>
      </div>

      {/* Asset List Rows */}
      <div className="space-y-2.5">
        {assets.map((item) => (
          <div
            key={item.asset}
            className="flex items-center justify-between p-2.5 bg-[#0a0b0e] border border-[#1e2530] rounded-lg hover:border-slate-700 transition-colors"
          >
            {/* Asset Identity */}
            <div className="flex items-center gap-2.5">
              <div
                className={`w-7 h-7 rounded-md ${item.meta.iconBg} ${item.meta.iconBorder} border flex items-center justify-center font-bold text-xs ${item.meta.iconColor}`}
              >
                {item.meta.symbol}
              </div>
              <div>
                <span className="text-xs font-semibold text-[#f5f7fa]">{item.asset}</span>
                <span className="text-[11px] text-slate-400 ml-1.5 hidden sm:inline">
                  {item.meta.name}
                </span>
              </div>
            </div>

            {/* Balance & Weight */}
            <div className="text-right flex items-center gap-3">
              <span className="text-xs font-mono font-medium text-slate-200">
                {item.balance}
              </span>
              <span
                className="text-[11px] font-mono font-semibold px-1.5 py-0.5 rounded text-right min-w-[38px]"
                style={{
                  color: item.meta.hexColor,
                  backgroundColor: `${item.meta.hexColor}15`,
                }}
              >
                {item.weightPct}%
              </span>
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
