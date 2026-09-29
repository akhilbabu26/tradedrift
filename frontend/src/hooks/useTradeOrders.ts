import { useState, useEffect, useCallback } from 'react'
import { orderApi, type Order, type CreateOrderRequest } from '../api/order'
import { wsService, WsChannels } from '../api/ws'
import { useAuthStore } from '../store/authStore'
import toast from 'react-hot-toast'

export function useTradeOrders(marketId: string, onBalanceRefresh?: () => void) {
  const [orders, setOrders] = useState<Order[]>([])
  const [loading, setLoading] = useState(true)
  const [submitting, setSubmitting] = useState(false)
  const [cancellingId, setCancellingId] = useState<string | null>(null)
  const [cancellingAll, setCancellingAll] = useState(false)
  const user = useAuthStore((s) => s.user)
  const userId = user?.userId

  const fetchOrders = useCallback(async () => {
    try {
      // Filter by the currently selected market so the panel only shows
      // orders relevant to what the user is trading right now.
      const data = await orderApi.listOrders({ market_id: marketId })
      setOrders(data || [])
    } catch (err) {
      console.warn('Failed to fetch orders', err)
      setOrders([])
    } finally {
      setLoading(false)
    }
  }, [marketId])

  useEffect(() => {
    fetchOrders()

    if (!userId) return

    const unsub = wsService.subscribe(WsChannels.userNotifications(userId), () => {
      fetchOrders()
      onBalanceRefresh?.()
    })

    return () => {
      unsub()
    }
  }, [fetchOrders, onBalanceRefresh, userId])

  const submitOrder = async (req: CreateOrderRequest): Promise<boolean> => {
    setSubmitting(true)
    try {
      const newOrder = await orderApi.createOrder(req)
      toast.success(`${req.side} order placed: ${req.quantity} ${marketId.split('-')[0]}`)
      setOrders((prev) => [newOrder, ...prev])
      onBalanceRefresh?.()
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
    setCancellingId(orderId)
    try {
      await orderApi.cancelOrder(orderId)
      toast.success('Order cancelled')
      setOrders((prev) =>
        prev.map((o) => (o.id === orderId ? { ...o, status: 'CANCELLED' } : o))
      )
      onBalanceRefresh?.()
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
    if (openOrders.length === 0) return
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
    } catch {
      toast.error('Unexpected error while cancelling orders')
    } finally {
      setCancellingAll(false)
    }
  }

  return {
    orders,
    loading,
    submitting,
    cancellingId,
    cancellingAll,
    fetchOrders,
    submitOrder,
    cancelOrder,
    cancelAll,
  }
}
