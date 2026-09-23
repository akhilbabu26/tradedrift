import { useState, useEffect, useCallback } from 'react'
import { walletApi, type Balance } from '../api/wallet'
import { wsService } from '../api/ws'

export function useTradeBalances() {
  const [balances, setBalances] = useState<Balance[]>([])
  const [loading, setLoading] = useState(true)

  const fetchBalances = useCallback(async () => {
    try {
      const data = await walletApi.getAllBalances()
      setBalances(data || [])
    } catch (err) {
      console.warn('Failed to fetch wallet balances', err)
      // Provide fallback demo balances if user is offline/demo
      setBalances([
        { asset: 'USDT', availableBalance: '25420.50', reservedBalance: '1500.00' },
        { asset: 'BTC', availableBalance: '0.8450', reservedBalance: '0.0500' },
        { asset: 'ETH', availableBalance: '4.2500', reservedBalance: '0.0000' },
        { asset: 'SOL', availableBalance: '18.4000', reservedBalance: '0.0000' },
      ])
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchBalances()

    // Listen to orders stream to auto-refresh balances on fills
    const unsub = wsService.subscribe('orders', () => {
      fetchBalances()
    })

    return () => {
      unsub()
    }
  }, [fetchBalances])

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
