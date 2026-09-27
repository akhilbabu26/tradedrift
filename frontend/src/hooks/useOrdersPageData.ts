import { useState, useEffect, useCallback } from 'react'
import { orderApi } from '../api/order'
import { walletApi } from '../api/wallet'
import { tradesApi } from '../api/trades'
import { wsService, WsChannels, type ConnectionStatus } from '../api/ws'
import { useAuthStore } from '../store/authStore'
import {
  MOCK_ORDERS_KPIS,
  MOCK_OPEN_ORDERS,
  MOCK_ORDER_HISTORY,
  MOCK_TRADE_FILLS,
} from '../data/ordersMock'
import type {
  OpenOrderItem,
  OrderHistoryItem,
  TradeFillItem,
  OrdersKPIs,
  OrderStatusUI,
} from '../types/orders'
import { toDecimal } from '../utils/decimal'
import { formatPrice, formatQuantity, formatDate } from '../utils/formatters'
import toast from 'react-hot-toast'

/** Normalizes backend status strings (including ORDER_STATUS_* enums) to UI display labels and badges */
export function normalizeOrderStatus(rawStatus: string): OrderStatusUI {
  const s = (rawStatus || '').toUpperCase()
  if (s.includes('FILL') || s === 'COMPLETED') {
    if (s.includes('PARTIAL')) return 'Partially Filled'
    return 'Filled'
  }
  if (s.includes('CANCEL')) return 'Canceled'
  if (s.includes('EXPIRE')) return 'Expired'
  if (s.includes('REJECT')) return 'Rejected'
  return 'Open'
}

/** Determines if an order is currently active (OPEN or PARTIALLY_FILLED) */
export function isOrderActive(rawStatus: string): boolean {
  const s = (rawStatus || '').toUpperCase()
  if (s === 'ORDER_STATUS_OPEN' || s === 'OPEN' || s === 'PENDING') return true
  if (s === 'ORDER_STATUS_PARTIALLY_FILLED' || s === 'PARTIALLY_FILLED') return true
  return false
}

/** Normalizes order side strings (ORDER_SIDE_BUY/SELL, BUY/SELL) */
export function normalizeOrderSide(rawSide: string): 'BUY' | 'SELL' {
  const s = (rawSide || '').toUpperCase()
  if (s.includes('SELL')) return 'SELL'
  return 'BUY'
}

/** Normalizes order type strings (ORDER_TYPE_LIMIT/MARKET, LIMIT/MARKET) */
export function normalizeOrderType(rawType: string): 'Limit' | 'Market' {
  const s = (rawType || '').toUpperCase()
  if (s.includes('MARKET')) return 'Market'
  return 'Limit'
}

export function useOrdersPageData() {
  const [openOrders, setOpenOrders] = useState<OpenOrderItem[]>([])
  const [orderHistory, setOrderHistory] = useState<OrderHistoryItem[]>([])
  const [tradeFills, setTradeFills] = useState<TradeFillItem[]>([])
  const [kpis, setKpis] = useState<OrdersKPIs>(MOCK_ORDERS_KPIS)
  const [loading, setLoading] = useState(true)
  const [isDemoData, setIsDemoData] = useState(false)
  const [cancellingId, setCancellingId] = useState<string | null>(null)
  const [wsStatus, setWsStatus] = useState<ConnectionStatus>('connecting')

  // ── 1. Fetch Orders, History, and Authoritative KPI Sources ─────────────────
  const loadOrders = useCallback(async () => {
    setLoading(true)

    try {
      // 1. Fetch orders from authoritative Order API (retrieve up to 100 for user overview)
      const rawOrders = await orderApi.listOrders({ limit: 100 })
      setIsDemoData(false)

      // Separate open orders and historical orders
      const liveOpen: OpenOrderItem[] = []
      const liveHistory: OrderHistoryItem[] = []

      let openCount = 0
      let todayExecutionsCount = 0
      let totalTradedVolume = toDecimal(0)

      for (const o of (rawOrders || [])) {
        const rawStatus = o.status || ''
        const baseAsset = o.market_id ? o.market_id.split('-')[0] : 'BTC'
        const pair = o.market_id ? o.market_id.replace('-', '/') : 'BTC/USDT'
        const side = normalizeOrderSide(o.side)
        const type = normalizeOrderType(o.order_type)

        const totalQty = toDecimal(o.quantity || '0')
        const filledQty = toDecimal(o.filled_quantity || '0')
        const remainingQty = totalQty.minus(filledQty).clamp(0, totalQty)
        const priceDec = toDecimal(o.price || '0')

        const progress = totalQty.gt(0)
          ? Math.min(100, Math.round(filledQty.dividedBy(totalQty).times(100).toNumber()))
          : 0

        if (isOrderActive(rawStatus)) {
          openCount++
          liveOpen.push({
            id: o.id,
            time: o.created_at ? formatDate(o.created_at) : 'Just now',
            pair,
            type,
            side,
            price: formatPrice(o.price || '0'),
            amount: `${formatQuantity(o.quantity || '0', 4)} ${baseAsset}`,
            filledRemaining: `${formatQuantity(o.filled_quantity || '0', 4)} / ${formatQuantity(remainingQty.toString(), 4)}`,
            progress,
            status: 'Open',
            rawStatus: o.status,
          })
        } else {
          const statusUI = normalizeOrderStatus(rawStatus)
          if (statusUI === 'Filled') {
            todayExecutionsCount++
            totalTradedVolume = totalTradedVolume.plus(filledQty.times(priceDec))
          }

          const totalVal = filledQty.gt(0) ? filledQty.times(priceDec) : totalQty.times(priceDec)
          const ts = o.created_at ? new Date(o.created_at).getTime() : Date.now()
          liveHistory.push({
            id: o.id,
            orderId: o.id.startsWith('ord_') ? o.id : `ord_${o.id.substring(0, 12)}`,
            time: o.created_at ? formatDate(o.created_at) : 'Recent',
            timestamp: isNaN(ts) ? Date.now() : ts,
            pair,
            type,
            side,
            avgFilledPrice: formatPrice(o.price || '0'),
            executedTotal: `${formatQuantity(o.filled_quantity || '0', 4)} / ${formatQuantity(o.quantity || '0', 4)}`,
            totalValueUSDT: formatPrice(totalVal.toString()),
            status: statusUI,
            rawStatus: o.status,
          })
        }
      }

      setOpenOrders(liveOpen)
      setOrderHistory(liveHistory)

      // ── Fetch real trade fills via GET /api/v1/trades ──────────────────
      try {
        const tradesRes = await tradesApi.listTrades({ limit: 100 })
        const currentUserId = useAuthStore.getState().user?.userId
        const fills: TradeFillItem[] = (tradesRes.trades || []).map((t) => {
          const base = t.base_asset ?? t.market_id?.split('-')[0] ?? 'BTC'
          const qty = toDecimal(t.quantity || '0')
          const price = toDecimal(t.price || '0')
          const total = qty.times(price)
          const fee = total.times('0.001')
          let side: 'BUY' | 'SELL' = 'BUY'
          if (currentUserId && t.buyer_id && t.seller_id) {
            side = t.buyer_id === currentUserId ? 'BUY' : 'SELL'
          } else if (t.taker_side) {
            side = normalizeOrderSide(t.taker_side)
          }
          const timeStr = t.executed_at ?? t.created_at
          const ts = timeStr ? new Date(timeStr).getTime() : Date.now()
          return {
            id: t.id,
            tradeId: t.id.startsWith('trd_') ? t.id : `trd_${t.id.substring(0, 6)}`,
            time: timeStr ? formatDate(timeStr) : 'Recent',
            timestamp: isNaN(ts) ? Date.now() : ts,
            pair: t.market_id?.replace('-', '/') ?? `${base}/USDT`,
            side,
            executionPrice: formatPrice(t.price || '0'),
            filledAmount: `${formatQuantity(t.quantity || '0', 4)} ${base}`,
            totalCostUSDT: formatPrice(total.toString()),
            feeUSDT: fee.toFixed(2),
          }
        })
        setTradeFills(fills)
      } catch {
        setTradeFills([])
      }

      // Calculate authoritative Funds Locked from wallet reserved balance
      let lockedFunds = toDecimal(0)
      try {
        const balances = await walletApi.getAllBalances()
        if (balances && balances.length > 0) {
          for (const b of balances) {
            const res = toDecimal(b.reservedBalance || '0')
            if (res.gt(0)) {
              lockedFunds = lockedFunds.plus(res)
            }
          }
        }
      } catch {
        // Keep default 0 if wallet unavailable
      }

      setKpis({
        activeOrders: openCount,
        todayExecutions: todayExecutionsCount,
        todayExecutionsChange: todayExecutionsCount > 0 ? '+100%' : '+0%',
        tradedVolume24h: totalTradedVolume.toFixed(2),
        tradedVolume24hChange: totalTradedVolume.gt(0) ? '+100%' : '+0%',
        fundsLocked: lockedFunds.toFixed(2),
        fundsLockedOrderCount: openCount,
      })
    } catch (err) {
      console.warn('Orders API unreachable, falling back to deterministic mock dataset', err)
      setIsDemoData(true)
      setOpenOrders(MOCK_OPEN_ORDERS)
      setOrderHistory(MOCK_ORDER_HISTORY)
      setTradeFills(MOCK_TRADE_FILLS)
      setKpis(MOCK_ORDERS_KPIS)
    } finally {
      setLoading(false)
    }
  }, [])

  // ── 2. WebSocket Subscription for Live Order Push Events ───────────────────
  useEffect(() => {
    loadOrders()

    const unsubWs = wsService.onStatus((_connected, status) => setWsStatus(status))
    const userId = useAuthStore.getState().user?.userId
    const unsubOrders = userId
      ? wsService.subscribe(WsChannels.userNotifications(userId), () => {
          loadOrders()
        })
      : () => {}

    return () => {
      unsubWs()
      unsubOrders()
    }
  }, [loadOrders])

  // ── 3. Cancel Order Action ────────────────────────────────────────────────
  const handleCancelOrder = useCallback(
    async (orderId: string): Promise<boolean> => {
      setCancellingId(orderId)
      try {
        if (!isDemoData) {
          await orderApi.cancelOrder(orderId)
        }

        // Optimistically remove from Open Orders & update KPIs
        setOpenOrders((prev) => {
          const target = prev.find((o) => o.id === orderId)
          const remaining = prev.filter((o) => o.id !== orderId)

          if (target) {
            // Add to Order History as Canceled
            const canceledItem: OrderHistoryItem = {
              id: target.id,
              orderId: target.id.startsWith('ord_') ? target.id : `ord_${target.id.substring(0, 12)}`,
              time: 'Just now',
              timestamp: Date.now(),
              pair: target.pair,
              type: target.type,
              side: target.side,
              avgFilledPrice: target.price,
              executedTotal: target.filledRemaining,
              totalValueUSDT: '0.00',
              status: 'Canceled',
              rawStatus: 'CANCELLED',
            }
            setOrderHistory((h) => [canceledItem, ...h])
          }

          setKpis((k) => ({
            ...k,
            activeOrders: Math.max(0, remaining.length),
            fundsLockedOrderCount: Math.max(0, remaining.length),
          }))

          return remaining
        })

        toast.success('Order cancelled successfully')
        return true
      } catch (err: any) {
        const msg = err?.response?.data?.message || err?.message || 'Failed to cancel order'
        toast.error(msg)
        return false
      } finally {
        setCancellingId(null)
      }
    },
    [isDemoData]
  )

  return {
    openOrders,
    orderHistory,
    tradeFills,
    kpis,
    loading,
    isDemoData,
    cancellingId,
    wsStatus,
    handleCancelOrder,
    refetch: loadOrders,
  }
}
