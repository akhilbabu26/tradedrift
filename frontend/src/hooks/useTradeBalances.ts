import { useState, useEffect, useCallback } from 'react'
import { walletApi, type Balance } from '../api/wallet'
import { wsService, WsChannels } from '../api/ws'
import { useAuthStore } from '../store/authStore'

export function useTradeBalances() {
  const [balances, setBalances] = useState<Balance[]>([])
  const [loading, setLoading] = useState(true)
  const user = useAuthStore((s) => s.user)
  const userId = user?.userId

  const fetchBalances = useCallback(async () => {
    try {
      const data = await walletApi.getAllBalances()
      setBalances(data || [])
    } catch (err) {
      console.warn('Failed to fetch wallet balances', err)
      setBalances([])
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchBalances()

    if (!userId) return

    // Listen to user notifications stream to auto-refresh balances on fills
    const unsub = wsService.subscribe(WsChannels.userNotifications(userId), () => {
      fetchBalances()
    })

    return () => {
      unsub()
    }
  }, [fetchBalances, userId])

  const getBalance = useCallback(
    (asset: string): Balance => {
      const found = balances.find((b) => b.asset.toUpperCase() === asset.toUpperCase())
      return found || { asset, availableBalance: '0.00', reservedBalance: '0.00' }
    },
    [balances]
  )

  return {
    balances,
    loading,
    getBalance,
    refetchBalances: fetchBalances,
  }
}
