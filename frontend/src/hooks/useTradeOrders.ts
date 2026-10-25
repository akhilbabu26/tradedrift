import { useState, useEffect, useCallback, useRef } from 'react'
import { orderApi, type Order, type CreateOrderRequest } from '../api/order'
import { wsService, WsChannels } from '../api/ws'
import { useAuthStore } from '../store/authStore'
import { useWalletStore } from '../store/walletStore'
import toast from 'react-hot-toast'

export function useTradeOrders(marketId: string, onBalanceRefresh?: () => void) {
  const [orders, setOrders] = useState<Order[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [cancellingId, setCancellingId] = useState<string | null>(null)
  const [cancellingAll, setCancellingAll] = useState(false)

  const user = useAuthStore((s) => s.user)
  const userId = user?.userId || (user as any)?.user_id || (user as any)?.id

  const reqIdRef = useRef(0)
  const inFlightRef = useRef(false)
  const pendingRefetchRef = useRef(false)
  const latestAppliedReqIdRef = useRef(0)

  // Fetch all orders for the authenticated user across all markets with coalesced refetching
  const fetchOrders = useCallback(async () => {
    // Guest protection: never call order API without an authenticated session
    if (!useAuthStore.getState().isAuthenticated || !userId) {
      setOrders([])
      setLoading(false)
      setError(null)
      return
    }

    if (inFlightRef.current) {
      pendingRefetchRef.current = true
      return
    }

    inFlightRef.current = true
    const thisReqId = ++reqIdRef.current

    try {
      setError(null)
      // Retrieve up to 100 orders across all markets for the user overview
      const data = await orderApi.listOrders({ limit: 100 })
      // Guard against stale out-of-order REST responses:
      if (thisReqId < latestAppliedReqIdRef.current) {
        return
      }
      latestAppliedReqIdRef.current = thisReqId
      setOrders(data || [])
    } catch (err: any) {
      if (thisReqId >= latestAppliedReqIdRef.current) {
        const msg = err?.response?.data?.message || err?.message || 'Failed to fetch orders'
        console.warn('Failed to fetch orders', err)
        setError(msg)
      }
    } finally {
      inFlightRef.current = false
      setLoading(false)

      if (pendingRefetchRef.current) {
        pendingRefetchRef.current = false
        // Trigger queued refetch to ensure the latest state is captured
        fetchOrders()
      }
    }
  }, [userId])

  useEffect(() => {
    setLoading(true)
    fetchOrders()

    const onRefresh = () => {
      fetchOrders()
      onBalanceRefresh?.()
    }

    const unsubs: Array<() => void> = []

    if (userId) {
      // Subscribe to both user-scoped notifications and settled trade/portfolio events
      const notifCh = WsChannels.userNotifications(userId)
      const portfolioCh = WsChannels.userPortfolio(userId)

      if (notifCh) {
        unsubs.push(wsService.subscribe(notifCh, onRefresh))
      }
      if (portfolioCh) {
        unsubs.push(wsService.subscribe(portfolioCh, onRefresh))
      }
    }

    unsubs.push(wsService.onReconnect(onRefresh))

    window.addEventListener('focus', onRefresh)
    unsubs.push(() => window.removeEventListener('focus', onRefresh))

    return () => {
      unsubs.forEach((u) => u())
    }
  }, [fetchOrders, onBalanceRefresh, userId])

  const submitOrder = async (req: CreateOrderRequest): Promise<boolean> => {
    if (!useAuthStore.getState().isAuthenticated || !userId) {
      toast.error('Authentication required to place orders')
      return false
    }

    setSubmitting(true)
    try {
      const newOrder = await orderApi.createOrder(req)
      const baseAsset = req.market_id?.split('-')[0] || marketId.split('-')[0]
      toast.success(`${req.side} order placed: ${req.quantity} ${baseAsset}`)

      // Advance monotonic request id to prevent older background responses from discarding this order
      latestAppliedReqIdRef.current = ++reqIdRef.current
      setOrders((prev) => [newOrder, ...prev.filter((o) => o.id !== newOrder.id)])

      onBalanceRefresh?.()
      useWalletStore.getState().invalidateBalances().catch(() => {})
      return true
    } catch (err: any) {
      const msg = err?.response?.data?.message || err?.message || 'Failed to submit order'
      toast.error(msg)
      return false
    } finally {
      setSubmitting(false)
    }
  }

  const cancelOrder = async (orderId: string): Promise<boolean> => {
    if (!useAuthStore.getState().isAuthenticated || !userId) {
      toast.error('Authentication required')
      return false
    }

    setCancellingId(orderId)
    try {
      const updated = await orderApi.cancelOrder(orderId)
      toast.success('Order cancelled')

      // Advance monotonic request id so an older listOrders response cannot un-cancel the order
      latestAppliedReqIdRef.current = ++reqIdRef.current
      setOrders((prev) =>
        prev.map((o) => {
          if (o.id !== orderId) return o
          return {
            ...o,
            status: 'CANCELLED',
            ...(updated ? {
              filled_quantity: updated.filled_quantity || o.filled_quantity,
              quantity: updated.quantity || o.quantity,
            } : {}),
          }
        })
      )
      onBalanceRefresh?.()
      useWalletStore.getState().invalidateBalances().catch(() => {})
      return true
    } catch (err: any) {
      const msg = err?.response?.data?.message || err?.message || 'Failed to cancel order'
      toast.error(msg)
      return false
    } finally {
      setCancellingId(null)
    }
  }

  const cancelAll = async (openOrders: Order[]): Promise<void> => {
    if (!useAuthStore.getState().isAuthenticated || !userId || openOrders.length === 0) return
    setCancellingAll(true)

    try {
      const results = await Promise.allSettled(
        openOrders.map((o) => orderApi.cancelOrder(o.id))
      )

      const succeeded = results.filter((r) => r.status === 'fulfilled').length
      const failed = results.filter((r) => r.status === 'rejected').length

      if (failed === 0) {
        toast.success(`Cancelled all ${succeeded} orders`)
      } else if (succeeded === 0) {
        toast.error('Failed to cancel orders — please try again')
      } else {
        toast.error(`${succeeded} cancelled, ${failed} failed`)
      }

      await fetchOrders()
      onBalanceRefresh?.()
      useWalletStore.getState().invalidateBalances().catch(() => {})
    } catch {
      toast.error('Unexpected error while cancelling orders')
    } finally {
      setCancellingAll(false)
    }
  }

  return {
    orders,
    loading,
    error,
    submitting,
    cancellingId,
    cancellingAll,
    fetchOrders,
    submitOrder,
    cancelOrder,
    cancelAll,
  }
}
