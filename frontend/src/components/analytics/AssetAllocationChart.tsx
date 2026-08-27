import { Info, Inbox } from 'lucide-react'

export interface AllocationItem {
  symbol: string
  name: string
  pct: number
  exposureUsd: number
  color: string
  iconBg: string
  iconText: string
  iconChar: string
}

interface AssetAllocationChartProps {
  allocations?: AllocationItem[]
  totalUsd?: number
}

const DEFAULT_ALLOCATIONS: AllocationItem[] = [
  {
    symbol: 'BTC',
    name: 'Bitcoin',
    pct: 0,
    exposureUsd: 0,
    color: '#00e676',
    iconBg: 'bg-[#f7931a]/15 border-[#f7931a]/30',
    iconText: 'text-[#f7931a]',
    iconChar: '₿',
  },
  {
    symbol: 'USDT',
    name: 'Tether',
    pct: 0,
    exposureUsd: 0,
    color: '#00e5ff',
    iconBg: 'bg-[#00e676]/15 border-[#00e676]/30',
    iconText: 'text-[#00e676]',
    iconChar: '₮',
  },
  {
    symbol: 'ETH',
    name: 'Ethereum',
    pct: 0,
    exposureUsd: 0,
    color: '#a855f7',
    iconBg: 'bg-[#627eea]/15 border-[#627eea]/30',
    iconText: 'text-[#627eea]',
    iconChar: 'Ξ',
  },
  {
    symbol: 'SOL',
    name: 'Solana',
    pct: 0,
    exposureUsd: 0,
    color: '#f59e0b',
    iconBg: 'bg-[#00e5ff]/15 border-[#00e5ff]/30',
    iconText: 'text-[#00e5ff]',
    iconChar: 'S',
  },
]

export default function AssetAllocationChart({
  allocations = DEFAULT_ALLOCATIONS,
  totalUsd = 0,
}: AssetAllocationChartProps) {
  const isZero = totalUsd === 0

  return (
    <div className="bg-[#0e121b] border border-[#1b2230] rounded-xl p-5 flex flex-col justify-between shadow-xl select-none">
      {/* ── Header ── */}
      <div className="flex items-center justify-between pb-3 border-b border-[#1b2230]/60">
        <div className="flex items-center gap-1.5 text-sm font-bold text-white font-sans tracking-tight">
          <span>Asset Allocation &amp; Exposure</span>
          <Info size={13} className="text-slate-500 hover:text-slate-300 cursor-pointer" />
        </div>
      </div>

      {isZero ? (
        <div className="py-16 flex flex-col items-center justify-center text-center text-slate-500">
          <Inbox size={28} className="mb-2 text-slate-600" />
          <span className="text-xs font-sans">No funded asset balances</span>
        </div>
      ) : (
        /* ── Donut Chart & Table Grid ── */
        <div className="grid grid-cols-1 md:grid-cols-12 gap-6 items-center mt-4">
          {/* Left: SVG Donut Chart with Center Text */}
          <div className="md:col-span-5 flex items-center justify-center relative">
            <div className="w-48 h-48 relative flex items-center justify-center">
              <svg className="w-full h-full -rotate-90" viewBox="0 0 100 100">
                <circle
                  cx="50"
                  cy="50"
                  r="38"
                  fill="transparent"
                  stroke="#00e5ff"
                  strokeWidth="12"
                  strokeDasharray="238.7 0"
                  strokeDashoffset="0"
                  className="transition-all duration-500"
                />
              </svg>

              {/* Donut Center Info */}
              <div className="absolute inset-0 flex flex-col items-center justify-center pointer-events-none text-center">
                <span className="text-[10px] text-slate-400 font-sans uppercase tracking-wider">
                  Total Portfolio
                </span>
                <span className="text-sm font-black font-mono text-white mt-0.5">
                  ${totalUsd.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}
                </span>
                <span className="text-[10px] font-mono text-[#00e676] font-bold">100%</span>
              </div>
            </div>
          </div>

          {/* Right: Asset Allocation Table */}
          <div className="md:col-span-7 overflow-x-auto">
            <table className="w-full text-xs font-mono text-left border-collapse">
              <thead>
                <tr className="border-b border-[#1b2230] text-slate-400 text-[10px] font-sans font-semibold">
                  <th className="pb-2">Asset</th>
                  <th className="pb-2 text-center">Allocation</th>
                  <th className="pb-2 text-right">Exposure (USD)</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-[#1b2230]/50">
                {allocations.map((a) => (
                  <tr key={a.symbol} className="hover:bg-[#141a26]/40 transition-colors">
                    {/* Asset */}
                    <td className="py-2.5 font-sans">
                      <div className="flex items-center gap-2">
                        <div
                          className={`w-5 h-5 rounded-full flex items-center justify-center font-bold text-[10px] border ${a.iconBg} ${a.iconText}`}
                        >
                          {a.iconChar}
                        </div>
                        <span className="font-bold text-white text-xs">
                          {a.symbol} <span className="text-slate-400 font-normal">({a.name})</span>
                        </span>
                      </div>
                    </td>

                    {/* Allocation */}
                    <td className="py-2.5 text-center font-bold text-white">
                      {a.pct.toFixed(1)}%
                    </td>

                    {/* Exposure */}
                    <td className="py-2.5 text-right font-semibold text-slate-200">
                      ${a.exposureUsd.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}
                    </td>
                  </tr>
                ))}
                {/* Total Row */}
                <tr className="border-t border-[#1b2230] font-sans font-bold">
                  <td className="pt-2.5 text-white">Total</td>
                  <td className="pt-2.5 text-center text-[#00e676] font-mono">100%</td>
                  <td className="pt-2.5 text-right text-white font-mono">
                    ${totalUsd.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  )
}
