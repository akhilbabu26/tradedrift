import { useState } from 'react'
import {
  Calendar,
  Filter,
  Download,
  Copy,
  Check,
  Droplet,
  TrendingUp,
  Lock,
  ArrowUpRight,
  ChevronDown,
  CheckCircle2,
} from 'lucide-react'
import toast from 'react-hot-toast'

export interface LedgerItem {
  id: string
  txHash: string
  type: 'Faucet Deposit' | 'Trade Fill' | 'Order Lock' | 'Withdrawal'
  asset: string
  amount: string
  amountNum: number
  usdValue: string
  dateTimeUtc: string
  status: 'Completed' | 'Pending' | 'Failed'
}

interface FinancialLedgerProps {
  transactions?: LedgerItem[]
}

const DEFAULT_TRANSACTIONS: LedgerItem[] = [
  {
    id: '1',
    txHash: '0x7b2f...a9c8e4d1',
    type: 'Faucet Deposit',
    asset: 'USDT',
    amount: '+10,000.00 USDT',
    amountNum: 10000,
    usdValue: '+$10,000.00',
    dateTimeUtc: 'May 26, 2025 12:45:33.123',
    status: 'Completed',
  },
  {
    id: '2',
    txHash: '0x3c9a...f6d2b7e8',
    type: 'Trade Fill',
    asset: 'BTC',
    amount: '-0.05000000 BTC',
    amountNum: -0.05,
    usdValue: '-$4,822.50',
    dateTimeUtc: 'May 26, 2025 12:32:18.456',
    status: 'Completed',
  },
  {
    id: '3',
    txHash: '0x8e1d...7a3f9b2c',
    type: 'Order Lock',
    asset: 'USDT',
    amount: '-1,500.00 USDT',
    amountNum: -1500,
    usdValue: '-$1,500.00',
    dateTimeUtc: 'May 26, 2025 12:31:05.789',
    status: 'Completed',
  },
  {
    id: '4',
    txHash: '0x2f4e...b1a8d6c7',
    type: 'Trade Fill',
    asset: 'ETH',
    amount: '+0.25000000 ETH',
    amountNum: 0.25,
    usdValue: '+$695.50',
    dateTimeUtc: 'May 26, 2025 11:58:44.321',
    status: 'Completed',
  },
  {
    id: '5',
    txHash: '0x9a7b...e3d1f6a9',
    type: 'Faucet Deposit',
    asset: 'BTC',
    amount: '+1.00000000 BTC',
    amountNum: 1.0,
    usdValue: '+$96,450.00',
    dateTimeUtc: 'May 26, 2025 11:20:10.654',
    status: 'Completed',
  },
  {
    id: '6',
    txHash: '0x6c3d...4e8f1b7a',
    type: 'Withdrawal',
    asset: 'USDT',
    amount: '-2,000.00 USDT',
    amountNum: -2000,
    usdValue: '-$2,000.00',
    dateTimeUtc: 'May 25, 2025 18:43:21.987',
    status: 'Completed',
  },
]

export default function FinancialLedger({ transactions = DEFAULT_TRANSACTIONS }: FinancialLedgerProps) {
  const [copiedId, setCopiedId] = useState<string | null>(null)
  const [currentPage, setCurrentPage] = useState(1)
  const [rowsPerPage, setRowsPerPage] = useState('10')

  const copyTxHash = (id: string, hash: string) => {
    navigator.clipboard.writeText(hash)
    setCopiedId(id)
    toast.success('Tx hash copied to clipboard')
    setTimeout(() => {
      setCopiedId((prev) => (prev === id ? null : prev))
    }, 2000)
  }

  const exportCsv = () => {
    const headers = ['Tx Hash', 'Type', 'Asset', 'Amount', 'USD Value', 'Date / Time (UTC)', 'Status']
    const rows = transactions.map((t) => [
      t.txHash,
      t.type,
      t.asset,
      t.amount,
      t.usdValue,
      t.dateTimeUtc,
      t.status,
    ])
    const csvContent = 'data:text/csv;charset=utf-8,' + [headers.join(','), ...rows.map((e) => e.join(','))].join('\n')
    const encodedUri = encodeURI(csvContent)
    const link = document.createElement('a')
    link.setAttribute('href', encodedUri)
    link.setAttribute('download', `tradedrift_ledger_${Date.now()}.csv`)
    document.body.appendChild(link)
    link.click()
    document.body.removeChild(link)
    toast.success('Financial ledger exported to CSV')
  }

  const renderTypeIcon = (type: LedgerItem['type']) => {
    switch (type) {
      case 'Faucet Deposit':
        return (
          <div className="flex items-center gap-2 text-[#00e676]">
            <Droplet size={13} className="text-[#00e676]" />
            <span className="font-medium text-slate-200">Faucet Deposit</span>
          </div>
        )
      case 'Trade Fill':
        return (
          <div className="flex items-center gap-2 text-[#00e5ff]">
            <TrendingUp size={13} className="text-[#00e5ff]" />
            <span className="font-medium text-slate-200">Trade Fill</span>
          </div>
        )
      case 'Order Lock':
        return (
          <div className="flex items-center gap-2 text-amber-400">
            <Lock size={13} className="text-amber-400" />
            <span className="font-medium text-slate-200">Order Lock</span>
          </div>
        )
      case 'Withdrawal':
        return (
          <div className="flex items-center gap-2 text-[#ff3366]">
            <ArrowUpRight size={13} className="text-[#ff3366]" />
            <span className="font-medium text-slate-200">Withdrawal</span>
          </div>
        )
    }
  }

  const renderAssetBadge = (asset: string) => {
    const isUsdt = asset === 'USDT'
    const isBtc = asset === 'BTC'
    const isEth = asset === 'ETH'

    const bg = isUsdt
      ? 'bg-[#00e676]/15 text-[#00e676] border-[#00e676]/30'
      : isBtc
      ? 'bg-[#f7931a]/15 text-[#f7931a] border-[#f7931a]/30'
      : isEth
      ? 'bg-[#627eea]/15 text-[#627eea] border-[#627eea]/30'
      : 'bg-[#00e5ff]/15 text-[#00e5ff] border-[#00e5ff]/30'

    const char = isUsdt ? '₮' : isBtc ? '₿' : isEth ? 'Ξ' : 'S'

    return (
      <div className="flex items-center gap-2">
        <span className={`w-5 h-5 rounded-full flex items-center justify-center font-bold text-[10px] border ${bg}`}>
          {char}
        </span>
        <span className="font-bold text-white tracking-tight">{asset}</span>
      </div>
    )
  }

  return (
    <div className="bg-[#0e121b] border border-[#1b2230] rounded-xl flex flex-col overflow-hidden shadow-xl select-none">
      {/* ── Header: Title & Subtitle + Date / Filter / Export ── */}
      <div className="p-4 border-b border-[#1b2230] flex flex-wrap items-center justify-between gap-4 bg-[#07090e]/40">
        <div>
          <h2 className="text-sm font-bold text-white font-sans tracking-tight">
            Financial Ledger
          </h2>
          <p className="text-xs text-slate-400 font-sans mt-0.5">
            All wallet transactions and account activity
          </p>
        </div>

        <div className="flex items-center gap-2.5">
          {/* Date Range Selector */}
          <button
            type="button"
            className="flex items-center gap-2 px-3 py-1.5 rounded-lg bg-[#07090e] border border-[#1b2230] text-xs font-mono text-slate-300 hover:text-white hover:border-slate-500 transition-colors"
          >
            <Calendar size={13} className="text-slate-400" />
            <span>May 19, 2025 – May 26, 2025</span>
          </button>

          {/* Filter */}
          <button
            type="button"
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-[#07090e] border border-[#1b2230] text-xs font-sans font-medium text-slate-300 hover:text-white hover:border-slate-500 transition-colors"
          >
            <Filter size={13} className="text-slate-400" />
            <span>Filter</span>
          </button>

          {/* Export CSV */}
          <button
            type="button"
            onClick={exportCsv}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-[#07090e] border border-[#1b2230] text-xs font-sans font-medium text-slate-300 hover:text-white hover:border-slate-500 transition-colors"
          >
            <Download size={13} className="text-slate-400" />
            <span>Export CSV</span>
          </button>
        </div>
      </div>

      {/* ── Transactions Table ── */}
      <div className="overflow-x-auto custom-scrollbar">
        <table className="w-full text-xs font-mono text-left border-collapse">
          <thead>
            <tr className="border-b border-[#1b2230] bg-[#07090e]/60 text-slate-400 text-[11px] font-sans font-medium">
              <th className="py-3 px-4">Tx Hash</th>
              <th className="py-3 px-4">Type</th>
              <th className="py-3 px-4">Asset</th>
              <th className="py-3 px-4 text-right">Amount</th>
              <th className="py-3 px-4 text-right">USD Value</th>
              <th className="py-3 px-4">Date / Time (UTC)</th>
              <th className="py-3 px-4 text-right">Status</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[#1b2230]/60">
            {transactions.map((tx) => {
              const isPositive = tx.amountNum > 0
              const isLocked = tx.type === 'Order Lock'
              const amountColor = isLocked
                ? 'text-amber-400'
                : isPositive
                ? 'text-[#00e676]'
                : 'text-[#ff3366]'

              return (
                <tr
                  key={tx.id}
                  className="hover:bg-[#141a26]/60 transition-colors group"
                >
                  {/* Tx Hash with Copy button */}
                  <td className="py-3.5 px-4 font-mono">
                    <div className="flex items-center gap-1.5">
                      <span className="text-[#00e5ff] hover:underline cursor-pointer">
                        {tx.txHash}
                      </span>
                      <button
                        type="button"
                        onClick={() => copyTxHash(tx.id, tx.txHash)}
                        aria-label="Copy Tx Hash"
                        className="text-slate-500 hover:text-slate-300 p-0.5 rounded transition-colors"
                      >
                        {copiedId === tx.id ? (
                          <Check size={12} className="text-[#00e676]" />
                        ) : (
                          <Copy size={12} />
                        )}
                      </button>
                    </div>
                  </td>

                  {/* Type */}
                  <td className="py-3.5 px-4">
                    {renderTypeIcon(tx.type)}
                  </td>

                  {/* Asset */}
                  <td className="py-3.5 px-4">
                    {renderAssetBadge(tx.asset)}
                  </td>

                  {/* Amount */}
                  <td className={`py-3.5 px-4 text-right font-bold ${amountColor}`}>
                    {tx.amount}
                  </td>

                  {/* USD Value */}
                  <td className={`py-3.5 px-4 text-right font-semibold ${amountColor}`}>
                    {tx.usdValue}
                  </td>

                  {/* Date / Time (UTC) */}
                  <td className="py-3.5 px-4 text-slate-400">
                    {tx.dateTimeUtc}
                  </td>

                  {/* Status Badge */}
                  <td className="py-3.5 px-4 text-right">
                    <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[10px] font-semibold bg-[#00e676]/10 text-[#00e676] border border-[#00e676]/20 shadow-[0_0_8px_rgba(0,230,118,0.1)]">
                      <span>Completed</span>
                      <CheckCircle2 size={11} className="stroke-[2.5]" />
                    </span>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>

      {/* ── Pagination Footer ── */}
      <div className="p-3 border-t border-[#1b2230] flex flex-wrap items-center justify-between gap-3 text-xs text-slate-400 font-sans bg-[#07090e]/40">
        <div>Showing 1 to {transactions.length} of 124 results</div>

        <div className="flex items-center gap-4">
          {/* Page numbers */}
          <div className="flex items-center gap-1 font-mono text-xs">
            <button
              type="button"
              onClick={() => setCurrentPage(1)}
              className="w-7 h-7 rounded-lg bg-[#07090e] border border-[#1b2230] flex items-center justify-center hover:text-white hover:border-slate-500 transition-colors"
            >
              «
            </button>
            <button
              type="button"
              onClick={() => setCurrentPage(Math.max(1, currentPage - 1))}
              className="w-7 h-7 rounded-lg bg-[#07090e] border border-[#1b2230] flex items-center justify-center hover:text-white hover:border-slate-500 transition-colors"
            >
              ‹
            </button>
            <button
              type="button"
              className="w-7 h-7 rounded-lg bg-[#00e5ff]/15 border border-[#00e5ff]/40 text-[#00e5ff] font-bold flex items-center justify-center"
            >
              1
            </button>
            <button
              type="button"
              className="w-7 h-7 rounded-lg bg-[#07090e] border border-[#1b2230] flex items-center justify-center hover:text-white hover:border-slate-500 transition-colors"
            >
              2
            </button>
            <button
              type="button"
              className="w-7 h-7 rounded-lg bg-[#07090e] border border-[#1b2230] flex items-center justify-center hover:text-white hover:border-slate-500 transition-colors"
            >
              3
            </button>
            <button
              type="button"
              className="w-7 h-7 rounded-lg bg-[#07090e] border border-[#1b2230] flex items-center justify-center hover:text-white hover:border-slate-500 transition-colors"
            >
              4
            </button>
            <span className="px-1 text-slate-600">...</span>
            <button
              type="button"
              className="w-7 h-7 rounded-lg bg-[#07090e] border border-[#1b2230] flex items-center justify-center hover:text-white hover:border-slate-500 transition-colors"
            >
              21
            </button>
            <button
              type="button"
              onClick={() => setCurrentPage(currentPage + 1)}
              className="w-7 h-7 rounded-lg bg-[#07090e] border border-[#1b2230] flex items-center justify-center hover:text-white hover:border-slate-500 transition-colors"
            >
              ›
            </button>
            <button
              type="button"
              onClick={() => setCurrentPage(21)}
              className="w-7 h-7 rounded-lg bg-[#07090e] border border-[#1b2230] flex items-center justify-center hover:text-white hover:border-slate-500 transition-colors"
            >
              »
            </button>
          </div>

          {/* Rows per page */}
          <div className="flex items-center gap-2 text-xs">
            <span>Rows per page:</span>
            <div className="relative">
              <select
                value={rowsPerPage}
                onChange={(e) => setRowsPerPage(e.target.value)}
                className="appearance-none bg-[#07090e] border border-[#1b2230] rounded-lg px-2.5 py-1 pr-6 text-xs text-white font-mono focus:outline-none focus:border-[#00e5ff]/50 cursor-pointer"
              >
                <option value="10">10</option>
                <option value="25">25</option>
                <option value="50">50</option>
                <option value="100">100</option>
              </select>
              <ChevronDown size={12} className="absolute right-2 top-1/2 -translate-y-1/2 text-slate-500 pointer-events-none" />
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
