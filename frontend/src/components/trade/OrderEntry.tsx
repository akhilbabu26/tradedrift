import { useState } from 'react'
import type { OrderSide, OrderType, Market, Balance, CreateOrderRequest } from '../../types/trade'
import OrderForm from './OrderForm'

interface OrderEntryProps {
  market: Market
  lastPrice: string
  quoteBalance: Balance
  baseBalance: Balance
  submitting: boolean
  onSubmit: (req: CreateOrderRequest) => Promise<boolean>
}

const SIDES: { key: OrderSide; label: string }[] = [
  { key: 'BUY',  label: 'Buy'  },
  { key: 'SELL', label: 'Sell' },
]

const ORDER_TYPES: { key: OrderType; label: string }[] = [
  { key: 'LIMIT',  label: 'Limit'  },
  { key: 'MARKET', label: 'Market' },
]

export default function OrderEntry({
  market,
  lastPrice,
  quoteBalance,
  baseBalance,
  submitting,
  onSubmit,
}: OrderEntryProps) {
  const [side,      setSide]      = useState<OrderSide>('BUY')
  const [orderType, setOrderType] = useState<OrderType>('LIMIT')

  return (
    /*
     * h-full: fills the grid cell
     * flex flex-col: stacks tabs → form vertically
     * min-h-0: allows shrinking within grid cell
     * overflow-hidden: form itself scrolls internally
     */
    <div className="h-full flex flex-col min-h-0 overflow-hidden border-l border-[#1e2530] bg-[#111318]">

      {/* ── Buy / Sell tab ────────────────────────────────────────────────── */}
      <div className="flex border-b border-[#1e2530] flex-shrink-0" role="tablist" aria-label="Order side">
        {SIDES.map(({ key, label }) => {
          const active   = side === key
          const isBuyBtn = key === 'BUY'
          return (
            <button
              key={key}
              role="tab"
              aria-selected={active}
              onClick={() => setSide(key)}
              className={`flex-1 py-2.5 text-sm font-bold transition-colors border-b-2 ${
                active
                  ? isBuyBtn
                    ? 'text-[#10b981] border-[#10b981] bg-[#10b981]/5'
                    : 'text-[#ef4444] border-[#ef4444] bg-[#ef4444]/5'
                  : 'text-slate-500 border-transparent hover:text-[#f5f7fa]'
              }`}
            >
              {label}
            </button>
          )
        })}
      </div>

      {/* ── Limit / Market tab ────────────────────────────────────────────── */}
      <div className="flex items-center gap-0.5 px-3 py-2 border-b border-[#1e2530] flex-shrink-0" role="tablist" aria-label="Order type">
        {ORDER_TYPES.map(({ key, label }) => {
          const active = orderType === key
          return (
            <button
              key={key}
              role="tab"
              aria-selected={active}
              onClick={() => setOrderType(key)}
              className={`px-3 py-1 rounded text-xs font-medium transition-colors ${
                active
                  ? 'bg-[#0a0b0e] text-[#f5f7fa] border border-[#1e2530]'
                  : 'text-slate-500 hover:text-[#f5f7fa]'
              }`}
            >
              {label}
            </button>
          )
        })}
      </div>

      {/* ── Order Form — scrolls internally when content overflows ─────────── */}
      <div className="flex-1 min-h-0 overflow-y-auto">
        <OrderForm
          key={market.id}
          side={side}
          orderType={orderType}
          market={market}
          lastPrice={lastPrice}
          availableQuote={quoteBalance}
          availableBase={baseBalance}
          submitting={submitting}
          onSubmit={onSubmit}
        />
      </div>
    </div>
  )
}
