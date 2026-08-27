import { Calendar, ChevronDown, Download } from 'lucide-react'

interface HistoryFilterToolbarProps {
  selectedMarket: string
  onMarketChange: (m: string) => void
  selectedSide: string
  onSideChange: (s: string) => void
  onExportCsv: () => void
}

export default function HistoryFilterToolbar({
  selectedMarket,
  onMarketChange,
  selectedSide,
  onSideChange,
  onExportCsv,
}: HistoryFilterToolbarProps) {
  return (
    <div className="bg-[#0e121b] border border-[#1b2230] rounded-xl p-4 flex flex-wrap items-center justify-between gap-4 shadow-xl select-none">
      {/* ── Left Filters ── */}
      <div className="flex flex-wrap items-center gap-3">
        {/* Date Range Selector */}
        <button
          type="button"
          className="flex items-center gap-2 px-3 py-1.5 rounded-lg bg-[#07090e] border border-[#1b2230] text-xs font-mono text-slate-300 hover:text-white hover:border-slate-500 transition-colors"
        >
          <Calendar size={13} className="text-slate-400" />
          <span>Aug 01, 2026 – Aug 27, 2026</span>
        </button>

        {/* Market Filter */}
        <div className="relative">
          <select
            value={selectedMarket}
            onChange={(e) => onMarketChange(e.target.value)}
            className="appearance-none bg-[#07090e] border border-[#1b2230] rounded-lg px-3 py-1.5 pr-8 text-xs font-sans text-white focus:outline-none focus:border-[#00e5ff]/50 cursor-pointer min-w-[130px]"
          >
            <option value="ALL">All Markets</option>
            <option value="BTC-USDT">BTC/USDT</option>
            <option value="ETH-USDT">ETH/USDT</option>
            <option value="SOL-USDT">SOL/USDT</option>
          </select>
          <ChevronDown size={13} className="absolute right-2.5 top-1/2 -translate-y-1/2 text-slate-500 pointer-events-none" />
        </div>

        {/* Side Filter */}
        <div className="relative">
          <select
            value={selectedSide}
            onChange={(e) => onSideChange(e.target.value)}
            className="appearance-none bg-[#07090e] border border-[#1b2230] rounded-lg px-3 py-1.5 pr-8 text-xs font-sans text-white focus:outline-none focus:border-[#00e5ff]/50 cursor-pointer min-w-[110px]"
          >
            <option value="ALL">All Sides</option>
            <option value="BUY">Buy</option>
            <option value="SELL">Sell</option>
          </select>
          <ChevronDown size={13} className="absolute right-2.5 top-1/2 -translate-y-1/2 text-slate-500 pointer-events-none" />
        </div>
      </div>

      {/* ── Right Action: Export CSV / Tax Report ── */}
      <div>
        <button
          type="button"
          onClick={onExportCsv}
          className="flex items-center gap-1.5 px-3.5 py-1.5 rounded-lg text-xs font-bold text-[#00e5ff] bg-[#00e5ff]/10 border border-[#00e5ff]/30 hover:bg-[#00e5ff]/20 hover:border-[#00e5ff]/60 transition-all shadow-[0_0_10px_rgba(0,229,255,0.15)] active:scale-95 font-sans"
        >
          <Download size={13} />
          <span>Export CSV / Tax Report</span>
        </button>
      </div>
    </div>
  )
}
