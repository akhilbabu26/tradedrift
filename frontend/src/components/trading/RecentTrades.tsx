import { ArrowUp, ArrowDown, ListFilter } from 'lucide-react'

export interface LiveTradeItem {
  id: string
  price: string
  quantity: string
  time: string
  side: 'BUY' | 'SELL'
}

interface RecentTradesProps {
  baseAsset: string
  quoteAsset: string
  trades: LiveTradeItem[]
}

const DEFAULT_TRADES: LiveTradeItem[] = [
  { id: '1', price: '96,450.00', quantity: '0.1250', time: '15:30:45.123', side: 'BUY' },
  { id: '2', price: '96,449.50', quantity: '0.0321', time: '15:30:45.987', side: 'SELL' },
  { id: '3', price: '96,450.00', quantity: '0.2500', time: '15:30:45.654', side: 'BUY' },
  { id: '4', price: '96,450.00', quantity: '0.0105', time: '15:30:45.321', side: 'BUY' },
  { id: '5', price: '96,447.50', quantity: '0.1200', time: '15:30:44.998', side: 'SELL' },
  { id: '6', price: '96,449.50', quantity: '0.3500', time: '15:30:44.765', side: 'BUY' },
  { id: '7', price: '96,450.00', quantity: '0.0750', time: '15:30:44.512', side: 'BUY' },
  { id: '8', price: '96,448.00', quantity: '0.2100', time: '15:30:44.201', side: 'SELL' },
  { id: '9', price: '96,445.50', quantity: '0.5000', time: '15:30:44.001', side: 'SELL' },
  { id: '10', price: '96,448.50', quantity: '0.0423', time: '15:30:43.876', side: 'BUY' },
]

export default function RecentTrades({ baseAsset, quoteAsset, trades }: RecentTradesProps) {
  const displayTrades = trades.length > 0 ? trades.slice(0, 15) : DEFAULT_TRADES

  return (
    <div className="bg-[#0e121b] border border-[#1b2230] rounded-xl flex flex-col overflow-hidden select-none shadow-xl">
      {/* Header */}
      <div className="h-10 border-b border-[#1b2230] px-3 flex items-center justify-between bg-[#07090e]/60 flex-shrink-0">
        <span className="text-xs font-bold text-white tracking-tight font-sans">Recent Trades</span>
        <button type="button" aria-label="Filter trades" className="text-slate-500 hover:text-slate-300">
          <ListFilter size={12} />
        </button>
      </div>

      {/* Column Titles */}
      <div className="grid grid-cols-3 px-3 py-1 text-[10px] font-mono text-slate-500 border-b border-[#1b2230]/60 bg-[#07090e]/30 flex-shrink-0">
        <div className="text-left">Price ({quoteAsset})</div>
        <div className="text-right">Amount ({baseAsset})</div>
        <div className="text-right">Time</div>
      </div>

      {/* Trades Tape */}
      <div className="flex-1 overflow-y-auto font-mono text-[11px] py-1 space-y-0.5 custom-scrollbar">
        {displayTrades.map((t, i) => (
          <div
            key={t.id || i}
            className="px-3 py-0.5 grid grid-cols-3 hover:bg-white/5 transition-colors items-center text-[11px]"
          >
            {/* Price with Arrow */}
            <div className={`flex items-center gap-1 font-semibold ${t.side === 'BUY' ? 'text-[#00e676]' : 'text-[#ff3366]'}`}>
              {t.side === 'BUY' ? <ArrowUp size={11} /> : <ArrowDown size={11} />}
              <span>{t.price}</span>
            </div>

            {/* Quantity */}
            <div className="text-right text-slate-300 font-medium">{t.quantity}</div>

            {/* Time */}
            <div className="text-right text-slate-500 text-[10px]">{t.time}</div>
          </div>
        ))}
      </div>
    </div>
  )
}
