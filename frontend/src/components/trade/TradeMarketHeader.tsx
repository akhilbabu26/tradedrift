import { useRef, useEffect, memo } from 'react'
import { Plus, LogIn } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import type { Ticker24h, Market, Balance } from '../../types/trade'
import { formatPrice, formatPercentage, formatQuantity, formatHighLow, formatTurnover } from '../../utils/formatters'
import { toDecimal } from '../../utils/decimal'
import { getMarketMetadata } from '../../utils/marketMetadata'
import { useAuthStore } from '../../store/authStore'
import MarketSelector from './MarketSelector'

interface TradeMarketHeaderProps {
  markets: Market[]
  selectedMarketId: string
  onSelectMarket: (id: string) => void
  ticker: Ticker24h | null
  isDemoData: boolean
  usdtBalance: Balance
  baseBalance: Balance
  marketTrends?: Record<string, number[]>
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

function TradeMarketHeader({
  markets,
  selectedMarketId,
  onSelectMarket,
  ticker,
  isDemoData,
  usdtBalance,
  baseBalance,
  marketTrends,
}: TradeMarketHeaderProps) {
  const navigate = useNavigate()
  const isAuthenticated = useAuthStore((s) => s.isAuthenticated)
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
  const baseAvailable  = formatQuantity(baseBalance.availableBalance, 6)

  const handleAddDemoFunds = () => {
    navigate('/wallet')
  }

  const scrollRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (scrollRef.current) {
      scrollRef.current.scrollLeft = 0
    }
  }, [selectedMarketId])

  const handleWheel = (e: React.WheelEvent<HTMLDivElement>) => {
    if (e.deltaY !== 0) {
      e.currentTarget.scrollLeft += e.deltaY
    }
  }

  return (
    <div className="relative z-30 flex items-center border-b border-[#1e2530] bg-[#111318] px-2.5 sm:px-4 min-h-[58px] h-[58px] flex-shrink-0">
      {/* Market Selector */}
      <div className="relative flex items-center pr-2.5 sm:pr-4 border-r border-[#1e2530] mr-2.5 sm:mr-4 flex-shrink-0">
        <MarketSelector
          markets={markets}
          selectedMarketId={selectedMarketId}
          onSelect={onSelectMarket}
          marketTrends={marketTrends}
        />
      </div>

      {/* Ticker stats & actions container - scrolls horizontally on small screens without clipping */}
      <div
        ref={scrollRef}
        onWheel={handleWheel}
        className="flex items-center gap-0 flex-1 min-w-0 h-full overflow-x-auto trade-scrollbar-x py-1"
      >
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
        <div className="flex flex-col mr-5 flex-shrink-0">
          <StatCell label="24h High" value={formatHighLow(high24h)} />
        </div>

        {/* 24h Low */}
        <div className="flex flex-col mr-5 flex-shrink-0">
          <StatCell label="24h Low" value={formatHighLow(low24h)} />
        </div>

        {/* 24h Volume */}
        <div className="flex flex-col mr-5 flex-shrink-0">
          <StatCell
            label={`24h Volume (${baseAsset})`}
            value={volume24h !== '—' && parseFloat(volume24h) > 0 ? formatQuantity(volume24h, 2) : '—'}
          />
        </div>

        {/* 24h Turnover */}
        {ticker?.quote_volume_24h && parseFloat(ticker.quote_volume_24h) > 0 && (
          <div className="flex flex-col mr-5 flex-shrink-0">
            <StatCell
              label={`24h Turnover (${quoteAsset})`}
              value={formatTurnover(ticker.quote_volume_24h)}
            />
          </div>
        )}

        {/* Demo indicator */}
        {isDemoData && (
          <div className="flex items-center gap-1 px-2 py-0.5 rounded bg-amber-500/10 border border-amber-500/20 mr-4 flex-shrink-0">
            <span className="text-[10px] font-semibold text-amber-400 uppercase tracking-wider">DEMO</span>
          </div>
        )}

        {/* Spacer */}
        <div className="flex-1 min-w-[12px]" />

        {/* USDT Balance */}
        <div className="flex flex-col items-end mr-4 flex-shrink-0">
          <div className="flex items-center gap-1.5">
            <div className={`w-4 h-4 rounded-full ${quoteMeta.iconBg} border ${quoteMeta.iconBorder} flex items-center justify-center flex-shrink-0`}>
              <span className={`text-[7px] font-black ${quoteMeta.iconColor}`}>{quoteMeta.symbol}</span>
            </div>
            <span className="text-[10px] text-slate-500 uppercase tracking-wider whitespace-nowrap">{quoteAsset} Balance</span>
          </div>
          <span className="text-sm font-bold font-mono text-[#f5f7fa] mt-0.5 whitespace-nowrap">
            {isAuthenticated ? formatPrice(usdtAvailable) : '—'}
          </span>
        </div>

        {/* Base Asset Balance */}
        <div className="flex flex-col items-end mr-4 flex-shrink-0">
          <div className="flex items-center gap-1.5">
            <div className={`w-4 h-4 rounded-full ${baseMeta.iconBg} border ${baseMeta.iconBorder} flex items-center justify-center flex-shrink-0`}>
              <span className={`text-[7px] font-black ${baseMeta.iconColor}`}>{baseMeta.symbol}</span>
            </div>
            <span className="text-[10px] text-slate-500 uppercase tracking-wider whitespace-nowrap">{baseAsset} Balance</span>
          </div>
          <span className="text-sm font-bold font-mono text-[#f5f7fa] mt-0.5 whitespace-nowrap">
            {isAuthenticated ? baseAvailable : '—'}
          </span>
        </div>

        {/* Action Button: Add Demo Funds (if auth) or Sign In (if guest) */}
        {isAuthenticated ? (
          <button
            type="button"
            onClick={handleAddDemoFunds}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-[#10b981]/40 bg-[#10b981]/8 text-[#10b981] text-xs font-semibold hover:bg-[#10b981]/15 hover:border-[#10b981]/60 transition-colors flex-shrink-0 whitespace-nowrap"
            aria-label="Add demo funds — informational"
          >
            <Plus size={13} strokeWidth={2.5} />
            Add Demo Funds
          </button>
        ) : (
          <button
            type="button"
            onClick={() => navigate(`/login?returnUrl=${encodeURIComponent(`/trade?market=${selectedMarketId}`)}`)}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-md border border-[#10b981]/50 bg-[#10b981] text-black text-xs font-bold hover:bg-[#0ea572] transition-colors flex-shrink-0 whitespace-nowrap cursor-pointer shadow-[0_0_10px_rgba(16,185,129,0.2)]"
            aria-label="Sign In to Trade"
          >
            <LogIn size={13} />
            Sign In to Trade
          </button>
        )}
      </div>
    </div>
  )
}

export default memo(TradeMarketHeader)
