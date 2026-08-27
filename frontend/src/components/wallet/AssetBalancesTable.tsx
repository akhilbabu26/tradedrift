import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Search, Filter, Clock } from 'lucide-react'

export interface AssetRowData {
  symbol: string
  name: string
  iconBg: string
  iconText: string
  iconChar: string
  total: number
  available: number
  inOrders: number
  priceUsd: number
}

interface AssetBalancesTableProps {
  assets: AssetRowData[]
  onDeposit: (asset: string) => void
  onWithdraw: (asset: string) => void
}

export default function AssetBalancesTable({
  assets,
  onDeposit,
  onWithdraw,
}: AssetBalancesTableProps) {
  const navigate = useNavigate()
  const [searchQuery, setSearchQuery] = useState('')
  const [hideZero, setHideZero] = useState(false)

  const filteredAssets = assets.filter((a) => {
    const matchesSearch =
      a.symbol.toLowerCase().includes(searchQuery.toLowerCase()) ||
      a.name.toLowerCase().includes(searchQuery.toLowerCase())
    const matchesZero = hideZero ? a.total > 0 : true
    return matchesSearch && matchesZero
  })

  const fmtQty = (qty: number, symbol: string) => {
    const decimals = symbol === 'USDT' ? 4 : symbol === 'SOL' ? 4 : 8
    return qty.toLocaleString('en-US', {
      minimumFractionDigits: decimals,
      maximumFractionDigits: decimals,
    })
  }

  const fmtUsd = (usd: number) =>
    `$${usd.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`

  return (
    <div className="bg-[#0e121b] border border-[#1b2230] rounded-xl flex flex-col overflow-hidden shadow-xl">
      {/* ── Section Header & Filter Controls ── */}
      <div className="p-4 border-b border-[#1b2230] flex flex-wrap items-center justify-between gap-3 bg-[#07090e]/40">
        <div className="flex items-center gap-2">
          <Clock size={16} className="text-slate-400" />
          <h2 className="text-sm font-bold text-white font-sans tracking-tight">Asset Balances</h2>
        </div>

        {/* Search, Hide Zero & Filter */}
        <div className="flex items-center gap-3">
          {/* Hide Zero Balances Checkbox */}
          <label className="flex items-center gap-2 text-xs text-slate-400 hover:text-slate-200 cursor-pointer select-none">
            <input
              type="checkbox"
              checked={hideZero}
              onChange={(e) => setHideZero(e.target.checked)}
              className="rounded border-[#1b2230] bg-[#07090e] text-[#00e5ff] focus:ring-0 focus:ring-offset-0 w-3.5 h-3.5 cursor-pointer accent-[#00e5ff]"
            />
            <span>Hide Zero Balances</span>
          </label>

          {/* Search Input */}
          <div className="relative">
            <Search size={13} className="absolute left-2.5 top-1/2 -translate-y-1/2 text-slate-500" />
            <input
              type="text"
              placeholder="Search assets..."
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              className="pl-8 pr-3 py-1.5 rounded-lg bg-[#07090e] border border-[#1b2230] text-xs text-white placeholder-slate-500 focus:outline-none focus:border-[#00e5ff]/50 w-40 md:w-52 transition-all font-sans"
            />
          </div>

          {/* Filter Button */}
          <button
            type="button"
            aria-label="Filter assets"
            className="p-1.5 rounded-lg bg-[#07090e] border border-[#1b2230] text-slate-400 hover:text-white hover:border-slate-500 transition-colors"
          >
            <Filter size={14} />
          </button>
        </div>
      </div>

      {/* ── Balances Table ── */}
      <div className="overflow-x-auto custom-scrollbar">
        <table className="w-full text-xs font-mono text-left border-collapse">
          <thead>
            <tr className="border-b border-[#1b2230] bg-[#07090e]/60 text-slate-400 text-[11px] font-sans font-medium">
              <th className="py-3 px-4">Asset</th>
              <th className="py-3 px-4 text-right">Total Balance</th>
              <th className="py-3 px-4 text-right">Available to Trade</th>
              <th className="py-3 px-4 text-right">In-Orders</th>
              <th className="py-3 px-4 text-right">USD Value</th>
              <th className="py-3 px-4 text-right">Actions</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[#1b2230]/60">
            {filteredAssets.map((asset) => {
              const totalUsd = asset.total * asset.priceUsd
              const availUsd = asset.available * asset.priceUsd
              const inOrderUsd = asset.inOrders * asset.priceUsd

              return (
                <tr
                  key={asset.symbol}
                  className="hover:bg-[#141a26]/60 transition-colors group"
                >
                  {/* Asset Icon + Name */}
                  <td className="py-3.5 px-4 font-sans">
                    <div className="flex items-center gap-3">
                      <div
                        className={`w-8 h-8 rounded-full flex items-center justify-center font-bold text-xs flex-shrink-0 border ${asset.iconBg} ${asset.iconText}`}
                      >
                        {asset.iconChar}
                      </div>
                      <div className="flex flex-col min-w-0">
                        <span className="font-bold text-white text-xs tracking-tight">
                          {asset.symbol}
                        </span>
                        <span className="text-[11px] text-slate-400 truncate">
                          {asset.name}
                        </span>
                      </div>
                    </div>
                  </td>

                  {/* Total Balance */}
                  <td className="py-3.5 px-4 text-right">
                    <div className="font-bold text-white text-xs">
                      {fmtQty(asset.total, asset.symbol)}
                    </div>
                    <div className="text-[10px] text-slate-500 mt-0.5">
                      {fmtUsd(totalUsd)}
                    </div>
                  </td>

                  {/* Available to Trade */}
                  <td className="py-3.5 px-4 text-right">
                    <div className="font-semibold text-slate-200 text-xs">
                      {fmtQty(asset.available, asset.symbol)}
                    </div>
                    <div className="text-[10px] text-slate-500 mt-0.5">
                      {fmtUsd(availUsd)}
                    </div>
                  </td>

                  {/* In-Orders */}
                  <td className="py-3.5 px-4 text-right">
                    <div className="font-medium text-amber-400 text-xs">
                      {fmtQty(asset.inOrders, asset.symbol)}
                    </div>
                    <div className="text-[10px] text-slate-500 mt-0.5">
                      {fmtUsd(inOrderUsd)}
                    </div>
                  </td>

                  {/* USD Value */}
                  <td className="py-3.5 px-4 text-right">
                    <div className="font-bold text-white text-xs">
                      {fmtUsd(totalUsd)}
                    </div>
                    <div className="text-[10px] text-slate-500 mt-0.5">
                      {asset.symbol === 'USDT'
                        ? `${fmtQty(asset.total, 'USDT')} USDT`
                        : `${fmtQty(asset.total, asset.symbol)} ${asset.symbol}`}
                    </div>
                  </td>

                  {/* Actions (Deposit, Withdraw, Trade) */}
                  <td className="py-3.5 px-4 text-right">
                    <div className="flex items-center justify-end gap-1.5 font-sans">
                      {/* Deposit Button */}
                      <button
                        type="button"
                        onClick={() => onDeposit(asset.symbol)}
                        className="px-2.5 py-1 rounded-lg text-[11px] font-semibold bg-[#00e676]/10 text-[#00e676] border border-[#00e676]/30 hover:bg-[#00e676]/20 transition-all shadow-[0_0_8px_rgba(0,230,118,0.15)]"
                      >
                        Deposit
                      </button>

                      {/* Withdraw Button */}
                      <button
                        type="button"
                        onClick={() => onWithdraw(asset.symbol)}
                        className="px-2.5 py-1 rounded-lg text-[11px] font-semibold bg-[#141a26] text-slate-300 border border-[#1b2230] hover:text-white hover:border-slate-500 transition-colors"
                      >
                        Withdraw
                      </button>

                      {/* Trade Button */}
                      <button
                        type="button"
                        onClick={() => {
                          const pair = asset.symbol === 'USDT' ? 'BTC-USDT' : `${asset.symbol}-USDT`
                          navigate(`/trade?market=${pair}`)
                        }}
                        className="px-2.5 py-1 rounded-lg text-[11px] font-semibold bg-[#00e5ff]/10 text-[#00e5ff] border border-[#00e5ff]/30 hover:bg-[#00e5ff]/20 transition-all shadow-[0_0_8px_rgba(0,229,255,0.15)]"
                      >
                        Trade
                      </button>
                    </div>
                  </td>
                </tr>
              )
            })}

            {filteredAssets.length === 0 && (
              <tr>
                <td colSpan={6} className="py-8 text-center text-slate-500 font-sans">
                  No assets found matching your filter criteria.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
