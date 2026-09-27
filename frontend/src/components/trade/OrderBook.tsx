import { useState } from 'react'
import { ChevronDown } from 'lucide-react'
import type { OrderBookSnapshot, OrderBookLevel } from '../../types/trade'
import { formatPrice, formatQuantity } from '../../utils/formatters'

interface OrderBookProps {
  orderBook: OrderBookSnapshot
  isDemoData: boolean
  precision: number
  onPrecisionChange: (p: number) => void
  lastPrice: string
  baseAsset: string   // e.g. "BTC"
  quoteAsset: string  // e.g. "USDT"
}

const PRECISIONS = [2, 1, 0]

function DepthRow({ level, side }: { level: OrderBookLevel; side: 'ask' | 'bid' }) {
  const isAsk      = side === 'ask'
  const barColor   = isAsk ? 'rgba(239,68,68,0.12)' : 'rgba(16,185,129,0.12)'
  const priceColor = isAsk ? 'text-[#ef4444]' : 'text-[#10b981]'

  return (
    <div className="relative flex items-center h-[22px] px-2 hover:bg-white/[0.02] transition-colors cursor-default select-none">
      {/* Depth bar — right-aligned */}
      <div
        className="absolute right-0 top-0 h-full"
        style={{ width: `${level.depthPct ?? 0}%`, backgroundColor: barColor, transition: 'width 0.3s ease' }}
      />
      <span className={`relative z-10 flex-1 text-[11px] font-mono font-medium ${priceColor} tabular-nums`}>
        {formatPrice(level.price)}
      </span>
      <span className="relative z-10 w-[72px] text-right text-[11px] font-mono text-slate-300 tabular-nums">
        {formatQuantity(level.quantity, 4)}
      </span>
      <span className="relative z-10 w-[68px] text-right text-[11px] font-mono text-slate-500 tabular-nums">
        {formatQuantity(level.total, 2)}
      </span>
    </div>
  )
}

export default function OrderBook({
  orderBook,
  isDemoData,
  precision,
  onPrecisionChange,
  lastPrice,
  baseAsset,
  quoteAsset,
}: OrderBookProps) {
  const [activeTab, setActiveTab] = useState<'book' | 'depth'>('book')
  const [precOpen, setPrecOpen]   = useState(false)

  const lastPriceNum = parseFloat(lastPrice || orderBook.lastPrice || '0')
  const displayPrice = lastPriceNum > 0 ? formatPrice(lastPriceNum.toString()) : '—'

  return (
    /*
     * h-full: fills the grid cell completely (grid parent controls height)
     * flex flex-col: stacks header → columns → content vertically
     * min-h-0: allows shrinking within grid cell
     */
    <div className="h-full flex flex-col min-h-0 border-l border-[#1e2530] bg-[#111318]">

      {/* ── Header: tabs + precision ──────────────────────────────────────── */}
      <div className="flex items-center justify-between px-2 py-1.5 border-b border-[#1e2530] flex-shrink-0">
        <div className="flex items-center gap-0" role="tablist">
          {(['book', 'depth'] as const).map((tab) => (
            <button
              key={tab}
              role="tab"
              aria-selected={activeTab === tab}
              onClick={() => setActiveTab(tab)}
              className={`px-2 py-1 text-[11px] font-medium rounded transition-colors ${
                activeTab === tab
                  ? 'bg-[#0a0b0e] text-[#f5f7fa] border border-[#1e2530]'
                  : 'text-slate-500 hover:text-[#f5f7fa]'
              }`}
            >
              {tab === 'book' ? 'Order Book' : 'Market Depth'}
            </button>
          ))}
        </div>

        <div className="flex items-center gap-1.5">
          {isDemoData && (
            <span className="text-[9px] font-semibold text-amber-400 bg-amber-500/10 border border-amber-500/20 px-1 py-0.5 rounded uppercase tracking-wider">
              DEMO
            </span>
          )}
          {/* Precision */}
          <div className="relative">
            <button
              type="button"
              onClick={() => setPrecOpen((o) => !o)}
              aria-label={`Precision ${precision}`}
              className="flex items-center gap-0.5 px-1.5 py-0.5 rounded border border-[#1e2530] text-[11px] font-mono text-slate-400 hover:text-[#f5f7fa] bg-[#0a0b0e] transition-colors"
            >
              {precision.toFixed(0)}
              <ChevronDown size={10} />
            </button>
            {precOpen && (
              <div className="absolute right-0 top-full mt-1 w-12 bg-[#111318] border border-[#1e2530] rounded shadow-lg z-30 overflow-hidden">
                {PRECISIONS.map((p) => (
                  <button
                    key={p}
                    type="button"
                    onClick={() => { onPrecisionChange(p); setPrecOpen(false) }}
                    className={`w-full text-center px-2 py-1.5 text-[11px] font-mono transition-colors ${
                      p === precision
                        ? 'text-[#10b981] bg-[#10b981]/10'
                        : 'text-slate-400 hover:text-[#f5f7fa] hover:bg-white/5'
                    }`}
                  >
                    {p.toFixed(0)}
                  </button>
                ))}
              </div>
            )}
          </div>
        </div>
      </div>

      {/* ── Column headers ─────────────────────────────────────────────────── */}
      <div className="flex items-center px-2 py-1 border-b border-[#1e2530]/60 flex-shrink-0">
        <span className="flex-1 text-[10px] text-slate-600 uppercase tracking-wider">Price ({quoteAsset})</span>
        <span className="w-[72px] text-right text-[10px] text-slate-600 uppercase tracking-wider">Amount ({baseAsset})</span>
        <span className="w-[68px] text-right text-[10px] text-slate-600 uppercase tracking-wider">Total</span>
      </div>

      {/* ── Book content ───────────────────────────────────────────────────── */}
      {activeTab === 'book' ? (
        /*
         * flex-1 min-h-0 flex flex-col: fills remaining height
         * asks: flex-1 overflow-y-auto, rows pushed to bottom (justify-end)
         * spread row: flex-shrink-0
         * bids: flex-1 overflow-y-auto
         */
        <div className="flex-1 min-h-0 flex flex-col overflow-hidden">
          {/* Asks — lowest ask (closest to mid) at the bottom */}
          <div className="flex-1 min-h-0 overflow-y-auto flex flex-col justify-end">
            {orderBook.asks.length === 0 ? (
              <div className="py-6 text-center text-[10px] text-slate-600 font-mono">
                No active asks
              </div>
            ) : (
              [...orderBook.asks].reverse().map((level, i) => (
                <DepthRow key={`ask-${i}`} level={level} side="ask" />
              ))
            )}
          </div>

          {/* Mid price / spread */}
          <div className="flex items-center justify-between px-2 py-1.5 bg-[#0d0e12] border-y border-[#1e2530] flex-shrink-0">
            <span className="text-sm font-bold font-mono text-[#10b981] tabular-nums">{displayPrice}</span>
            <span className="text-[10px] text-slate-500 font-mono">
              {orderBook.spread} ({orderBook.spreadPercent}%)
            </span>
          </div>

          {/* Bids */}
          <div className="flex-1 min-h-0 overflow-y-auto">
            {orderBook.bids.length === 0 ? (
              <div className="py-6 text-center text-[10px] text-slate-600 font-mono">
                No active bids
              </div>
            ) : (
              orderBook.bids.map((level, i) => (
                <DepthRow key={`bid-${i}`} level={level} side="bid" />
              ))
            )}
          </div>
        </div>
      ) : (
        <div className="flex-1 flex items-center justify-center">
          <div className="text-center px-4">
            <p className="text-xs text-slate-500">Market depth chart</p>
            <p className="text-[10px] text-slate-600 mt-0.5">Coming soon</p>
          </div>
        </div>
      )}
    </div>
  )
}
