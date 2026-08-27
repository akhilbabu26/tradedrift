import { useState, useMemo } from 'react'
import { ChevronDown, AlignJustify, ArrowDown, ArrowUp } from 'lucide-react'

export interface LiveDepthLevel {
  price: string
  size: string
  total: string
  depth: number
}

interface OrderBookProps {
  baseAsset: string
  quoteAsset: string
  asks: LiveDepthLevel[]
  bids: LiveDepthLevel[]
  lastPrice: string
  onSelectPrice: (price: string) => void
}

const cleanNumber = (val: string | number | undefined | null, fallback: number): number => {
  if (typeof val === 'number') return isNaN(val) ? fallback : val
  if (!val) return fallback
  const clean = String(val).replace(/,/g, '').trim()
  const num = parseFloat(clean)
  return isNaN(num) ? fallback : num
}

export default function OrderBook({
  baseAsset,
  quoteAsset,
  asks,
  bids,
  lastPrice,
  onSelectPrice,
}: OrderBookProps) {
  const [viewMode, setViewMode] = useState<'both' | 'bids' | 'asks'>('both')

  const fallbackPrice = baseAsset === 'ETH' ? 2780.50 : baseAsset === 'SOL' ? 188.20 : 96450.00
  const currentPriceNum = cleanNumber(lastPrice, fallbackPrice)

  // Dynamic precision options depending on baseAsset price scale
  const precisionOptions = useMemo(() => {
    if (currentPriceNum > 10000) return ['0.1', '1', '10']
    if (currentPriceNum > 1000) return ['0.01', '0.1', '1']
    return ['0.001', '0.01', '0.1']
  }, [currentPriceNum])

  const [precision, setPrecision] = useState(precisionOptions[0] || '0.01')
  const [showPrecisionMenu, setShowPrecisionMenu] = useState(false)

  // Formatter for prices
  const formatPrice = (p: number) => {
    const decimals = currentPriceNum > 1000 ? 2 : currentPriceNum > 10 ? 2 : 4
    return p.toLocaleString('en-US', { minimumFractionDigits: decimals, maximumFractionDigits: decimals })
  }

  // Dynamic generated L2 ladder based on actual asset price
  const { defaultAsks, defaultBids, spread } = useMemo(() => {
    const format = (p: number) => {
      const decimals = currentPriceNum > 1000 ? 2 : currentPriceNum > 10 ? 2 : 4
      return p.toLocaleString('en-US', { minimumFractionDigits: decimals, maximumFractionDigits: decimals })
    }

    const tickStep = currentPriceNum > 10000 ? 0.5 : currentPriceNum > 1000 ? 0.1 : 0.01
    const qtyMultiplier = baseAsset === 'SOL' ? 25 : baseAsset === 'ETH' ? 3.5 : 0.45

    const generatedAsks: LiveDepthLevel[] = []
    let askTotal = 0
    for (let i = 15; i >= 1; i--) {
      const p = currentPriceNum + i * tickStep
      const size = (Math.sin(i * 1.5) * 0.3 + 0.6) * qtyMultiplier
      askTotal += size
      generatedAsks.push({
        price: format(p),
        size: size.toFixed(4),
        total: askTotal.toFixed(4),
        depth: Math.min(100, Math.round((askTotal / (qtyMultiplier * 12)) * 100)),
      })
    }

    const generatedBids: LiveDepthLevel[] = []
    let bidTotal = 0
    for (let i = 1; i <= 15; i++) {
      const p = Math.max(0.001, currentPriceNum - i * tickStep)
      const size = (Math.cos(i * 1.2) * 0.3 + 0.65) * qtyMultiplier
      bidTotal += size
      generatedBids.push({
        price: format(p),
        size: size.toFixed(4),
        total: bidTotal.toFixed(4),
        depth: Math.min(100, Math.round((bidTotal / (qtyMultiplier * 12)) * 100)),
      })
    }

    const spVal = tickStep
    const spPct = ((spVal / currentPriceNum) * 100).toFixed(3)

    return {
      defaultAsks: generatedAsks,
      defaultBids: generatedBids,
      spread: `${spVal.toFixed(2)} (${spPct}%)`,
    }
  }, [currentPriceNum, baseAsset])

  const displayAsks = asks.length > 0 ? asks.slice(0, viewMode === 'asks' ? 24 : 12) : defaultAsks.slice(0, viewMode === 'asks' ? 24 : 12)
  const displayBids = bids.length > 0 ? bids.slice(0, viewMode === 'bids' ? 24 : 12) : defaultBids.slice(0, viewMode === 'bids' ? 24 : 12)

  return (
    <div className="bg-[#0e121b] border border-[#1b2230] rounded-xl flex flex-col overflow-hidden select-none shadow-xl">
      {/* ── Top Header: Title + Precision + View Toggle ── */}
      <div className="h-10 border-b border-[#1b2230] px-3 flex items-center justify-between bg-[#07090e]/60 flex-shrink-0">
        <span className="text-xs font-bold text-white tracking-tight font-sans">Order Book</span>

        <div className="flex items-center gap-2">
          {/* Precision Selector Dropdown */}
          <div className="relative">
            <button
              type="button"
              onClick={() => setShowPrecisionMenu(!showPrecisionMenu)}
              className="flex items-center gap-1 px-2 py-0.5 rounded-lg bg-[#07090e] border border-[#1b2230] text-[10px] font-mono text-slate-300 hover:text-white transition-colors"
            >
              <span>{precision}</span>
              <ChevronDown size={11} className="text-slate-500" />
            </button>

            {showPrecisionMenu && (
              <div className="absolute right-0 top-full mt-1 bg-[#141a26] border border-[#1b2230] rounded-lg shadow-2xl py-1 z-30 min-w-[60px]">
                {precisionOptions.map((opt) => (
                  <button
                    key={opt}
                    type="button"
                    onClick={() => {
                      setPrecision(opt)
                      setShowPrecisionMenu(false)
                    }}
                    className={`w-full text-left px-3 py-1 text-[10px] font-mono hover:bg-[#1e2530] ${
                      precision === opt ? 'text-[#00e5ff] font-bold' : 'text-slate-300'
                    }`}
                  >
                    {opt}
                  </button>
                ))}
              </div>
            )}
          </div>

          {/* View Toggles (Both, Bids, Asks) */}
          <div className="flex items-center bg-[#07090e] p-0.5 rounded-lg border border-[#1b2230]">
            <button
              type="button"
              title="Default (Bids & Asks)"
              onClick={() => setViewMode('both')}
              className={`p-1 rounded ${viewMode === 'both' ? 'bg-[#1b2230] text-white' : 'text-slate-500 hover:text-slate-300'}`}
            >
              <AlignJustify size={11} />
            </button>
            <button
              type="button"
              title="Bids Only"
              onClick={() => setViewMode('bids')}
              className={`p-1 rounded ${viewMode === 'bids' ? 'bg-[#1b2230] text-[#00e676]' : 'text-slate-500 hover:text-slate-300'}`}
            >
              <ArrowUp size={11} />
            </button>
            <button
              type="button"
              title="Asks Only"
              onClick={() => setViewMode('asks')}
              className={`p-1 rounded ${viewMode === 'asks' ? 'bg-[#1b2230] text-[#ff3366]' : 'text-slate-500 hover:text-slate-300'}`}
            >
              <ArrowDown size={11} />
            </button>
          </div>
        </div>
      </div>

      {/* ── Column Headers ── */}
      <div className="grid grid-cols-3 px-3 py-1.5 text-[10px] font-mono text-slate-500 border-b border-[#1b2230]/60 bg-[#07090e]/40 flex-shrink-0">
        <div className="text-left">Price ({quoteAsset})</div>
        <div className="text-right">Amount ({baseAsset})</div>
        <div className="text-right">Total ({baseAsset})</div>
      </div>

      {/* ── Asks (Sell Orders - Crimson) ── */}
      {(viewMode === 'both' || viewMode === 'asks') && (
        <div className="flex flex-col justify-end text-[11px] font-mono overflow-hidden py-0.5 space-y-[1px]">
          {displayAsks.map((ask, i) => (
            <div
              key={i}
              onClick={() => onSelectPrice(ask.price.replace(/,/g, ''))}
              className="relative px-3 py-[2px] grid grid-cols-3 hover:bg-white/5 cursor-pointer transition-colors group"
            >
              {/* Depth Fill Bar */}
              <div
                className="absolute right-0 top-0 bottom-0 bg-[#ff3366]/15 group-hover:bg-[#ff3366]/25 transition-all z-0"
                style={{ width: `${ask.depth}%` }}
              />
              <div className="relative z-10 text-left text-[#ff3366] font-semibold">{ask.price}</div>
              <div className="relative z-10 text-right text-slate-300">{ask.size}</div>
              <div className="relative z-10 text-right text-slate-500">{ask.total}</div>
            </div>
          ))}
        </div>
      )}

      {/* ── Mid-Market Spread Ribbon ── */}
      <div className="py-2 px-3 my-0.5 bg-[#07090e] border-y border-[#1b2230] flex items-center justify-between font-mono flex-shrink-0">
        <div className="flex items-center gap-2">
          <span className="text-base font-black text-[#00e676] flex items-center gap-1 tracking-tight">
            {formatPrice(currentPriceNum)}
          </span>
          <span className="text-[10px] text-slate-400">Spread: {spread}</span>
        </div>
        <ChevronDown size={13} className="text-slate-500" />
      </div>

      {/* ── Bids (Buy Orders - Emerald) ── */}
      {(viewMode === 'both' || viewMode === 'bids') && (
        <div className="flex flex-col justify-start text-[11px] font-mono overflow-hidden py-0.5 space-y-[1px]">
          {displayBids.map((bid, i) => (
            <div
              key={i}
              onClick={() => onSelectPrice(bid.price.replace(/,/g, ''))}
              className="relative px-3 py-[2px] grid grid-cols-3 hover:bg-white/5 cursor-pointer transition-colors group"
            >
              {/* Depth Fill Bar */}
              <div
                className="absolute right-0 top-0 bottom-0 bg-[#00e676]/15 group-hover:bg-[#00e676]/25 transition-all z-0"
                style={{ width: `${bid.depth}%` }}
              />
              <div className="relative z-10 text-left text-[#00e676] font-semibold">{bid.price}</div>
              <div className="relative z-10 text-right text-slate-300">{bid.size}</div>
              <div className="relative z-10 text-right text-slate-500">{bid.total}</div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
