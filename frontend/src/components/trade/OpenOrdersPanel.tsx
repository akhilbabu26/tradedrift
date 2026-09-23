import { useState } from 'react'
import { Trash2 } from 'lucide-react'
import type { Order, Balance } from '../../types/trade'
import { formatPrice, formatQuantity } from '../../utils/formatters'
import OrdersTable from './OrdersTable'

interface OpenOrdersPanelProps {
  orders: Order[]
  loading: boolean
  cancellingId: string | null
  cancellingAll: boolean
  balances: Balance[]
  onCancel: (id: string) => void
  onCancelAll: (openOrders: Order[]) => void
}

type PanelTab = 'open' | 'history' | 'fills' | 'balances'

const TABS: { key: PanelTab; label: string }[] = [
  { key: 'open',     label: 'Open Orders'   },
  { key: 'history',  label: 'Order History' },
  { key: 'fills',    label: 'Trade Fills'   },
  { key: 'balances', label: 'Balances'      },
]

/** Balance table for the Balances tab */
function BalancesTab({ balances }: { balances: Balance[] }) {
  if (balances.length === 0) {
    return (
      <div className="flex items-center justify-center py-8">
        <p className="text-xs text-slate-500">No balances found</p>
      </div>
    )
  }
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-xs">
        <thead>
          <tr className="border-b border-[#1e2530]">
            {['Asset', 'Available', 'Reserved', 'Total'].map((col) => (
              <th key={col} className="px-3 py-2 text-left text-[10px] font-medium text-slate-600 uppercase tracking-wider">
                {col}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="divide-y divide-[#1e2530]/50">
          {balances.map((b) => {
            const avail    = parseFloat(b.availableBalance || '0')
            const reserved = parseFloat(b.reservedBalance  || '0')
            const total    = avail + reserved
            const needsDec = b.asset === 'BTC' || b.asset === 'ETH' || b.asset === 'SOL'
            const fmt = (v: number) => needsDec ? formatQuantity(v.toString(), 6) : formatPrice(v.toString())
            return (
              <tr key={b.asset} className="hover:bg-white/[0.015] transition-colors">
                <td className="px-3 py-2 font-semibold text-[#f5f7fa]">{b.asset}</td>
                <td className="px-3 py-2 font-mono text-[#10b981]">{fmt(avail)}</td>
                <td className="px-3 py-2 font-mono text-slate-400">{fmt(reserved)}</td>
                <td className="px-3 py-2 font-mono text-slate-200">{fmt(total)}</td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}

/** Empty/placeholder state for Order History and Trade Fills */
function PlaceholderTab({ label }: { label: string }) {
  return (
    <div className="flex flex-col items-center justify-center py-8 text-center">
      <p className="text-xs text-slate-500">{label}</p>
      <p className="text-[10px] text-slate-600 mt-0.5">No data available yet</p>
    </div>
  )
}

export default function OpenOrdersPanel({
  orders,
  loading,
  cancellingId,
  cancellingAll,
  balances,
  onCancel,
  onCancelAll,
}: OpenOrdersPanelProps) {
  const [activeTab, setActiveTab] = useState<PanelTab>('open')

  const openOrders = orders.filter(
    (o) => o.status === 'OPEN' || o.status === 'PARTIALLY_FILLED'
  )
  const historyOrders = orders.filter(
    (o) => o.status !== 'OPEN' && o.status !== 'PARTIALLY_FILLED'
  )
  const openCount = openOrders.length

  return (
    /*
     * flex-shrink-0: prevents the panel from growing/shrinking — stays compact
     * Fixed total height of ~220px: 36px tab bar + 184px content
     */
    <div className="flex-shrink-0 border-t border-[#1e2530] bg-[#111318]" style={{ height: 220 }}>
      {/* Tab bar */}
      <div className="flex items-center justify-between border-b border-[#1e2530] px-3">
        <div className="flex items-center gap-0" role="tablist" aria-label="Order panel tabs">
          {TABS.map(({ key, label }) => {
            const isActive = activeTab === key
            const count    = key === 'open' ? openCount : 0
            return (
              <button
                key={key}
                role="tab"
                aria-selected={isActive}
                onClick={() => setActiveTab(key)}
                className={`flex items-center gap-1.5 px-3 py-2.5 text-xs font-medium border-b-2 transition-colors ${
                  isActive
                    ? 'text-[#f5f7fa] border-[#10b981]'
                    : 'text-slate-500 border-transparent hover:text-[#f5f7fa]'
                }`}
              >
                {label}
                {count > 0 && (
                  <span className="px-1.5 py-0.5 rounded-full bg-[#10b981]/15 text-[#10b981] text-[10px] font-bold">
                    {count}
                  </span>
                )}
              </button>
            )
          })}
        </div>

        {/* Cancel All — only shown on Open Orders tab */}
        {activeTab === 'open' && openCount > 0 && (
          <button
            type="button"
            onClick={() => onCancelAll(openOrders)}
            disabled={cancellingAll}
            className="flex items-center gap-1.5 px-3 py-1.5 rounded border border-[#ef4444]/40 text-[#ef4444] text-[11px] font-semibold hover:bg-[#ef4444]/8 hover:border-[#ef4444]/60 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
            aria-label="Cancel all open orders"
          >
            {cancellingAll ? (
              <>
                <span className="w-3 h-3 border border-[#ef4444] border-t-transparent rounded-full animate-spin" />
                Cancelling...
              </>
            ) : (
              <>
                <Trash2 size={12} />
                Cancel All
              </>
            )}
          </button>
        )}
      </div>

      {/* Panel content — scrolls internally */}
      <div className="overflow-y-auto" style={{ height: 'calc(220px - 36px)' }}>
        {loading ? (
          <div className="flex items-center justify-center py-8">
            <div className="w-4 h-4 border-2 border-[#10b981] border-t-transparent rounded-full animate-spin" />
          </div>
        ) : (
          <>
            {activeTab === 'open' && (
              <OrdersTable
                orders={openOrders}
                cancellingId={cancellingId}
                onCancel={onCancel}
              />
            )}
            {activeTab === 'history' && (
              historyOrders.length > 0
                ? <OrdersTable orders={historyOrders} cancellingId={null} onCancel={() => {}} />
                : <PlaceholderTab label="No order history yet" />
            )}
            {activeTab === 'fills' && (
              <PlaceholderTab label="Trade fills not yet available" />
            )}
            {activeTab === 'balances' && (
              <BalancesTab balances={balances} />
            )}
          </>
        )}
      </div>
    </div>
  )
}
