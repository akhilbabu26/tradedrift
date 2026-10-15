import { useState, useEffect, useCallback, useRef } from 'react'
import { Trash2 } from 'lucide-react'
import type { Order, Balance } from '../../types/trade'
import { isOrderActive } from '../../api/order'
import { tradesApi, type BackendTrade } from '../../api/trades'
import OrdersTable from './OrdersTable'
import BalancesTab from './BalancesTab'
import FillsTab from './FillsTab'
import { wsService, WsChannels } from '../../api/ws'
import { useAuthStore } from '../../store/authStore'
import { toDecimal } from '../../utils/decimal'

interface OpenOrdersPanelProps {
  orders: Order[]
  loading: boolean
  error?: string | null
  selectedMarketId?: string
  cancellingId: string | null
  cancellingAll: boolean
  balances: Balance[]
  onCancel: (id: string) => void
  onCancelAll: (openOrders: Order[]) => void
  onRetry?: () => void
}

type PanelTab = 'open' | 'history' | 'fills' | 'balances'

const TABS: { key: PanelTab; label: string }[] = [
  { key: 'open',     label: 'Open Orders'   },
  { key: 'history',  label: 'Order History' },
  { key: 'fills',    label: 'Trade Fills'   },
  { key: 'balances', label: 'Balances'      },
]

/** Empty/placeholder state for Order History */
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
  error,
  selectedMarketId,
  cancellingId,
  cancellingAll,
  balances,
  onCancel,
  onCancelAll,
  onRetry,
}: OpenOrdersPanelProps) {
  const [activeTab, setActiveTab] = useState<PanelTab>('open')
  const [hideOtherPairs, setHideOtherPairs] = useState(false)
  const [trades, setTrades] = useState<BackendTrade[]>([])
  const [tradesLoading, setTradesLoading] = useState(false)

  // Transient completed orders kept visible briefly for smooth completion UX
  interface CompletedEntry {
    order: Order
    isExiting: boolean
  }
  const [completedOrders, setCompletedOrders] = useState<Map<string, CompletedEntry>>(new Map())
  const prevOrdersRef = useRef<Map<string, Order>>(new Map())
  const timerRefs = useRef<Map<string, { exitTimer?: ReturnType<typeof setTimeout>; removeTimer?: ReturnType<typeof setTimeout> }>>(new Map())

  // Protect against slow REST/reconnect responses resurrecting confirmed filled orders in Open Orders
  const terminalFilledIdsRef = useRef<Set<string>>(new Set())

  // CANCELLING is a transient in-flight state: the cancel request has been
  // sent to the matching engine but hasn't settled yet. Keep it in Open Orders
  // (not Order History) so the user can see it is still being processed.
  const isOrderOpen = (order: Order) => {
    // If order was already confirmed FILLED in this session, never resurrect it in Open Orders
    if (terminalFilledIdsRef.current.has(order.id)) return false
    if (!isOrderActive(order)) return false

    // If remaining quantity is explicitly 0 or less, the order is filled
    try {
      if (order.quantity && order.filled_quantity) {
        const remaining = toDecimal(order.quantity).minus(toDecimal(order.filled_quantity || '0'))
        if (remaining.lte(0) && (order.status || '').toUpperCase() !== 'CANCELLING') {
          return false
        }
      }
    } catch {
      // fallback to status check
    }

    return true
  }

  // Smooth completion UX: detect orders that just transitioned to FILLED
  // and start the quick 150-250ms exit transition promptly without artificial lag
  useEffect(() => {
    const prevMap = prevOrdersRef.current
    const newMap = new Map<string, Order>()
    orders.forEach((o) => newMap.set(o.id, o))

    orders.forEach((currentOrder) => {
      const prevOrder = prevMap.get(currentOrder.id)
      const s = (currentOrder.status || '').toUpperCase()
      const isNowFilled =
        s === 'FILLED' ||
        (() => {
          try {
            return toDecimal(currentOrder.quantity).minus(toDecimal(currentOrder.filled_quantity || '0')).lte(0)
          } catch {
            return false
          }
        })()

      // Order transitioned from open/partially_filled to filled
      if (prevOrder && isOrderOpen(prevOrder) && isNowFilled) {
        // Prevent any slow network race or reconnect from resurrecting this order
        terminalFilledIdsRef.current.add(currentOrder.id)

        const presentationOrder: Order = {
          ...currentOrder,
          status: 'FILLED',
          filled_quantity: currentOrder.quantity,
        }

        setCompletedOrders((prev) => {
          const next = new Map(prev)
          next.set(currentOrder.id, {
            order: presentationOrder,
            isExiting: false,
          })
          return next
        })

        // Clean up any existing timer for this order
        const existing = timerRefs.current.get(currentOrder.id)
        if (existing) {
          clearTimeout(existing.exitTimer)
          clearTimeout(existing.removeTimer)
        }

        // Fast non-blocking completion UX:
        // 50ms hold to register the final 100% state, then immediate 200ms exit fade & collapse
        const HOLD_MS = 50
        const EXIT_MS = 200

        const exitTimer = setTimeout(() => {
          setCompletedOrders((prev) => {
            const entry = prev.get(currentOrder.id)
            if (!entry) return prev
            const next = new Map(prev)
            next.set(currentOrder.id, { ...entry, isExiting: true })
            return next
          })
        }, HOLD_MS)

        // Cleanly unmount from Open Orders after the short transition completes
        const removeTimer = setTimeout(() => {
          setCompletedOrders((prev) => {
            const next = new Map(prev)
            next.delete(currentOrder.id)
            return next
          })
          timerRefs.current.delete(currentOrder.id)
        }, HOLD_MS + EXIT_MS)

        timerRefs.current.set(currentOrder.id, { exitTimer, removeTimer })
      }
    })

    prevOrdersRef.current = newMap
  }, [orders])

  // Clear timers on unmount
  useEffect(() => {
    const activeTimers = timerRefs.current
    return () => {
      activeTimers.forEach(({ exitTimer, removeTimer }) => {
        clearTimeout(exitTimer)
        clearTimeout(removeTimer)
      })
      activeTimers.clear()
    }
  }, [])

  const trulyOpenOrders = orders.filter(isOrderOpen)
  const activeCompletedList = Array.from(completedOrders.values())
    .filter(({ order }) => !trulyOpenOrders.some((o) => o.id === order.id))
    .map(({ order }) => order)

  const allOpenOrders = [...trulyOpenOrders, ...activeCompletedList]
  const allHistoryOrders = orders.filter((o) => !isOrderOpen(o))

  // Filter by selectedMarketId only if "Hide other pairs" is checked
  const openOrders = hideOtherPairs && selectedMarketId
    ? allOpenOrders.filter((o) => o.market_id === selectedMarketId)
    : allOpenOrders

  const historyOrders = hideOtherPairs && selectedMarketId
    ? allHistoryOrders.filter((o) => o.market_id === selectedMarketId)
    : allHistoryOrders

  const displayedTrades = hideOtherPairs && selectedMarketId
    ? trades.filter((t) => t.market_id === selectedMarketId)
    : trades

  // Active open orders count (excluding transient exiting orders)
  const openCount = openOrders.filter(isOrderOpen).length

  // Sets for passing down to OrdersTable
  const completedOrderIds = new Set(Array.from(completedOrders.keys()))
  const exitingOrderIds = new Set(
    Array.from(completedOrders.entries())
      .filter(([_, entry]) => entry.isExiting)
      .map(([id]) => id)
  )

  const loadTrades = useCallback(async () => {
    setTradesLoading(true)
    try {
      const res = await tradesApi.listTrades()
      setTrades(res.trades || [])
    } catch {
      setTrades([])
    } finally {
      setTradesLoading(false)
    }
  }, [])

  // Load trades when switching to the Fills tab
  useEffect(() => {
    if (activeTab === 'fills') {
      loadTrades()
    }
  }, [activeTab, loadTrades])

  // Auto-refresh Fills whenever a WS user-notification or portfolio settlement fires while the tab is
  // open. This ensures a newly executed fill appears in real time without the
  // user needing to switch tabs back and forth.
  const user = useAuthStore((s) => s.user)
  const userId = user?.userId || (user as any)?.user_id || (user as any)?.id
  useEffect(() => {
    if (!userId) return
    const onTradeRefresh = () => {
      if (activeTab === 'fills') loadTrades()
    }

    const unsubs: Array<() => void> = []
    const notifCh = WsChannels.userNotifications(userId)
    const portfolioCh = WsChannels.userPortfolio(userId)

    if (notifCh) unsubs.push(wsService.subscribe(notifCh, onTradeRefresh))
    if (portfolioCh) unsubs.push(wsService.subscribe(portfolioCh, onTradeRefresh))
    unsubs.push(wsService.onReconnect(onTradeRefresh))
    window.addEventListener('focus', onTradeRefresh)

    return () => {
      unsubs.forEach((u) => u())
      window.removeEventListener('focus', onTradeRefresh)
    }
  }, [userId, activeTab, loadTrades])

  return (
    <div className="flex-shrink-0 border-t border-t-[rgba(255,255,255,0.09)] border-[#1e2530] bg-gradient-to-b from-[#181a1f] to-[#111318] shadow-[inset_0_1px_0_rgba(255,255,255,0.06)]">
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

        {/* Right side controls: Hide other pairs + Cancel All */}
        <div className="flex items-center gap-4">
          {/* Hide other pairs toggle — shown on Open Orders, Order History, and Fills tabs */}
          {activeTab !== 'balances' && (
            <label className="flex items-center gap-1.5 cursor-pointer select-none text-[11px] text-slate-400 hover:text-slate-200 transition-colors">
              <input
                type="checkbox"
                checked={hideOtherPairs}
                onChange={(e) => setHideOtherPairs(e.target.checked)}
                className="w-3.5 h-3.5 rounded border-[#1e2530] bg-[#0a0b0e] text-[#10b981] focus:ring-0 focus:ring-offset-0 cursor-pointer accent-[#10b981]"
                aria-label="Hide other pairs"
              />
              <span>Hide other pairs</span>
            </label>
          )}

          {/* Cancel All — only shown on Open Orders tab */}
          {activeTab === 'open' && openCount > 0 && (
            <button
              type="button"
              onClick={() => onCancelAll(openOrders)}
              disabled={cancellingAll}
              className="flex items-center gap-1.5 px-3 py-1.5 rounded border border-[#ef4444]/40 text-[#ef4444] text-[11px] font-semibold hover:bg-[#ef4444]/8 hover:border-[#ef4444]/60 transition-colors disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer"
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
      </div>

      {/* Panel content */}
      <div>
        {loading ? (
          <div className="flex items-center justify-center py-8">
            <div className="w-4 h-4 border-2 border-[#10b981] border-t-transparent rounded-full animate-spin" />
          </div>
        ) : error ? (
          <div className="flex flex-col items-center justify-center py-8 text-center px-4">
            <p className="text-xs text-red-400 font-medium">Failed to load orders: {error}</p>
            {onRetry && (
              <button
                type="button"
                onClick={onRetry}
                className="mt-2 px-3 py-1 bg-red-500/10 hover:bg-red-500/20 text-red-300 text-[11px] rounded transition-colors cursor-pointer"
              >
                Retry
              </button>
            )}
          </div>
        ) : (
          <>
            {activeTab === 'open' && (
              <OrdersTable
                orders={openOrders}
                cancellingId={cancellingId}
                onCancel={onCancel}
                completedOrderIds={completedOrderIds}
                exitingOrderIds={exitingOrderIds}
              />
            )}
            {activeTab === 'history' && (
              historyOrders.length > 0
                ? <OrdersTable orders={historyOrders} cancellingId={null} onCancel={() => {}} />
                : <PlaceholderTab label="No order history yet" />
            )}
            {activeTab === 'fills' && (
              <FillsTab trades={displayedTrades} loading={tradesLoading} />
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
