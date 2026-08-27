import { useState } from 'react'
import { Plus, Minus, MoreVertical, ShieldCheck } from 'lucide-react'
import type { Balance } from '../../api/wallet'

interface OrderFormProps {
  baseAsset: string
  quoteAsset: string
  price: string
  setPrice: (p: string) => void
  balances: Balance[]
  onSubmitOrder: (order: {
    side: 'BUY' | 'SELL'
    type: 'LIMIT' | 'MARKET' | 'STOP_LIMIT'
    price: string
    quantity: string
  }) => Promise<void>
  isSubmitting: boolean
}

export default function OrderForm({
  baseAsset,
  quoteAsset,
  price,
  setPrice,
  balances,
  onSubmitOrder,
  isSubmitting,
}: OrderFormProps) {
  const [side, setSide] = useState<'BUY' | 'SELL'>('BUY')
  const [orderType, setOrderType] = useState<'LIMIT' | 'MARKET' | 'STOP_LIMIT'>('LIMIT')
  const [quantity, setQuantity] = useState('0.000000')
  const [sliderPct, setSliderPct] = useState(0)

  // Find balances
  const baseBal = balances.find((b) => b.asset === baseAsset)?.availableBalance || '0.985000'
  const quoteBal = balances.find((b) => b.asset === quoteAsset)?.availableBalance || '98,420.00'

  const currentPriceNum = parseFloat(price || '96450.00') || 96450.00
  const qtyNum = parseFloat(quantity) || 0
  const totalNum = qtyNum * (orderType === 'MARKET' ? currentPriceNum : currentPriceNum)
  const feeNum = totalNum * 0.001 // 0.10%

  const handleSliderChange = (pct: number) => {
    setSliderPct(pct)
    if (side === 'BUY') {
      const avail = parseFloat(quoteBal.replace(/,/g, '')) || 98420
      const spend = (avail * pct) / 100
      const calculatedQty = spend / currentPriceNum
      setQuantity(calculatedQty.toFixed(6))
    } else {
      const avail = parseFloat(baseBal.replace(/,/g, '')) || 0.985
      const calculatedQty = (avail * pct) / 100
      setQuantity(calculatedQty.toFixed(6))
    }
  }

  const handlePriceStep = (delta: number) => {
    const next = Math.max(0.01, currentPriceNum + delta)
    setPrice(next.toFixed(2))
  }

  const handleQtyStep = (delta: number) => {
    const next = Math.max(0, qtyNum + delta)
    setQuantity(next.toFixed(6))
  }

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault()
    if (qtyNum <= 0) return
    onSubmitOrder({
      side,
      type: orderType,
      price: orderType === 'MARKET' ? currentPriceNum.toFixed(2) : price,
      quantity,
    })
  }

  return (
    <div className="bg-[#0e121b] border border-[#1b2230] rounded-xl flex flex-col p-4 select-none shadow-xl">
      {/* ── Top Header: Title + VIP Badge + 3-Dots ── */}
      <div className="flex items-center justify-between mb-3 pb-2 border-b border-[#1b2230]/60">
        <span className="text-xs font-bold text-white tracking-tight font-sans">Place Order</span>
        <div className="flex items-center gap-2">
          <span className="px-2 py-0.5 rounded-md bg-[#00e5ff]/10 border border-[#00e5ff]/30 text-[#00e5ff] font-mono text-[10px] font-bold">
            VIP 1
          </span>
          <button type="button" aria-label="Order form settings" className="text-slate-500 hover:text-slate-300">
            <MoreVertical size={13} />
          </button>
        </div>
      </div>

      <form onSubmit={handleSubmit} className="flex flex-col space-y-3">
        {/* ── Buy / Sell Toggle Tabs ── */}
        <div className="grid grid-cols-2 gap-1.5 p-1 rounded-xl bg-[#07090e] border border-[#1b2230]">
          <button
            type="button"
            onClick={() => setSide('BUY')}
            className={`py-2 rounded-lg text-xs font-bold font-sans transition-all ${
              side === 'BUY'
                ? 'bg-[#00e676] text-[#07090e] shadow-[0_0_12px_rgba(0,230,118,0.3)]'
                : 'text-slate-400 hover:text-white'
            }`}
          >
            Buy
          </button>
          <button
            type="button"
            onClick={() => setSide('SELL')}
            className={`py-2 rounded-lg text-xs font-bold font-sans transition-all ${
              side === 'SELL'
                ? 'bg-[#ff3366] text-white shadow-[0_0_12px_rgba(255,51,102,0.3)]'
                : 'text-slate-400 hover:text-white'
            }`}
          >
            Sell
          </button>
        </div>

        {/* ── Order Type Tabs (Limit, Market, Stop-Limit) ── */}
        <div className="grid grid-cols-3 gap-1 py-1 text-[11px] font-semibold text-center font-sans">
          {(['LIMIT', 'MARKET', 'STOP_LIMIT'] as const).map((type) => (
            <button
              key={type}
              type="button"
              onClick={() => setOrderType(type)}
              className={`py-1 rounded-lg transition-all ${
                orderType === type
                  ? 'bg-[#00e5ff]/15 text-[#00e5ff] border border-[#00e5ff]/40 shadow-[0_0_10px_rgba(0,229,255,0.15)]'
                  : 'text-slate-400 hover:text-slate-200'
              }`}
            >
              {type === 'LIMIT' ? 'Limit' : type === 'MARKET' ? 'Market' : 'Stop-Limit'}
            </button>
          ))}
        </div>

        {/* ── Price Input Field ── */}
        {orderType !== 'MARKET' && (
          <div>
            <label className="block text-[10px] font-semibold uppercase tracking-wider text-slate-400 mb-1">
              Price ({quoteAsset})
            </label>
            <div className="relative flex items-center">
              <input
                type="text"
                value={price}
                onChange={(e) => setPrice(e.target.value)}
                className="w-full bg-[#07090e] border border-[#1b2230] rounded-xl px-3 py-2 text-white font-mono text-sm font-bold focus:outline-none focus:border-[#00e5ff]/60 transition-colors"
              />
              <div className="absolute right-2 flex items-center gap-1 text-slate-400">
                <button
                  type="button"
                  aria-label="Decrease price"
                  onClick={() => handlePriceStep(-1)}
                  className="p-1 hover:text-white hover:bg-[#141a26] rounded transition-colors"
                >
                  <Minus size={13} />
                </button>
                <button
                  type="button"
                  aria-label="Increase price"
                  onClick={() => handlePriceStep(1)}
                  className="p-1 hover:text-white hover:bg-[#141a26] rounded transition-colors"
                >
                  <Plus size={13} />
                </button>
              </div>
            </div>
            <span className="text-[10px] font-mono text-slate-500 mt-0.5 block">≈ ${price || '0.00'}</span>
          </div>
        )}

        {/* ── Amount Input Field ── */}
        <div>
          <label className="block text-[10px] font-semibold uppercase tracking-wider text-slate-400 mb-1">
            Amount ({baseAsset})
          </label>
          <div className="relative flex items-center">
            <input
              type="text"
              value={quantity}
              onChange={(e) => setQuantity(e.target.value)}
              className="w-full bg-[#07090e] border border-[#1b2230] rounded-xl px-3 py-2 text-white font-mono text-sm font-bold focus:outline-none focus:border-[#00e5ff]/60 transition-colors"
            />
            <div className="absolute right-2 flex items-center gap-1 text-slate-400">
              <button
                type="button"
                aria-label="Decrease quantity"
                onClick={() => handleQtyStep(-0.01)}
                className="p-1 hover:text-white hover:bg-[#141a26] rounded transition-colors"
              >
                <Minus size={13} />
              </button>
              <button
                type="button"
                aria-label="Increase quantity"
                onClick={() => handleQtyStep(0.01)}
                className="p-1 hover:text-white hover:bg-[#141a26] rounded transition-colors"
              >
                <Plus size={13} />
              </button>
            </div>
          </div>
        </div>

        {/* ── Percentage Balance Slider & Chips ── */}
        <div className="space-y-1.5 pt-1">
          <input
            type="range"
            min="0"
            max="100"
            step="1"
            value={sliderPct}
            onChange={(e) => handleSliderChange(Number(e.target.value))}
            className="w-full accent-[#00e676] bg-[#1b2230] h-1.5 rounded-lg appearance-none cursor-pointer"
          />
          <div className="grid grid-cols-4 gap-1.5 font-mono text-[10px]">
            {[25, 50, 75, 100].map((pct) => (
              <button
                key={pct}
                type="button"
                onClick={() => handleSliderChange(pct)}
                className={`py-1 rounded-lg border text-center transition-all ${
                  sliderPct === pct
                    ? 'bg-[#00e676]/15 border-[#00e676]/50 text-[#00e676] font-bold'
                    : 'bg-[#07090e] border-[#1b2230] text-slate-400 hover:text-white hover:bg-[#141a26]'
                }`}
              >
                {pct}%
              </button>
            ))}
          </div>
        </div>

        {/* ── Total Input (USDT) ── */}
        <div>
          <label className="block text-[10px] font-semibold uppercase tracking-wider text-slate-400 mb-1">
            Total ({quoteAsset})
          </label>
          <div className="w-full bg-[#07090e] border border-[#1b2230] rounded-xl px-3 py-2 text-white font-mono text-sm font-bold flex justify-between items-center">
            <span>{totalNum.toFixed(2)}</span>
            <span className="text-xs text-slate-500 font-sans">{quoteAsset}</span>
          </div>
          <span className="text-[10px] font-mono text-slate-500 mt-0.5 block">≈ ${totalNum.toFixed(2)}</span>
        </div>

        {/* ── Summary Details: Balances, Fees & Totals ── */}
        <div className="space-y-1 pt-2 border-t border-[#1b2230]/60 text-[11px] font-mono">
          <div className="flex items-center justify-between">
            <span className="text-slate-400 font-sans">Available Balance</span>
            <span className="text-[#00e676] font-bold">
              {side === 'BUY' ? `${quoteBal} ${quoteAsset}` : `${baseBal} ${baseAsset}`}
            </span>
          </div>
          <div className="flex items-center justify-between text-slate-400">
            <span className="font-sans">Trading Fee (0.10%)</span>
            <span>{feeNum.toFixed(4)} {quoteAsset}</span>
          </div>
          <div className="flex items-center justify-between text-slate-400">
            <span className="font-sans">Est. Fee ({quoteAsset})</span>
            <span>{feeNum.toFixed(2)} {quoteAsset}</span>
          </div>
          <div className="flex items-center justify-between text-slate-300 font-bold">
            <span className="font-sans">Est. Total ({quoteAsset})</span>
            <span>{(totalNum + (side === 'BUY' ? feeNum : -feeNum)).toFixed(2)} {quoteAsset}</span>
          </div>
        </div>

        {/* ── Full-Width Action Submit Button ── */}
        <button
          type="submit"
          disabled={isSubmitting || qtyNum <= 0}
          className={`w-full py-3 rounded-xl font-bold font-sans text-sm transition-all shadow-lg flex items-center justify-center ${
            side === 'BUY'
              ? 'bg-[#00e676] hover:bg-[#00c853] text-[#07090e] shadow-[0_0_15px_rgba(0,230,118,0.3)] disabled:opacity-50'
              : 'bg-[#ff3366] hover:bg-[#e62e5c] text-white shadow-[0_0_15px_rgba(255,51,102,0.3)] disabled:opacity-50'
          }`}
        >
          {isSubmitting
            ? 'Submitting...'
            : side === 'BUY'
            ? `Buy ${baseAsset}`
            : `Sell ${baseAsset}`}
        </button>

        {/* Trust Badge */}
        <div className="flex items-center justify-center gap-1.5 text-[10px] text-slate-500 font-sans pt-1">
          <ShieldCheck size={12} className="text-[#00e676]" />
          <span>Secure. Fast. Institutional Grade.</span>
        </div>
      </form>
    </div>
  )
}
