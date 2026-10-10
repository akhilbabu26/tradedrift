import { useEffect } from 'react'
import { useWalletStore } from '../store/walletStore'
import type { Balance } from '../api/wallet'

/**
 * useTradeBalances
 *
 * Provides reactive wallet balance access to TradePage components.
 * Backed by centralized useWalletStore so balances synchronize across all pages.
 */
export function useTradeBalances() {
  const balances = useWalletStore((s) => s.balances)
  const loading = useWalletStore((s) => s.loading)
  const getBalance = useWalletStore((s) => s.getBalance)
  const refetchBalances = useWalletStore((s) => s.loadBalances)
  const subscribeToBalanceUpdates = useWalletStore((s) => s.subscribeToBalanceUpdates)

  useEffect(() => {
    if (balances.length === 0) {
      refetchBalances()
    }
    const unsub = subscribeToBalanceUpdates()
    return () => unsub()
  }, [refetchBalances, subscribeToBalanceUpdates, balances.length])

  return {
    balances,
    loading,
    getBalance,
    refetchBalances,
  }
}
export type { Balance }

