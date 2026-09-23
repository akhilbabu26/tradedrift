import { Plus } from 'lucide-react'
import toast from 'react-hot-toast'
import type { Ticker24h, Market, Balance } from '../../types/trade'
import { formatPrice, formatPercentage, formatQuantity } from '../../utils/formatters'
import { toDecimal } from '../../utils/decimal'
import { getMarketMetadata } from '../../utils/marketMetadata'
import MarketSelector from './MarketSelector'

interface TradeMarketHeaderProps {
  markets: Market[]
  selectedMarketId: string
  onSelectMarket: (id: string) => void
  ticker: Ticker24h | null
  isDemoData: boolean
  usdtBalance: Balance
  baseBalance: Balance
}

function StatCell({ label, value, valueClass = '' }: { label: string; value: string; valueClass?: string }) {
  return (
    <div className="flex flex-col">
      <span className="text-[10px] text-slate-500 uppercase tracking-wider whitespace-nowrap">{label}</span>
      <span className={`text-xs font-mono font-semibold mt-0.5 whitespace-nowrap ${valueClass || 'text-[#f5f7fa]'}`}>
        {value}
      </span>
    </div>
  )
}

export default function TradeMarketHeader({
  markets,
  selectedMarketId,
  onSelectMarket,
  ticker,
  isDemoData,
  usdtBalance,
  baseBalance,
}: TradeMarketHeaderProps) {
  const meta = getMarketMetadata(selectedMarketId)
  const baseAsset = meta.base
  const quoteAsset = meta.quote
  const baseMeta = meta.baseMeta
  const quoteMeta = meta.quoteMeta

  const lastPrice  = ticker?.last_price  ?? '—'
  const change24h  = ticker?.price_change_24h_percent ?? '0'
  const high24h    = ticker?.high_24h    ?? '—'
  const low24h     = ticker?.low_24h     ?? '—'
  const volume24h  = ticker?.volume_24h  ?? '—'

  const changeNum     = parseFloat(change24h)
  const changePositive = changeNum >= 0
  const changeColor   = changePositive ? 'text-[#10b981]' : 'text-[#ef4444]'
  const changeBg      = changePositive ? 'bg-[#10b981]/10 border-[#10b981]/20' : 'bg-[#ef4444]/10 border-[#ef4444]/20'

  const usdtAvailable  = toDecimal(usdtBalance.availableBalance).toFixed(2)
  const usdtUsdValue   = `≈ $${formatPrice(usdtBalance.availableBalance)}`
  const baseAvailable  = formatQuantity(baseBalance.availableBalance, 6)

  const handleAddDemoFunds = () => {
    toast('Demo funding is not available yet. Your simulator starts with virtual funds.', {
      icon: '💡',
      duration: 4000,
    })
  }

  return (
    <div className="relative z-30 flex items-center border-b border-[#1e2530] bg-[#111318] px-4 h-14 flex-shrink-0">
      {/* Market Selector */}
      <div className="relative flex items-center pr-5 border-r border-[#1e2530] mr-5 flex-shrink-0">
        <MarketSelector
          markets={markets}
          selectedMarketId={selectedMarketId}
          onSelect={onSelectMarket}
        />
      </div>

      {/* Ticker stats & actions container - scrolls horizontally on small screens without clipping MarketSelector */}
      <div className="flex items-center gap-0 flex-1 min-w-0 overflow-x-auto h-full scrollbar-none">
        {/* Last Price */}
        <div className="flex flex-col mr-5 flex-shrink-0">
          <span className="text-[10px] text-slate-500 uppercase tracking-wider">Last Price</span>
          <span className="text-base font-bold font-mono text-[#f5f7fa] mt-0.5 whitespace-nowrap">
            {lastPrice !== '—' ? formatPrice(lastPrice) : '—'}
          </span>
        </div>

        {/* 24h Change */}
        <div className="flex flex-col mr-5 flex-shrink-0">
          <span className="text-[10px] text-slate-500 uppercase tracking-wider">24h Change</span>
          <span className={`text-xs font-mono font-semibold mt-0.5 px-1.5 py-0.5 rounded border ${changeBg} ${changeColor} whitespace-nowrap`}>
            {changePositive ? '+' : ''}{formatPercentage(change24h, false)}
          </span>
        </div>

        {/* 24h High */}
        <div className="hidden md:flex flex-col mr-5 flex-shrink-0">
          <StatCell label="24h High" value={high24h !== '—' ? formatPrice(high24h) : '—'} />
        </div>

        {/* 24h Low */}
        <div className="hidden md:flex flex-col mr-5 flex-shrink-0">
          <StatCell label="24h Low" value={low24h !== '—' ? formatPrice(low24h) : '—'} />
        </div>

        {/* 24h Volume */}
        <div className="hidden lg:flex flex-col mr-5 flex-shrink-0">
          <StatCell
            label={`24h Volume (${baseAsset})`}
            value={volume24h !== '—' ? formatQuantity(volume24h, 2) : '—'}
          />
        </div>

        {/* Demo indicator */}
        {isDemoData && (
          <div className="hidden md:flex items-center gap-1 px-2 py-0.5 rounded bg-amber-500/10 border border-amber-500/20 mr-4 flex-shrink-0">
            <span className="text-[10px] font-semibold text-amber-400 uppercase tracking-wider">DEMO</span>
          </div>
        )}

        {/* Spacer */}
        <div className="flex-1" />

        {/* USDT Balance */}
        <div className="hidden lg:flex flex-col items-end mr-4 flex-shrink-0">
          <div className="flex items-center gap-1.5">
            <div className={`w-4 h-4 rounded-full ${quoteMeta.iconBg} border ${quoteMeta.iconBorder} flex items-center justify-center flex-shrink-0`}>
              <span className={`text-[7px] font-black ${quoteMeta.iconColor}`}>{quoteMeta.symbol}</span>
            </div>
            <span className="text-[10px] text-slate-500 uppercase tracking-wider">{quoteAsset} Balance</span>
          </div>
          <span className="text-sm font-bold font-mono text-[#f5f7fa] mt-0.5">{formatPrice(usdtAvailable)}</span>
          <span className="text-[10px] text-slate-500">{usdtUsdValue}</span>
        </div>

        {/* Base Asset Balance */}
        <div className="hidden lg:flex flex-col items-end mr-4 flex-shrink-0">
          <div className="flex items-center gap-1.5">
            <div className={`w-4 h-4 rounded-full ${baseMeta.iconBg} border ${baseMeta.iconBorder} flex items-center justify-center flex-shrink-0`}>
              <span className={`text-[7px] font-black ${baseMeta.iconColor}`}>{baseMeta.symbol}</span>
            </div>
            <span className="text-[10px] text-slate-500 uppercase tracking-wider">{baseAsset} Balance</span>
          </div>
          <span className="text-sm font-bold font-mono text-[#f5f7fa] mt-0.5">{baseAvailable}</span>
        </div>

        {/* Add Demo Funds */}
        <button
          type="button"
          onClick={handleAddDemoFunds}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-[#10b981]/40 bg-[#10b981]/8 text-[#10b981] text-xs font-semibold hover:bg-[#10b981]/15 hover:border-[#10b981]/60 transition-colors flex-shrink-0"
          aria-label="Add demo funds — informational"
        >
          <Plus size={13} strokeWidth={2.5} />
          Add Demo Funds
        </button>
      </div>
    </div>
  )
}
