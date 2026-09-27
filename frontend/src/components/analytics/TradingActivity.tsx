import { useState } from 'react'
import { ShieldCheck, PieChart } from 'lucide-react'
import type {
  TradingActivityTab,
  BySideData,
  ByAssetFillItem,
  ByMonthFillItem,
} from '../../types/analytics'

interface Props {
  bySide: BySideData
  byAsset: ByAssetFillItem[]
  byMonth: ByMonthFillItem[]
  currentPortfolioValue?: string
  activeAssetsCount?: number
}

/**
 * Trading Activity — right ~40% panel
 * Interactive tabs: By Side | By Asset | By Month
 * SVG Donut Chart + 2 bottom summary cards
 */
export default function TradingActivity({
  bySide,
  byAsset,
  byMonth,
  currentPortfolioValue = '24,150.80',
  activeAssetsCount = 4,
}: Props) {
  const [activeTab, setActiveTab] = useState<TradingActivityTab>('side')

  // ── Donut Chart Math ────────────────────────────────────────────────────────
  const SIZE = 130
  const RADIUS = 48
  const STROKE = 14
  const CIRCUMFERENCE = 2 * Math.PI * RADIUS
  const cx = SIZE / 2
  const cy = SIZE / 2

  // Buy fills arc (green) & Sell fills arc (red)
  const buyArc = (bySide.buyPct / 100) * CIRCUMFERENCE
  const gap = 3
  const buyVisible = Math.max(0, buyArc - gap)
  const sellArc = (bySide.sellPct / 100) * CIRCUMFERENCE
  const sellVisible = Math.max(0, sellArc - gap)

  return (
    <div className="bg-[#111318] border border-[#1e2530] rounded-xl p-5 flex flex-col justify-between">
      {/* ── Top Header + Tab Selector ────────────────────────────────────── */}
      <div>
        <div className="flex items-start justify-between gap-2">
          <div>
            <h2 className="text-sm font-semibold text-[#f5f7fa]">
              Trading Activity
            </h2>
            <p className="text-xs text-[#94a3b8] mt-0.5">
              Breakdown of your trading fills
            </p>
          </div>

          {/* Tab Selector Pills */}
          <div className="bg-[#0a0b0e] border border-[#1e2530] p-0.5 rounded-lg flex items-center gap-0.5 flex-shrink-0">
            <button
              type="button"
              onClick={() => setActiveTab('side')}
              className={`px-2.5 py-1 rounded-md text-[11px] font-medium transition-colors ${
                activeTab === 'side'
                  ? 'bg-[#10b981]/15 border border-[#10b981]/30 text-[#10b981] font-semibold'
                  : 'text-[#94a3b8] hover:text-[#f5f7fa] border border-transparent'
              }`}
            >
              By Side
            </button>
            <button
              type="button"
              onClick={() => setActiveTab('asset')}
              className={`px-2.5 py-1 rounded-md text-[11px] font-medium transition-colors ${
                activeTab === 'asset'
                  ? 'bg-[#10b981]/15 border border-[#10b981]/30 text-[#10b981] font-semibold'
                  : 'text-[#94a3b8] hover:text-[#f5f7fa] border border-transparent'
              }`}
            >
              By Asset
            </button>
            <button
              type="button"
              onClick={() => setActiveTab('month')}
              className={`px-2.5 py-1 rounded-md text-[11px] font-medium transition-colors ${
                activeTab === 'month'
                  ? 'bg-[#10b981]/15 border border-[#10b981]/30 text-[#10b981] font-semibold'
                  : 'text-[#94a3b8] hover:text-[#f5f7fa] border border-transparent'
              }`}
            >
              By Month
            </button>
          </div>
        </div>

        {/* ── Tab Content ─────────────────────────────────────────────────── */}
        <div className="mt-4 min-h-[140px] flex items-center">
          {/* TAB 1: By Side (Donut chart + breakdown) */}
          {activeTab === 'side' && (
            <div className="flex items-center justify-around w-full gap-4">
              {/* Donut Chart */}
              <div className="relative flex-shrink-0" style={{ width: SIZE, height: SIZE }}>
                <svg width={SIZE} height={SIZE} viewBox={`0 0 ${SIZE} ${SIZE}`}>
                  {/* Background base track */}
                  <circle
                    cx={cx}
                    cy={cy}
                    r={RADIUS}
                    fill="none"
                    stroke="#1e2530"
                    strokeWidth={STROKE}
                  />

                  {/* Rotated segments starting at 12 o'clock */}
                  <g transform={`rotate(-90 ${cx} ${cy})`}>
                    {/* Buy segment (green) */}
                    <circle
                      cx={cx}
                      cy={cy}
                      r={RADIUS}
                      fill="none"
                      stroke="#10b981"
                      strokeWidth={STROKE}
                      strokeDasharray={`${buyVisible} ${CIRCUMFERENCE - buyVisible}`}
                      strokeDashoffset={0}
                      strokeLinecap="butt"
                      className="transition-all duration-300"
                    />

                    {/* Sell segment (red) */}
                    <circle
                      cx={cx}
                      cy={cy}
                      r={RADIUS}
                      fill="none"
                      stroke="#ef4444"
                      strokeWidth={STROKE}
                      strokeDasharray={`${sellVisible} ${CIRCUMFERENCE - sellVisible}`}
                      strokeDashoffset={-buyArc}
                      strokeLinecap="butt"
                      className="transition-all duration-300"
                    />
                  </g>
                </svg>

                {/* Donut Center */}
                <div className="absolute inset-0 flex flex-col items-center justify-center pointer-events-none">
                  <span className="text-xl font-bold text-[#f5f7fa] font-mono leading-none">
                    {bySide.totalFills}
                  </span>
                  <span className="text-[9px] text-[#64748b] mt-1 uppercase font-medium">
                    Total Fills
                  </span>
                </div>
              </div>

              {/* Side Legend */}
              <div className="flex flex-col gap-3 min-w-[140px]">
                <div className="flex items-center justify-between gap-3">
                  <div className="flex items-center gap-2">
                    <span className="w-2.5 h-2.5 rounded-full bg-[#10b981] flex-shrink-0" />
                    <span className="text-xs text-[#cbd5e1] font-medium">Buy Fills</span>
                  </div>
                  <span className="text-xs font-mono font-medium text-[#f5f7fa]">
                    {bySide.buyFills} ({bySide.buyPct}%)
                  </span>
                </div>

                <div className="flex items-center justify-between gap-3">
                  <div className="flex items-center gap-2">
                    <span className="w-2.5 h-2.5 rounded-full bg-[#ef4444] flex-shrink-0" />
                    <span className="text-xs text-[#cbd5e1] font-medium">Sell Fills</span>
                  </div>
                  <span className="text-xs font-mono font-medium text-[#f5f7fa]">
                    {bySide.sellFills} ({bySide.sellPct}%)
                  </span>
                </div>
              </div>
            </div>
          )}

          {/* TAB 2: By Asset (BTC, ETH, SOL fills breakdown) */}
          {activeTab === 'asset' && (
            <div className="w-full flex flex-col gap-2.5 py-1">
              {byAsset.map((item) => (
                <div key={item.asset} className="flex items-center gap-3">
                  <div className="flex items-center gap-2 w-14 flex-shrink-0">
                    <span
                      className="w-2 h-2 rounded-full flex-shrink-0"
                      style={{ backgroundColor: item.color }}
                    />
                    <span className="text-xs font-semibold text-[#f5f7fa]">
                      {item.asset}
                    </span>
                  </div>

                  <div className="flex-1 h-2 bg-[#1e2530] rounded-full overflow-hidden">
                    <div
                      className="h-full rounded-full transition-all duration-300"
                      style={{
                        width: `${item.percentage}%`,
                        backgroundColor: item.color,
                      }}
                    />
                  </div>

                  <span className="text-xs text-[#cbd5e1] font-mono font-medium w-24 text-right flex-shrink-0">
                    {item.fillsCount} fills ({item.percentage}%)
                  </span>
                </div>
              ))}
              <p className="text-[10px] text-[#64748b] mt-1 text-right">
                USDT excluded (quote settlement currency)
              </p>
            </div>
          )}

          {/* TAB 3: By Month */}
          {activeTab === 'month' && (
            <div className="w-full flex flex-col gap-2.5 py-1">
              {byMonth.map((m) => (
                <div key={m.month} className="flex items-center gap-3">
                  <span className="text-xs text-[#94a3b8] w-24 flex-shrink-0">
                    {m.month}
                  </span>

                  <div className="flex-1 h-2 bg-[#1e2530] rounded-full overflow-hidden">
                    <div
                      className="h-full rounded-full bg-[#10b981] transition-all duration-300"
                      style={{ width: `${m.percentage}%` }}
                    />
                  </div>

                  <span className="text-xs text-[#cbd5e1] font-mono font-medium w-24 text-right flex-shrink-0">
                    {m.fillsCount} fills ({m.percentage}%)
                  </span>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>

      {/* ── Bottom Summary Cards (Current Portfolio Value & Active Assets) ── */}
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 mt-4 pt-4 border-t border-[#1e2530]">
        {/* Card 1: Current Portfolio Value */}
        <div className="bg-[#0a0b0e]/60 border border-[#1e2530] rounded-xl p-3 flex items-center gap-3">
          <div className="w-8 h-8 rounded-lg bg-[#10b981]/10 border border-[#10b981]/20 flex items-center justify-center text-[#10b981] flex-shrink-0">
            <ShieldCheck className="w-4 h-4" />
          </div>
          <div className="min-w-0">
            <p className="text-[10px] text-[#94a3b8] font-medium uppercase tracking-wide">
              Current Portfolio Value
            </p>
            <p className="text-xs font-bold text-[#f5f7fa] font-mono mt-0.5 truncate">
              {currentPortfolioValue.replace(/^\$/, '')} <span className="text-[10px] font-normal text-[#94a3b8]">USDT</span>
            </p>
            <p className="text-[10px] text-[#64748b]">Realized + Unrealized</p>
          </div>
        </div>

        {/* Card 2: Active Assets */}
        <div className="bg-[#0a0b0e]/60 border border-[#1e2530] rounded-xl p-3 flex items-center gap-3">
          <div className="w-8 h-8 rounded-lg bg-[#3b82f6]/10 border border-[#3b82f6]/20 flex items-center justify-center text-[#3b82f6] flex-shrink-0">
            <PieChart className="w-4 h-4" />
          </div>
          <div className="min-w-0">
            <p className="text-[10px] text-[#94a3b8] font-medium uppercase tracking-wide">
              Active Assets
            </p>
            <p className="text-xs font-bold text-[#f5f7fa] font-mono mt-0.5">
              {activeAssetsCount}
            </p>
            <p className="text-[10px] text-[#64748b]">Different assets in portfolio</p>
          </div>
        </div>
      </div>
    </div>
  )
}
