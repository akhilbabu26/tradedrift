import { useState, useCallback } from 'react'
import { ChevronUp, ChevronDown } from 'lucide-react'
import type { OrderSide, OrderType, Market, Balance, CreateOrderRequest } from '../../types/trade'
import { toDecimal, multiply, divide } from '../../utils/decimal'
import { formatPrice, formatQuantity } from '../../utils/formatters'

interface OrderFormProps {
  side: OrderSide
  orderType: OrderType
  market: Market
  lastPrice: string
  availableQuote: Balance   // USDT for BTC/USDT
  availableBase: Balance    // BTC for BTC/USDT
  submitting: boolean
  onSubmit: (req: CreateOrderRequest) => Promise<boolean>
}

const PCT_BUTTONS = [
  { label: '25%', pct: 0.25 },
  { label: '50%', pct: 0.50 },
  { label: '75%', pct: 0.75 },
  { label: 'Max', pct: 1.00 },
]

function NumericInput({
  id,
  label,
  suffix,
  value,
  onChange,
  disabled = false,
  placeholder = '0.00',
  step = 1,
  min = 0,
}: {
  id: string
  label: string
  suffix: string
  value: string
  onChange: (v: string) => void
  disabled?: boolean
  placeholder?: string
  step?: number
  min?: number
}) {
  const handleStep = (direction: 1 | -1) => {
    try {
      const current = toDecimal(value || '0')
      const next    = current.plus(toDecimal(step).times(direction))
      if (next.gte(min)) onChange(next.toFixed(step < 1 ? 4 : 2))
    } catch { /* ignore */ }
  }

  return (
    <div className="flex flex-col gap-1">
      <label htmlFor={id} className="text-[10px] text-slate-500 uppercase tracking-wider">{label}</label>
      <div className="flex items-center bg-[#0a0b0e] border border-[#1e2530] rounded-md overflow-hidden focus-within:border-[#10b981]/40 transition-colors">
        <input
          id={id}
          type="number"
          inputMode="decimal"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={placeholder}
          disabled={disabled}
          min={min}
          className="flex-1 bg-transparent px-2.5 py-2 text-sm font-mono text-[#f5f7fa] placeholder-slate-700 focus:outline-none disabled:opacity-50 [appearance:textfield] [&::-webkit-outer-spin-button]:appearance-none [&::-webkit-inner-spin-button]:appearance-none"
          aria-label={`${label} in ${suffix}`}
        />
        <div className="flex flex-col border-l border-[#1e2530]">
          <button
            type="button"
            onClick={() => handleStep(1)}
            disabled={disabled}
            aria-label={`Increase ${label}`}
            className="px-1.5 py-0.5 text-slate-500 hover:text-[#f5f7fa] hover:bg-white/5 transition-colors disabled:opacity-40"
          >
            <ChevronUp size={11} />
          </button>
          <button
            type="button"
            onClick={() => handleStep(-1)}
            disabled={disabled}
            aria-label={`Decrease ${label}`}
            className="px-1.5 py-0.5 text-slate-500 hover:text-[#f5f7fa] hover:bg-white/5 transition-colors disabled:opacity-40"
          >
            <ChevronDown size={11} />
          </button>
        </div>
        <span className="px-2 text-[11px] text-slate-600 font-medium whitespace-nowrap border-l border-[#1e2530] py-2 bg-[#111318]">
          {suffix}
        </span>
      </div>
    </div>
  )
}

export default function OrderForm({
  side,
  orderType,
  market,
  lastPrice,
  availableQuote,
  availableBase,
  submitting,
  onSubmit,
}: OrderFormProps) {
  const [price,  setPrice]  = useState(lastPrice || '')
  const [amount, setAmount] = useState('')
  const [error,  setError]  = useState<string | null>(null)

  const baseAsset  = market.base_asset
  const quoteAsset = market.quote_asset
  const minQty     = toDecimal(market.min_quantity || '0.0001')
  const isBuy      = side === 'BUY'
  const isLimit    = orderType === 'LIMIT'

  // ── Derived financials (all via decimal.js) ────────────────────────────────
  const priceNum  = isLimit ? toDecimal(price  || '0') : toDecimal(lastPrice || '0')
  const amountNum = toDecimal(amount || '0')
  const orderValue = priceNum.gt(0) && amountNum.gt(0)
    ? multiply(priceNum, amountNum).toFixed(2)
    : '0.00'
  const feeRate    = toDecimal('0.001')  // 0.1%
  const estFee     = toDecimal(orderValue).times(feeRate).toFixed(2)

  const availableQuoteNum = toDecimal(availableQuote.availableBalance || '0')
  const availableBaseNum  = toDecimal(availableBase.availableBalance  || '0')

  // ── Percentage buttons ────────────────────────────────────────────────────
  const handlePctClick = useCallback((pct: number) => {
    try {
      if (isBuy) {
        // BUY: pct of available USDT → convert to base quantity
        const usdtToUse = availableQuoteNum.times(pct)
        const effectivePrice = isLimit ? priceNum : toDecimal(lastPrice || '0')
        if (effectivePrice.gt(0)) {
          const qty = divide(usdtToUse, effectivePrice)
          setAmount(qty.toFixed(4))
        }
      } else {
        // SELL: pct of available base
        const qty = availableBaseNum.times(pct)
        setAmount(qty.toFixed(4))
      }
    } catch { /* ignore bad input */ }
  }, [isBuy, availableQuoteNum, availableBaseNum, priceNum, isLimit, lastPrice])

  // ── Validation ────────────────────────────────────────────────────────────
  const validate = (): string | null => {
    if (isLimit && (!price || priceNum.lte(0))) return 'Price is required and must be > 0'
    if (!amount || amountNum.lte(0))            return 'Amount is required and must be > 0'
    if (amountNum.lt(minQty))                   return `Minimum amount is ${minQty.toFixed(4)} ${baseAsset}`
    if (isBuy) {
      if (toDecimal(orderValue).gt(availableQuoteNum)) return `Insufficient ${quoteAsset} balance`
    } else {
      if (amountNum.gt(availableBaseNum)) return `Insufficient ${baseAsset} balance`
    }
    return null
  }

  // ── Submit ────────────────────────────────────────────────────────────────
  const handleSubmit = async () => {
    const err = validate()
    if (err) { setError(err); return }
    setError(null)

    const req: CreateOrderRequest = {
      market_id:  market.id,
      side,
      order_type: orderType,
      quantity:   amountNum.toFixed(8),
      ...(isLimit && { price: priceNum.toFixed(2) }),
    }

    const ok = await onSubmit(req)
    if (ok) {
      setAmount('')
      if (isLimit) setPrice('')
    }
  }

  const buyBg  = isBuy  ? 'bg-[#10b981] text-[#0a0b0e] hover:bg-[#0ea572]' : 'bg-[#ef4444] text-white hover:bg-[#dc2626]'

  return (
    <div className="flex flex-col gap-3 px-3 py-3">
      {/* Price (Limit only) */}
      {isLimit && (
        <NumericInput
          id="order-price"
          label={`Price (${quoteAsset})`}
          suffix={quoteAsset}
          value={price}
          onChange={(v) => { setPrice(v); setError(null) }}
          placeholder={lastPrice || '0.00'}
          step={parseFloat(market.tick_size || '0.01')}
        />
      )}

      {/* Market order info */}
      {!isLimit && (
        <div className="px-2.5 py-2 rounded-md bg-[#0a0b0e] border border-[#1e2530]">
          <p className="text-[11px] text-slate-500">Market Price</p>
          <p className="text-sm font-mono font-semibold text-[#f5f7fa] mt-0.5">
            {lastPrice ? formatPrice(lastPrice) : '—'} <span className="text-slate-600 text-xs">{quoteAsset}</span>
          </p>
        </div>
      )}

      {/* Amount */}
      <NumericInput
        id="order-amount"
        label={`Amount (${baseAsset})`}
        suffix={baseAsset}
        value={amount}
        onChange={(v) => { setAmount(v); setError(null) }}
        placeholder="0.0000"
        step={parseFloat(market.lot_size || '0.0001')}
        min={0}
      />

      {/* Percentage buttons */}
      <div className="grid grid-cols-4 gap-1.5">
        {PCT_BUTTONS.map(({ label, pct }) => (
          <button
            key={label}
            type="button"
            onClick={() => handlePctClick(pct)}
            className="py-1 rounded bg-[#0a0b0e] border border-[#1e2530] text-[11px] text-slate-400 hover:text-[#f5f7fa] hover:border-slate-600 transition-colors font-medium"
          >
            {label}
          </button>
        ))}
      </div>

      {/* Error */}
      {error && (
        <div className="px-2.5 py-1.5 rounded-md bg-[#ef4444]/8 border border-[#ef4444]/20">
          <p className="text-xs text-[#ef4444]">{error}</p>
        </div>
      )}

      {/* Summary rows */}
      <div className="flex flex-col gap-1.5 pt-1 border-t border-[#1e2530]">
        <div className="flex items-center justify-between">
          <span className="text-[11px] text-slate-500">Available ({isBuy ? quoteAsset : baseAsset})</span>
          <span className="text-[11px] font-mono text-slate-300">
            {isBuy
              ? formatPrice(availableQuote.availableBalance)
              : formatQuantity(availableBase.availableBalance, 6)
            }
          </span>
        </div>
        <div className="flex items-center justify-between">
          <span className="text-[11px] text-slate-500">Order Value</span>
          <span className="text-[11px] font-mono text-slate-300">{formatPrice(orderValue)}</span>
        </div>
        <div className="flex items-center justify-between">
          <span className="text-[11px] text-slate-500">Est. Fee (0.1%)</span>
          <span className="text-[11px] font-mono text-slate-300">{estFee} {quoteAsset}</span>
        </div>
      </div>

      {/* Submit button */}
      <button
        type="button"
        onClick={handleSubmit}
        disabled={submitting}
        className={`w-full py-2.5 rounded-md font-bold text-sm transition-colors disabled:opacity-60 disabled:cursor-not-allowed ${buyBg}`}
        aria-label={`${isBuy ? 'Buy' : 'Sell'} ${baseAsset}`}
      >
        {submitting ? (
          <span className="flex items-center justify-center gap-2">
            <span className="w-3.5 h-3.5 border-2 border-current border-t-transparent rounded-full animate-spin" />
            Submitting...
          </span>
        ) : (
          `${isBuy ? 'Buy' : 'Sell'} ${baseAsset}`
        )}
      </button>
    </div>
  )
}
