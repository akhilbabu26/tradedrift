import { useState, useRef, useEffect } from 'react'
import { ChevronDown, Check } from 'lucide-react'
import type { Market } from '../../types/trade'
import { getMarketMetadata } from '../../utils/marketMetadata'

interface MarketSelectorProps {
  markets: Market[]
  selectedMarketId: string
  onSelect: (marketId: string) => void
}

export default function MarketSelector({ markets, selectedMarketId, onSelect }: MarketSelectorProps) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  const selected = getMarketMetadata(selectedMarketId)

  // Close dropdown on outside click or Escape key
  useEffect(() => {
    const handleClickOutside = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false)
      }
    }
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        setOpen(false)
      }
    }
    document.addEventListener('mousedown', handleClickOutside)
    document.addEventListener('keydown', handleKeyDown)
    return () => {
      document.removeEventListener('mousedown', handleClickOutside)
      document.removeEventListener('keydown', handleKeyDown)
    }
  }, [])

  const displayList = markets.length > 0 ? markets : [
    { id: 'BTC-USDT', base_asset: 'BTC', quote_asset: 'USDT', tick_size: '0.01', lot_size: '0.0001', status: 'ACTIVE', min_quantity: '0.0001', created_at: '', updated_at: '' },
    { id: 'ETH-USDT', base_asset: 'ETH', quote_asset: 'USDT', tick_size: '0.01', lot_size: '0.001',  status: 'ACTIVE', min_quantity: '0.001',  created_at: '', updated_at: '' },
    { id: 'SOL-USDT', base_asset: 'SOL', quote_asset: 'USDT', tick_size: '0.01', lot_size: '0.01',   status: 'ACTIVE', min_quantity: '0.01',   created_at: '', updated_at: '' },
  ]

  return (
    <div className="relative" ref={ref}>
      {/* Trigger */}
      <button
        type="button"
        id="market-selector-trigger"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        aria-haspopup="listbox"
        aria-label={`Select market, current: ${selected.pair}`}
        className="flex items-center gap-2 pr-1 group cursor-pointer"
      >
        {/* Asset icon */}
        <div className={`w-8 h-8 rounded-full ${selected.baseMeta.iconBg} border ${selected.baseMeta.iconBorder} flex items-center justify-center flex-shrink-0`}>
          <span className={`text-sm font-black ${selected.baseMeta.iconColor}`}>{selected.baseMeta.symbol}</span>
        </div>

        {/* Name + pair */}
        <div className="flex flex-col items-start leading-none">
          <div className="flex items-center gap-1">
            <span className="text-sm font-bold text-[#f5f7fa]">{selected.pair}</span>
            <ChevronDown
              size={13}
              className={`text-slate-500 transition-transform duration-150 ${open ? 'rotate-180' : ''}`}
            />
          </div>
          <span className="text-[11px] text-slate-500 mt-0.5">{selected.name}</span>
        </div>
      </button>

      {/* Dropdown */}
      {open && (
        <div
          role="listbox"
          aria-label="Market selection"
          className="absolute top-full left-0 mt-1.5 w-52 bg-[#111318] border border-[#1e2530] rounded-lg shadow-2xl z-50 overflow-hidden"
        >
          <div className="px-3 py-1.5 border-b border-[#1e2530]">
            <p className="text-[10px] font-semibold text-slate-500 uppercase tracking-wider">Select Market</p>
          </div>
          <div className="py-1">
            {displayList.map((m) => {
              const meta = getMarketMetadata(m.id)
              const isActive = m.id === selectedMarketId
              return (
                <button
                  key={m.id}
                  type="button"
                  role="option"
                  aria-selected={isActive}
                  onClick={() => {
                    onSelect(m.id)
                    setOpen(false)
                  }}
                  className={`w-full flex items-center gap-2.5 px-3 py-2 text-xs transition-colors cursor-pointer ${
                    isActive
                      ? 'bg-[#10b981]/10 text-[#10b981]'
                      : 'text-slate-300 hover:text-[#f5f7fa] hover:bg-white/5'
                  }`}
                >
                  <div className={`w-6 h-6 rounded-full ${meta.baseMeta.iconBg} border ${meta.baseMeta.iconBorder} flex items-center justify-center flex-shrink-0`}>
                    <span className={`text-[11px] font-black ${meta.baseMeta.iconColor}`}>{meta.baseMeta.symbol}</span>
                  </div>
                  <div className="flex-1 text-left">
                    <p className="font-semibold">{meta.pair}</p>
                    <p className="text-[10px] text-slate-500">{meta.name}</p>
                  </div>
                  {isActive && <Check size={13} className="text-[#10b981] flex-shrink-0" />}
                </button>
              )
            })}
          </div>
        </div>
      )}
    </div>
  )
}
