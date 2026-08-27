import { Calendar, ChevronDown, Trash2 } from 'lucide-react'

interface OrdersFilterToolbarProps {
  selectedMarket: string
  onMarketChange: (m: string) => void
  selectedSide: string
  onSideChange: (s: string) => void
  selectedType: string
  onTypeChange: (t: string) => void
  onCancelAll: () => void
}

export default function OrdersFilterToolbar({
  selectedMarket,
  onMarketChange,
  selectedSide,
  onSideChange,
  selectedType,
  onTypeChange,
  onCancelAll,
}: OrdersFilterToolbarProps) {
  return (
    <div className="bg-[#0e121b] border border-[#1b2230] rounded-xl p-4 flex flex-wrap items-center justify-between gap-4 shadow-xl select-none">
      {/* ── Left Filters ── */}
      <div className="flex flex-wrap items-center gap-3">
        {/* Market Filter */}
        <div className="flex flex-col gap-1">
          <label className="text-[10px] uppercase font-sans font-semibold text-slate-400">Market</label>
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
        </div>

        {/* Side Filter */}
        <div className="flex flex-col gap-1">
          <label className="text-[10px] uppercase font-sans font-semibold text-slate-400">Side</label>
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

        {/* Type Filter */}
        <div className="flex flex-col gap-1">
          <label className="text-[10px] uppercase font-sans font-semibold text-slate-400">Type</label>
          <div className="relative">
            <select
              value={selectedType}
              onChange={(e) => onTypeChange(e.target.value)}
              className="appearance-none bg-[#07090e] border border-[#1b2230] rounded-lg px-3 py-1.5 pr-8 text-xs font-sans text-white focus:outline-none focus:border-[#00e5ff]/50 cursor-pointer min-w-[120px]"
            >
              <option value="ALL">All Types</option>
              <option value="LIMIT">Limit</option>
              <option value="MARKET">Market</option>
              <option value="STOP_LIMIT">Stop-Limit</option>
            </select>
            <ChevronDown size={13} className="absolute right-2.5 top-1/2 -translate-y-1/2 text-slate-500 pointer-events-none" />
          </div>
        </div>

        {/* Date Range Selector */}
        <div className="flex flex-col gap-1">
          <label className="text-[10px] uppercase font-sans font-semibold text-slate-400">Date Range</label>
          <button
            type="button"
            className="flex items-center gap-2 px-3 py-1.5 rounded-lg bg-[#07090e] border border-[#1b2230] text-xs font-mono text-slate-300 hover:text-white hover:border-slate-500 transition-colors"
          >
            <Calendar size={13} className="text-slate-400" />
            <span>May 19, 2025 – May 26, 2025</span>
          </button>
        </div>
      </div>

      {/* ── Right Action: Cancel All Orders ── */}
      <div className="flex items-end">
        <button
          type="button"
          onClick={onCancelAll}
          className="flex items-center gap-1.5 px-3.5 py-1.5 rounded-lg text-xs font-bold text-[#ff3366] bg-[#ff3366]/10 border border-[#ff3366]/30 hover:bg-[#ff3366]/20 hover:border-[#ff3366]/60 transition-all shadow-[0_0_10px_rgba(255,51,102,0.15)] active:scale-95"
        >
          <Trash2 size={13} />
          <span>Cancel All Orders</span>
        </button>
      </div>
    </div>
  )
}
