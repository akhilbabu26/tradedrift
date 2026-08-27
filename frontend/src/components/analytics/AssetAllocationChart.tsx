import { Info } from 'lucide-react'

interface AllocationItem {
  symbol: string
  name: string
  pct: number
  exposureUsd: number
  color: string
  iconBg: string
  iconText: string
  iconChar: string
}

const ALLOCATIONS: AllocationItem[] = [
  {
    symbol: 'BTC',
    name: 'Bitcoin',
    pct: 52,
    exposureUsd: 64922.13,
    color: '#00e676',
    iconBg: 'bg-[#f7931a]/15 border-[#f7931a]/30',
    iconText: 'text-[#f7931a]',
    iconChar: '₿',
  },
  {
    symbol: 'USDT',
    name: 'Tether',
    pct: 28,
    exposureUsd: 34979.7,
    color: '#00e5ff',
    iconBg: 'bg-[#00e676]/15 border-[#00e676]/30',
    iconText: 'text-[#00e676]',
    iconChar: '₮',
  },
  {
    symbol: 'ETH',
    name: 'Ethereum',
    pct: 12,
    exposureUsd: 14994.03,
    color: '#a855f7',
    iconBg: 'bg-[#627eea]/15 border-[#627eea]/30',
    iconText: 'text-[#627eea]',
    iconChar: 'Ξ',
  },
  {
    symbol: 'SOL',
    name: 'Solana',
    pct: 8,
    exposureUsd: 9954.39,
    color: '#f59e0b',
    iconBg: 'bg-[#00e5ff]/15 border-[#00e5ff]/30',
    iconText: 'text-[#00e5ff]',
    iconChar: 'S',
  },
]

export default function AssetAllocationChart() {
  const totalUsd = 124850.25

  return (
    <div className="bg-[#0e121b] border border-[#1b2230] rounded-xl p-5 flex flex-col justify-between shadow-xl select-none">
      {/* ── Header ── */}
      <div className="flex items-center justify-between pb-3 border-b border-[#1b2230]/60">
        <div className="flex items-center gap-1.5 text-sm font-bold text-white font-sans tracking-tight">
          <span>Asset Allocation &amp; Exposure</span>
          <Info size={13} className="text-slate-500 hover:text-slate-300 cursor-pointer" />
        </div>
      </div>

      {/* ── Donut Chart & Table Grid ── */}
      <div className="grid grid-cols-1 md:grid-cols-12 gap-6 items-center mt-4">
        {/* Left: SVG Donut Chart with Center Text */}
        <div className="md:col-span-5 flex items-center justify-center relative">
          <div className="w-48 h-48 relative flex items-center justify-center">
            <svg className="w-full h-full -rotate-90" viewBox="0 0 100 100">
              {/* BTC: 52% (dasharray 52, offset 0) */}
              <circle
                cx="50"
                cy="50"
                r="38"
                fill="transparent"
                stroke="#00e676"
                strokeWidth="12"
                strokeDasharray="124.1 238.7"
                strokeDashoffset="0"
                className="transition-all duration-500 hover:opacity-90"
              />
              {/* USDT: 28% (dasharray 28, offset -124.1) */}
              <circle
                cx="50"
                cy="50"
                r="38"
                fill="transparent"
                stroke="#00e5ff"
                strokeWidth="12"
                strokeDasharray="66.8 238.7"
                strokeDashoffset="-124.1"
                className="transition-all duration-500 hover:opacity-90"
              />
              {/* ETH: 12% (dasharray 12, offset -190.9) */}
              <circle
                cx="50"
                cy="50"
                r="38"
                fill="transparent"
                stroke="#a855f7"
                strokeWidth="12"
                strokeDasharray="28.6 238.7"
                strokeDashoffset="-190.9"
                className="transition-all duration-500 hover:opacity-90"
              />
              {/* SOL: 8% (dasharray 8, offset -219.5) */}
              <circle
                cx="50"
                cy="50"
                r="38"
                fill="transparent"
                stroke="#f59e0b"
                strokeWidth="12"
                strokeDasharray="19.1 238.7"
                strokeDashoffset="-219.5"
                className="transition-all duration-500 hover:opacity-90"
              />
            </svg>

            {/* Donut Center Info */}
            <div className="absolute inset-0 flex flex-col items-center justify-center pointer-events-none text-center">
              <span className="text-[10px] text-slate-400 font-sans uppercase tracking-wider">
                Total Portfolio
              </span>
              <span className="text-sm font-black font-mono text-white mt-0.5">
                ${totalUsd.toLocaleString('en-US', { minimumFractionDigits: 2 })}
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
              {ALLOCATIONS.map((a) => (
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
                    {a.pct}%
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
    </div>
  )
}
