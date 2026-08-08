import { useEffect, useState } from 'react'
import { Plus, ArrowUpRight, ArrowDownLeft, ArrowLeftRight, CheckCircle, Clock } from 'lucide-react'
import Sidebar from '../components/dashboard/Sidebar'
import { walletApi, type Balance } from '../api/wallet'

// ── Static meta per asset ──────────────────────────────────────────────────────
const ASSET_META: Record<string, { label: string; color: string; bg: string; initial: string }> = {
  USDT: { label: 'Tether',   color: '#10b981', bg: '#10b981', initial: 'U' },
  BTC:  { label: 'Bitcoin',  color: '#f7931a', bg: '#f7931a', initial: 'B' },
  ETH:  { label: 'Ethereum', color: '#627eea', bg: '#627eea', initial: 'E' },
  SOL:  { label: 'Solana',   color: '#9945FF', bg: '#9945FF', initial: 'S' },
}

// Fallback USD prices (replace with real market data later)
const USD_PRICES: Record<string, number> = {
  USDT: 1,
  BTC:  65000,
  ETH:  2250,
  SOL:  98.99,
}

// Mock transactions (replace with real API later)
const MOCK_TXS = [
  { type: 'Deposit',  asset: 'USDT', amount: '+5,000.00',  date: 'Today, 14:32',     status: 'Completed' },
  { type: 'Withdraw', asset: 'BTC',  amount: '-0.05 BTC',  date: 'Yesterday, 09:15', status: 'Pending'   },
  { type: 'Transfer', asset: 'ETH',  amount: '0.5 ETH',    date: 'Oct 24, 18:45',    status: 'Completed' },
]

// ── Donut chart ────────────────────────────────────────────────────────────────
function AllocationDonut({ balances }: { balances: Balance[] }) {
  const COLORS: Record<string, string> = {
    BTC: '#f7931a', USDT: '#10b981', ETH: '#627eea', SOL: '#9945FF',
  }

  const values = balances.map((b) => {
    const price = USD_PRICES[b.asset] ?? 1
    return parseFloat(b.availableBalance || '0') * price
  })
  const total = values.reduce((s, v) => s + v, 0) || 1

  let offset = 0
  const slices = balances.map((b, i) => {
    const pct = (values[i] / total) * 100
    const slice = { asset: b.asset, pct, offset, color: COLORS[b.asset] ?? '#6b7280' }
    offset += pct
    return slice
  })

  return (
    <div className="flex gap-6 items-center">
      {/* SVG donut */}
      <div className="relative w-28 h-28 shrink-0">
        <svg className="w-full h-full -rotate-90" viewBox="0 0 36 36">
          <circle cx="18" cy="18" r="15.915" fill="transparent" stroke="#1f2229" strokeWidth="4" />
          {slices.map((s) => (
            <circle
              key={s.asset}
              cx="18" cy="18" r="15.915"
              fill="transparent"
              stroke={s.color}
              strokeWidth="4"
              strokeDasharray={`${s.pct} ${100 - s.pct}`}
              strokeDashoffset={-(s.offset)}
            />
          ))}
        </svg>
      </div>
      {/* Legend */}
      <div className="flex flex-col gap-2.5 flex-1 text-xs font-mono">
        {slices.map((s) => (
          <div key={s.asset} className="flex justify-between items-center">
            <div className="flex items-center gap-1.5">
              <span className="w-2 h-2 rounded-full" style={{ background: s.color }} />
              <span className="text-slate-400">{s.asset}</span>
            </div>
            <span className="text-white">{s.pct.toFixed(1)}%</span>
          </div>
        ))}
      </div>
    </div>
  )
}

// ── Main page ──────────────────────────────────────────────────────────────────
export default function WalletPage() {
  const [balances, setBalances] = useState<Balance[]>([])
  const [loading, setLoading]   = useState(true)

  useEffect(() => {
    walletApi.getAllBalances()
      .then(setBalances)
      .catch(() => {
        // Fallback to static data if API fails
        setBalances([
          { asset: 'USDT', availableBalance: '12450.00', reservedBalance: '0.00' },
          { asset: 'BTC',  availableBalance: '0.234',    reservedBalance: '0.011' },
          { asset: 'ETH',  availableBalance: '1.8',      reservedBalance: '0.00' },
          { asset: 'SOL',  availableBalance: '15.0',     reservedBalance: '0.00' },
        ])
      })
      .finally(() => setLoading(false))
  }, [])

  // Derived totals
  const totalUsd = balances.reduce((sum, b) => {
    const price = USD_PRICES[b.asset] ?? 1
    const total = (parseFloat(b.availableBalance || '0') + parseFloat(b.reservedBalance || '0')) * price
    return sum + total
  }, 0)

  const reservedUsd = balances.reduce((sum, b) => {
    const price = USD_PRICES[b.asset] ?? 1
    return sum + parseFloat(b.reservedBalance || '0') * price
  }, 0)

  const availableUsd = totalUsd - reservedUsd

  const fmt = (n: number) =>
    '$' + n.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })

  return (
    <div className="bg-[#0a0b0e] text-white h-screen w-screen overflow-hidden flex font-sans text-sm select-none">
      <Sidebar />

      <div className="flex-1 flex flex-col min-w-0">

        {/* ── Header ── */}
        <header className="h-16 bg-[#111318]/80 backdrop-blur-md border-b border-[#1f2229] flex items-center justify-between px-6 flex-shrink-0">
          <div className="flex flex-col">
            <span className="font-bold text-white text-base">My Wallet</span>
            <span className="text-[11px] text-slate-400">Manage your assets &amp; balances</span>
          </div>
          <button className="flex items-center gap-2 bg-[#10b981] hover:bg-[#0e9f6e] text-black font-bold text-xs px-4 py-2 rounded-lg transition-colors shadow-[0_0_12px_rgba(16,185,129,0.3)]">
            <Plus size={14} />
            Deposit
          </button>
        </header>

        {/* ── Main scrollable content ── */}
        <main className="flex-1 overflow-y-auto p-5 flex flex-col gap-5">

          {/* Row 1 — Summary cards */}
          <div className="grid grid-cols-4 gap-4">
            {[
              { label: 'Total Balance',     value: loading ? '—' : fmt(totalUsd),     sub: '+2.45%', subColor: 'text-[#10b981]' },
              { label: 'Available',         value: loading ? '—' : fmt(availableUsd), sub: null,     subColor: '' },
              { label: 'Reserved in Orders',value: loading ? '—' : fmt(reservedUsd),  sub: null,     subColor: 'text-amber-400' },
              { label: 'Assets Held',       value: `${balances.length}`,              sub: 'assets', subColor: 'text-slate-400' },
            ].map(({ label, value, sub, subColor }) => (
              <div key={label} className="bg-[#111318] border border-[#1f2229] rounded-xl p-5 hover:bg-[#1e2025] transition-colors">
                <p className="text-[11px] text-slate-400 mb-3 uppercase tracking-wider font-semibold">{label}</p>
                <p className="text-2xl font-black font-mono text-white">{value}</p>
                {sub && <p className={`text-xs mt-1 font-mono ${subColor}`}>{sub}</p>}
              </div>
            ))}
          </div>

          {/* Row 2 — Asset Balances table */}
          <div className="bg-[#111318] border border-[#1f2229] rounded-xl p-5">
            <h2 className="font-semibold text-white mb-4">Asset Balances</h2>
            <div className="overflow-x-auto">
              <table className="w-full text-xs font-mono">
                <thead>
                  <tr className="text-slate-500 border-b border-[#1f2229]/50 text-left">
                    <th className="pb-3 font-medium">Asset</th>
                    <th className="pb-3 text-right font-medium">Available</th>
                    <th className="pb-3 text-right font-medium">Reserved</th>
                    <th className="pb-3 text-right font-medium">Total</th>
                    <th className="pb-3 text-right font-medium">Value (USD)</th>
                    <th className="pb-3 text-right font-medium">Actions</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-[#1f2229]/40">
                  {balances.map((b) => {
                    const meta  = ASSET_META[b.asset]
                    const price = USD_PRICES[b.asset] ?? 1
                    const avail = parseFloat(b.availableBalance || '0')
                    const resv  = parseFloat(b.reservedBalance  || '0')
                    const usd   = (avail + resv) * price
                    return (
                      <tr key={b.asset} className="group hover:bg-[#1e2025] transition-colors">
                        <td className="py-3.5">
                          <div className="flex items-center gap-3">
                            <div
                              className="w-8 h-8 rounded-full flex items-center justify-center text-white font-bold text-[11px]"
                              style={{ background: meta?.bg ?? '#6b7280' }}
                            >
                              {meta?.initial ?? b.asset[0]}
                            </div>
                            <div>
                              <p className="text-white font-semibold">{b.asset}</p>
                              <p className="text-slate-500 text-[10px]">{meta?.label ?? b.asset}</p>
                            </div>
                          </div>
                        </td>
                        <td className="py-3.5 text-right text-white">{b.availableBalance}</td>
                        <td className="py-3.5 text-right text-amber-400">{b.reservedBalance}</td>
                        <td className="py-3.5 text-right text-white">
                          {(avail + resv).toFixed(b.asset === 'USDT' ? 2 : 4)}
                        </td>
                        <td className="py-3.5 text-right text-white">{fmt(usd)}</td>
                        <td className="py-3.5 text-right">
                          <div className="flex justify-end gap-2 opacity-0 group-hover:opacity-100 transition-opacity">
                            {[
                              { label: 'Deposit',  cls: 'hover:text-[#10b981]' },
                              { label: 'Withdraw', cls: 'hover:text-amber-400'  },
                              { label: 'Transfer', cls: 'hover:text-blue-400'   },
                            ].map(({ label, cls }) => (
                              <button
                                key={label}
                                className={`px-2.5 py-1 bg-[#1e2025] border border-[#1f2229] rounded text-slate-300 ${cls} transition-colors`}
                              >
                                {label}
                              </button>
                            ))}
                          </div>
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          </div>

          {/* Row 3 — Transactions + Quick Actions */}
          <div className="grid grid-cols-5 gap-5">

            {/* Recent Transactions (60%) */}
            <div className="col-span-3 bg-[#111318] border border-[#1f2229] rounded-xl p-5">
              <div className="flex justify-between items-center mb-4">
                <h2 className="font-semibold text-white">Recent Transactions</h2>
                <button className="text-[11px] text-[#10b981] hover:underline">View All</button>
              </div>
              <div className="flex flex-col gap-2">
                {MOCK_TXS.map((tx, i) => {
                  const Icon = tx.type === 'Deposit' ? ArrowDownLeft : tx.type === 'Withdraw' ? ArrowUpRight : ArrowLeftRight
                  const iconBg = tx.type === 'Deposit' ? 'bg-[#10b981]/10 text-[#10b981]' : tx.type === 'Withdraw' ? 'bg-amber-400/10 text-amber-400' : 'bg-blue-400/10 text-blue-400'
                  const amtColor = tx.type === 'Deposit' ? 'text-[#10b981]' : 'text-white'
                  const badgeCls = tx.status === 'Completed' ? 'bg-[#10b981]/10 text-[#10b981]' : 'bg-amber-400/10 text-amber-400'
                  return (
                    <div key={i} className="flex items-center justify-between p-3.5 bg-[#0a0b0e] border border-[#1f2229] rounded-lg hover:bg-[#1e2025] transition-colors">
                      <div className="flex items-center gap-3">
                        <div className={`w-9 h-9 rounded-full ${iconBg} flex items-center justify-center`}>
                          <Icon size={15} />
                        </div>
                        <div>
                          <p className="text-white font-medium text-[13px]">{tx.type} {tx.asset}</p>
                          <p className="text-slate-500 text-[11px] mt-0.5">{tx.date}</p>
                        </div>
                      </div>
                      <div className="text-right">
                        <p className={`font-mono font-semibold ${amtColor}`}>{tx.amount}</p>
                        <span className={`inline-block mt-1 px-2 py-0.5 rounded text-[10px] font-semibold ${badgeCls}`}>
                          {tx.status === 'Completed' ? <CheckCircle className="inline w-2.5 h-2.5 mr-0.5" /> : <Clock className="inline w-2.5 h-2.5 mr-0.5" />}
                          {tx.status}
                        </span>
                      </div>
                    </div>
                  )
                })}
              </div>
            </div>

            {/* Right column (40%) */}
            <div className="col-span-2 flex flex-col gap-4">

              {/* Quick Actions */}
              <div className="bg-[#111318] border border-[#1f2229] rounded-xl p-5">
                <h3 className="font-semibold text-white mb-3">Quick Actions</h3>
                <div className="flex flex-col gap-2">
                  {[
                    { label: 'Deposit Funds',           icon: ArrowDownLeft, accent: '#10b981' },
                    { label: 'Withdraw Funds',           icon: ArrowUpRight,  accent: '#f59e0b' },
                    { label: 'Transfer Between Assets',  icon: ArrowLeftRight, accent: '#60a5fa' },
                  ].map(({ label, icon: Icon, accent }) => (
                    <button
                      key={label}
                      className="w-full flex items-center gap-3 p-3 bg-[#1e2025] border border-[#1f2229] rounded-lg hover:border-opacity-60 transition-all text-left group"
                      style={{ '--accent': accent } as React.CSSProperties}
                    >
                      <div
                        className="w-8 h-8 rounded-lg flex items-center justify-center text-white group-hover:scale-110 transition-transform"
                        style={{ background: `${accent}20`, color: accent }}
                      >
                        <Icon size={15} />
                      </div>
                      <span className="text-white text-[13px] font-medium">{label}</span>
                    </button>
                  ))}
                </div>
              </div>

              {/* Portfolio Allocation */}
              <div className="bg-[#111318] border border-[#1f2229] rounded-xl p-5 flex-1">
                <h3 className="font-semibold text-white mb-4">Allocation</h3>
                {balances.length > 0
                  ? <AllocationDonut balances={balances} />
                  : <p className="text-slate-500 text-xs text-center py-4">Loading…</p>
                }
              </div>
            </div>
          </div>
        </main>
      </div>
    </div>
  )
}
