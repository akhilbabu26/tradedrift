import { useState, useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import { Search, ChevronDown, Plus, ChevronRight, Check } from 'lucide-react'
import type { EnrichedAssetRow } from '../../hooks/useWalletData'
import { formatPrice } from '../../utils/formatters'

interface WalletAssetsProps {
  assets: EnrichedAssetRow[]
  loading?: boolean
  onTopUpClick: () => void
}

export default function WalletAssets({ assets, loading = false, onTopUpClick }: WalletAssetsProps) {
  const navigate = useNavigate()
  const [search, setSearch] = useState('')
  const [hideZero, setHideZero] = useState(false)

  // Filter assets by search query & zero balance toggle
  const filteredAssets = useMemo(() => {
    let list = assets

    if (hideZero) {
      list = list.filter((a) => !a.isZeroBalance)
    }

    const q = search.trim().toLowerCase()
    if (q) {
      list = list.filter(
        (a) =>
          a.asset.toLowerCase().includes(q) ||
          a.name.toLowerCase().includes(q)
      )
    }

    return list
  }, [assets, search, hideZero])

  const handleTrade = (assetSymbol: string) => {
    navigate(`/trade?market=${assetSymbol.toUpperCase()}-USDT`)
  }

  return (
    <div className="rounded-xl border border-[#1e2530] bg-[#111318] overflow-hidden shadow-sm flex flex-col h-full">
      {/* ── Header ─────────────────────────────────────────────────────────── */}
      <div className="p-4 sm:p-5 flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 border-b border-[#1e2530]">
        <h2 className="text-base font-bold text-[#f5f7fa]" style={{ color: '#f5f7fa' }}>
          Assets in Wallet
        </h2>

        <div className="flex items-center gap-3 sm:gap-4 flex-wrap">
          {/* Search Box */}
          <div className="relative flex items-center">
            <Search size={13} className="absolute left-3 text-slate-400 pointer-events-none" />
            <input
              type="text"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search assets..."
              className="bg-[#0a0b0e] border border-[#1e2530] rounded-lg pl-8 pr-3 py-1.5 text-xs text-[#f5f7fa] placeholder-slate-400 focus:outline-none focus:border-[#10b981]/50 w-36 sm:w-44 transition-colors"
            />
          </div>

          {/* Hide Zero Balances Toggle */}
          <label className="flex items-center gap-2 text-xs text-slate-200 cursor-pointer select-none">
            <div
              onClick={() => setHideZero((v) => !v)}
              className={`w-4 h-4 rounded flex items-center justify-center border transition-colors ${
                hideZero
                  ? 'bg-[#10b981] border-[#10b981] text-[#0a0b0e]'
                  : 'bg-[#0a0b0e] border-[#1e2530] text-transparent hover:border-slate-400'
              }`}
            >
              <Check size={11} strokeWidth={3} />
            </div>
            <span>Hide zero balances</span>
          </label>
        </div>
      </div>

      {/* ── Table ──────────────────────────────────────────────────────────── */}
      <div className="overflow-x-auto flex-1 flex flex-col">
        <table className="w-full text-left text-xs">
          <thead>
            <tr className="border-b border-[#1e2530]/80 text-slate-300 uppercase tracking-wider text-[11px] font-semibold">
              <th className="py-3 px-4 sm:px-5">
                <span className="flex items-center gap-1">
                  Asset <ChevronDown size={12} className="text-slate-400" />
                </span>
              </th>
              <th className="py-3 px-4 sm:px-5 text-right">Total Balance</th>
              <th className="py-3 px-4 sm:px-5 text-right">Available</th>
              <th className="py-3 px-4 sm:px-5 text-right">In Orders</th>
              <th className="py-3 px-4 sm:px-5 text-right">Value (USDT)</th>
              <th className="py-3 px-4 sm:px-5 text-center">Action</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[#1e2530]/50">
            {loading ? (
              // Loading skeletons
              Array.from({ length: 3 }).map((_, i) => (
                <tr key={i} className="animate-pulse">
                  <td className="py-3.5 px-4 sm:px-5">
                    <div className="flex items-center gap-3">
                      <div className="w-8 h-8 rounded-full bg-[#1e2530]" />
                      <div className="space-y-1">
                        <div className="h-3 w-12 bg-[#1e2530] rounded" />
                        <div className="h-2.5 w-16 bg-[#1e2530] rounded" />
                      </div>
                    </div>
                  </td>
                  <td className="py-3.5 px-4 sm:px-5 text-right"><div className="h-3 w-16 bg-[#1e2530] rounded ml-auto" /></td>
                  <td className="py-3.5 px-4 sm:px-5 text-right"><div className="h-3 w-16 bg-[#1e2530] rounded ml-auto" /></td>
                  <td className="py-3.5 px-4 sm:px-5 text-right"><div className="h-3 w-12 bg-[#1e2530] rounded ml-auto" /></td>
                  <td className="py-3.5 px-4 sm:px-5 text-right"><div className="h-3 w-20 bg-[#1e2530] rounded ml-auto" /></td>
                  <td className="py-3.5 px-4 sm:px-5 text-center"><div className="h-6 w-16 bg-[#1e2530] rounded mx-auto" /></td>
                </tr>
              ))
            ) : filteredAssets.length === 0 ? (
              // Empty State
              <tr>
                <td colSpan={6} className="py-8 text-center text-slate-400">
                  {search ? `No assets matching "${search}"` : 'No assets found'}
                </td>
              </tr>
            ) : (
              filteredAssets.map((a) => {
                const isUSDT = a.asset === 'USDT'
                return (
                  <tr
                    key={a.asset}
                    className="hover:bg-white/[0.02] transition-colors"
                  >
                    {/* Asset Icon & Label */}
                    <td className="py-3.5 px-4 sm:px-5 whitespace-nowrap">
                      <div className="flex items-center gap-3">
                        <div
                          className={`w-8 h-8 rounded-full ${a.meta.iconBg} border ${a.meta.iconBorder} flex items-center justify-center flex-shrink-0`}
                        >
                          <span className={`text-xs font-black ${a.meta.iconColor}`}>
                            {a.meta.symbol}
                          </span>
                        </div>
                        <div className="flex flex-col">
                          <span className="font-bold text-sm text-[#f5f7fa]">
                            {a.asset}
                          </span>
                          <span className="text-[11px] text-slate-300 font-medium">
                            {a.name}
                          </span>
                        </div>
                      </div>
                    </td>

                    {/* Total Balance */}
                    <td className="py-3.5 px-4 sm:px-5 text-right font-mono font-semibold text-[#f5f7fa] whitespace-nowrap">
                      {formatPrice(a.totalBalance)}
                    </td>

                    {/* Available */}
                    <td className="py-3.5 px-4 sm:px-5 text-right font-mono text-slate-200 whitespace-nowrap">
                      {formatPrice(a.availableBalance)}
                    </td>

                    {/* In Orders */}
                    <td className="py-3.5 px-4 sm:px-5 text-right font-mono text-slate-300 whitespace-nowrap">
                      {formatPrice(a.reservedBalance)}
                    </td>

                    {/* Value (USDT) */}
                    <td className="py-3.5 px-4 sm:px-5 text-right font-mono font-bold text-[#f5f7fa] whitespace-nowrap">
                      ${formatPrice(a.valueUSDT)}
                    </td>

                    {/* Action Button */}
                    <td className="py-3.5 px-4 sm:px-5 text-center whitespace-nowrap">
                      {isUSDT ? (
                        <button
                          type="button"
                          onClick={onTopUpClick}
                          className="inline-flex items-center gap-1 px-3 py-1 rounded-md border border-[#10b981]/40 bg-[#10b981]/8 text-[#10b981] hover:bg-[#10b981]/15 text-xs font-semibold transition-colors cursor-pointer"
                        >
                          <Plus size={12} strokeWidth={2.5} />
                          Top Up
                        </button>
                      ) : (
                        <button
                          type="button"
                          onClick={() => handleTrade(a.asset)}
                          className="inline-flex items-center gap-0.5 px-3 py-1 rounded-md border border-[#1e2530] bg-[#0a0b0e] text-[#10b981] hover:border-[#10b981]/40 hover:bg-white/5 text-xs font-semibold transition-colors cursor-pointer"
                        >
                          Trade <ChevronRight size={13} />
                        </button>
                      )}
                    </td>
                  </tr>
                )
              })
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
