import { Plus } from 'lucide-react'
import type { Market, Ticker24h } from '../../api/market'

interface TradeMarketHeaderProps {
  markets: Market[]
  selectedMarketId: string
  onSelectMarket: (marketId: string) => void
  ticker: Ticker24h | null
  wsConnected: boolean
}

const DEFAULT_MARKET_PILLS = [
  { id: 'BTC-USDT', symbol: 'BTC/USDT', icon: '₿', iconBg: 'bg-[#f7931a]/15 text-[#f7931a] border-[#f7931a]/30', price: '$96,450.00', change: '+2.40%', up: true },
  { id: 'ETH-USDT', symbol: 'ETH/USDT', icon: 'Ξ', iconBg: 'bg-[#627eea]/15 text-[#627eea] border-[#627eea]/30', price: '$2,780.50', change: '+1.80%', up: true },
  { id: 'SOL-USDT', symbol: 'SOL/USDT', icon: 'S', iconBg: 'bg-[#00e5ff]/15 text-[#00e5ff] border-[#00e5ff]/30', price: '$188.20', change: '-0.60%', up: false },
]

export default function TradeMarketHeader({
  selectedMarketId,
  onSelectMarket,
  ticker,
  wsConnected,
}: TradeMarketHeaderProps) {
  const currentPriceRaw = ticker?.last_price || (selectedMarketId === 'BTC-USDT' ? '96450.00' : selectedMarketId === 'ETH-USDT' ? '2780.50' : '188.20')
  const numPrice = parseFloat(String(currentPriceRaw).replace(/,/g, '')) || 96450.00
  const isUp = (ticker?.price_change_24h_percent || '+').startsWith('+')

  const formattedPrice = `$${numPrice.toLocaleString('en-US', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  })}`

  // Dynamic 24h stats based on selected pair
  const stats = {
    high: ticker?.high_24h || (selectedMarketId === 'BTC-USDT' ? '97,200.00' : selectedMarketId === 'ETH-USDT' ? '2,845.00' : '194.50'),
    low: ticker?.low_24h || (selectedMarketId === 'BTC-USDT' ? '94,100.00' : selectedMarketId === 'ETH-USDT' ? '2,710.00' : '182.30'),
    volume: ticker?.volume_24h
      ? `${ticker.volume_24h} ${selectedMarketId.split('-')[0]}`
      : selectedMarketId === 'BTC-USDT'
      ? '42,850 BTC'
      : selectedMarketId === 'ETH-USDT'
      ? '185,420 ETH'
      : '890,150 SOL',
  }

  return (
    <div className="h-14 bg-[#0e121b] border-b border-[#1b2230] px-4 flex items-center justify-between flex-shrink-0 z-20 select-none">
      {/* ── Left: Horizontal Market Pill Switcher ── */}
      <div className="flex items-center gap-2 overflow-x-auto py-1">
        {DEFAULT_MARKET_PILLS.map((pill) => {
          const isActive = selectedMarketId === pill.id
          const displayPrice = isActive ? formattedPrice : pill.price
          const displayChange = isActive ? (ticker?.price_change_24h_percent || pill.change) : pill.change
          const displayUp = isActive ? isUp : pill.up

          return (
            <button
              key={pill.id}
              type="button"
              onClick={() => onSelectMarket(pill.id)}
              className={`flex items-center gap-2.5 px-3 py-1.5 rounded-xl text-xs font-mono font-medium transition-all ${
                isActive
                  ? 'bg-[#00e5ff]/10 border border-[#00e5ff]/50 text-white shadow-[0_0_15px_rgba(0,229,255,0.18)]'
                  : 'bg-[#07090e] border border-[#1b2230] text-slate-300 hover:border-slate-600 hover:bg-[#141a26]'
              }`}
            >
              <span className={`w-5 h-5 rounded-full border flex items-center justify-center font-bold text-[10px] ${pill.iconBg}`}>
                {pill.icon}
              </span>
              <span className="font-sans font-bold text-white tracking-tight">{pill.symbol}</span>
              <span className="font-medium text-white">{displayPrice}</span>
              <span className={`text-[11px] font-bold ${displayUp ? 'text-[#00e676]' : 'text-[#ff3366]'}`}>
                {displayChange}
              </span>
            </button>
          )
        })}

        {/* Plus / Add Market Pill */}
        <button
          type="button"
          aria-label="Add market"
          className="w-7 h-7 rounded-xl bg-[#07090e] border border-[#1b2230] hover:border-slate-500 text-slate-400 hover:text-white flex items-center justify-center transition-colors"
        >
          <Plus size={14} />
        </button>
      </div>

      {/* ── Right: 24h Real-Time Stats Ribbon ── */}
      <div className="hidden xl:flex items-center gap-6 text-[11px] font-mono">
        {/* 24h High */}
        <div className="flex flex-col">
          <span className="text-[10px] text-slate-500 font-sans uppercase tracking-wider">24h High</span>
          <span className="text-white font-medium">{stats.high}</span>
        </div>

        {/* 24h Low */}
        <div className="flex flex-col">
          <span className="text-[10px] text-slate-500 font-sans uppercase tracking-wider">24h Low</span>
          <span className="text-white font-medium">{stats.low}</span>
        </div>

        {/* 24h Volume */}
        <div className="flex flex-col">
          <span className="text-[10px] text-slate-500 font-sans uppercase tracking-wider">24h Volume</span>
          <span className="text-white font-medium">{stats.volume}</span>
        </div>

        {/* Funding / 8h */}
        <div className="flex flex-col">
          <span className="text-[10px] text-slate-500 font-sans uppercase tracking-wider">Funding / 8h</span>
          <span className="text-[#00e676] font-bold">0.0100%</span>
        </div>

        {/* Vertical Divider */}
        <div className="h-5 w-px bg-[#1b2230]" />

        {/* WS Latency */}
        <div className="flex items-center gap-2 px-2 py-1 rounded-lg bg-[#07090e] border border-[#1b2230]">
          <span className="relative flex h-2 w-2">
            {wsConnected && (
              <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-[#00e676] opacity-75" />
            )}
            <span className={`relative inline-flex rounded-full h-2 w-2 ${wsConnected ? 'bg-[#00e676]' : 'bg-amber-400'}`} />
          </span>
          <span className="text-[10px] text-slate-400 font-sans font-medium">WS Latency</span>
          <span className="text-[11px] font-bold text-[#00e676]">{wsConnected ? '12ms Live' : 'Reconnecting...'}</span>
        </div>
      </div>
    </div>
  )
}
