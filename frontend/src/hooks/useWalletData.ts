import { useState, useEffect, useCallback, useMemo } from 'react'
import { walletApi, type Balance } from '../api/wallet'
import { marketApi } from '../api/market'
import { topupApi, type DailyUsage, type TopUpHistoryItem } from '../api/topup'
import { useWalletStore } from '../store/walletStore'
import { wsService } from '../api/ws'
import {
  MOCK_DAILY_USAGE,
  DEFAULT_ASSET_PRICES,
} from '../data/walletMock'
import { toDecimal } from '../utils/decimal'
import { getAssetMetadata, type AssetMetadata } from '../utils/marketMetadata'
import { extractApiError } from '../utils/apiError'
import toast from 'react-hot-toast'

export interface EnrichedAssetRow {
  asset: string
  name: string
  meta: AssetMetadata
  availableBalance: string
  reservedBalance: string
  totalBalance: string
  price: string
  valueUSDT: string
  isZeroBalance: boolean
}

export interface WalletSummaryMetrics {
  totalWalletValue: string
  availableValue: string
  lockedValue: string
  availablePct: number
  lockedPct: number
}

const TOPUP_STORAGE_KEY = 'tradedrift_user_topup_history'

function loadSavedTopUps(): TopUpHistoryItem[] {
  try {
    const raw = localStorage.getItem(TOPUP_STORAGE_KEY)
    if (raw) return JSON.parse(raw)
  } catch { }
  return []
}

export function useWalletData() {
  const [rawBalances, setRawBalances] = useState<Balance[]>([])
  const [prices, setPrices] = useState<Record<string, string>>(DEFAULT_ASSET_PRICES)
  const [dailyUsage, setDailyUsage] = useState<DailyUsage>(MOCK_DAILY_USAGE)
  const [topUpHistory, setTopUpHistory] = useState<TopUpHistoryItem[]>(() => loadSavedTopUps())
  const [loading, setLoading] = useState(true)
  const [isDemoData, setIsDemoData] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [submittingTopUp, setSubmittingTopUp] = useState(false)

  const storeBalances = useWalletStore((s) => s.balances)

  // ── 1. Fetch Balances, Top-Ups & Market Prices ─────────────────────────────
  const loadData = useCallback(async () => {
    setLoading(true)
    setError(null)

    let loadedBalances: Balance[] = []

    try {
      const res = await walletApi.getAllBalances()
      if (res && res.length > 0) {
        loadedBalances = res
      } else {
        // Authentic zero balances for supported assets
        loadedBalances = [
          { asset: 'BTC', availableBalance: '0.00000000', reservedBalance: '0.00000000' },
          { asset: 'ETH', availableBalance: '0.00000000', reservedBalance: '0.00000000' },
          { asset: 'SOL', availableBalance: '0.00000000', reservedBalance: '0.00000000' },
          { asset: 'USDT', availableBalance: '0.00', reservedBalance: '0.00' },
        ]
      }
      setRawBalances(loadedBalances)
      setIsDemoData(false)
    } catch (err) {
      const msg = extractApiError(err)
      console.warn('Wallet API unreachable:', msg)
      setError(msg)
      setRawBalances([])
      setIsDemoData(false)
    }

    // 2. Fetch live prices for non-USDT assets
    const updatedPrices: Record<string, string> = { ...DEFAULT_ASSET_PRICES, USDT: '1.00' }
    const cryptoAssets = (loadedBalances.length > 0 ? loadedBalances : [
      { asset: 'BTC' }, { asset: 'ETH' }, { asset: 'SOL' }
    ]).filter((b) => b.asset?.toUpperCase() !== 'USDT')

    await Promise.allSettled(
      cryptoAssets.map(async (b) => {
        const symbol = b.asset.toUpperCase()
        const marketId = `${symbol}-USDT`
        try {
          const ticker = await marketApi.getTicker(marketId)
          if (ticker?.last_price && parseFloat(ticker.last_price) > 0) {
            updatedPrices[symbol] = ticker.last_price
          }
        } catch {
          // Keep default fallback price if ticker fetch fails
        }
      })
    )
    setPrices(updatedPrices)

    // 3. Fetch authentic top-ups from backend GET /api/v1/topups
    try {
      const serverOrders = await topupApi.listTopUps()
      if (serverOrders && serverOrders.length > 0) {
        const history: TopUpHistoryItem[] = serverOrders.map((o) => {
          let status: TopUpHistoryItem['status'] = 'Pending'
          if (o.status === 'COMPLETED') status = 'Completed'
          else if (o.status === 'PAYMENT_CONFIRMED' || o.status === 'CREDIT_PENDING') status = 'Credited'
          else if (o.status === 'FAILED' || o.status === 'REFUND_REQUIRED') status = 'Failed'

          return {
            id: o.topupId,
            orderId: o.topupId.startsWith('topup_') ? o.topupId : `topup_${o.topupId.substring(0, 8)}`,
            amountInr: o.inrAmount,
            usdtCredited: o.usdtAmount,
            paymentMode: (o.provider || 'UPI').toUpperCase(),
            status,
            date: o.createdAt ? new Date(o.createdAt).toLocaleDateString() : 'Recent',
          }
        })
        setTopUpHistory(history)
      }
    } catch {
      // Local fallback if top-up service endpoint unconfigured
    }

    // 4. Fetch daily usage from TopUp API
    try {
      const usage = await topupApi.getDailyUsage()
      if (usage) {
        setDailyUsage(usage)
      }
    } catch {
      setDailyUsage(MOCK_DAILY_USAGE)
    }

    setLoading(false)
  }, [])

  // Sync with global walletStore
  useEffect(() => {
    if (storeBalances.length > 0) {
      setRawBalances(storeBalances)
    }
  }, [storeBalances])

  // Lifecycle listeners: auto-refresh on mount, WS balance push, reconnect, and tab focus
  useEffect(() => {
    loadData()

    const unsubBalances = useWalletStore.getState().subscribeToBalanceUpdates()
    const unsubReconnect = wsService.onReconnect(loadData)

    const onFocus = () => {
      loadData()
    }
    window.addEventListener('focus', onFocus)

    return () => {
      unsubBalances()
      unsubReconnect()
      window.removeEventListener('focus', onFocus)
    }
  }, [loadData])

  // ── 2. Enriched Asset Rows ─────────────────────────────────────────────────
  const enrichedAssets = useMemo<EnrichedAssetRow[]>(() => {
    return rawBalances.map((b) => {
      const asset = b.asset.toUpperCase()
      const meta = getAssetMetadata(asset)
      const available = toDecimal(b.availableBalance || '0')
      const reserved = toDecimal(b.reservedBalance || '0')
      const total = available.plus(reserved)
      const priceStr = prices[asset] || (asset === 'USDT' ? '1.00' : '0.00')
      const price = toDecimal(priceStr)
      const value = total.times(price)

      const isZero = total.lte(0)

      return {
        asset,
        name: meta.name,
        meta,
        availableBalance: available.toFixed(asset === 'USDT' ? 2 : 4),
        reservedBalance: reserved.toFixed(asset === 'USDT' ? 2 : 4),
        totalBalance: total.toFixed(asset === 'USDT' ? 2 : 4),
        price: priceStr,
        valueUSDT: value.toFixed(2),
        isZeroBalance: isZero,
      }
    })
  }, [rawBalances, prices])

  // ── 3. Summary & Financial Valuation (Safe Decimal.js) ─────────────────────
  const summary = useMemo<WalletSummaryMetrics>(() => {
    let totalVal = toDecimal(0)
    let availVal = toDecimal(0)
    let lockVal = toDecimal(0)

    for (const b of rawBalances) {
      const asset = b.asset.toUpperCase()
      const priceStr = prices[asset] || (asset === 'USDT' ? '1.00' : '0.00')
      const price = toDecimal(priceStr)

      const avail = toDecimal(b.availableBalance || '0')
      const res = toDecimal(b.reservedBalance || '0')

      const aVal = avail.times(price)
      const rVal = res.times(price)

      availVal = availVal.plus(aVal)
      lockVal = lockVal.plus(rVal)
      totalVal = totalVal.plus(aVal).plus(rVal)
    }

    let availablePct = 100
    let lockedPct = 0

    if (totalVal.gt(0)) {
      availablePct = Math.round(availVal.dividedBy(totalVal).times(100).toNumber())
      lockedPct = 100 - availablePct
    }

    return {
      totalWalletValue: totalVal.toFixed(2),
      availableValue: availVal.toFixed(2),
      lockedValue: lockVal.toFixed(2),
      availablePct,
      lockedPct,
    }
  }, [rawBalances, prices])

  // ── 4. Top-Up Order Action ────────────────────────────────────────────────
  const initiateTopUp = useCallback(async (inrAmount: number) => {
    if (inrAmount < 1 || inrAmount > 10) {
      toast.error('Amount must be between ₹1 and ₹10')
      return
    }

    setSubmittingTopUp(true)
    const idempotencyKey = typeof crypto !== 'undefined' && crypto.randomUUID
      ? crypto.randomUUID()
      : `topup-${Date.now()}-${Math.random().toString(36).substring(2, 9)}`

    try {
      const order = await topupApi.createTopUp({ inrAmount }, idempotencyKey)
      if (order && order.topupId) {
        const newTx: TopUpHistoryItem = {
          id: order.topupId,
          orderId: order.topupId,
          amountInr: order.inrAmount,
          usdtCredited: `+${order.usdtAmount || '0.00'} USDT`,
          status: 'Completed',
          paymentMode: 'UPI / Simulator',
          date: `${new Date().toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' })} ${new Date().toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit' })}`,
        }
        setTopUpHistory((prev) => {
          const updated = [newTx, ...prev]
          try {
            localStorage.setItem(TOPUP_STORAGE_KEY, JSON.stringify(updated))
          } catch { }
          return updated
        })

        // Refresh daily usage from API to reflect updated reserved/consumed quota
        try {
          const freshUsage = await topupApi.getDailyUsage()
          if (freshUsage) {
            setDailyUsage(freshUsage)
          }
        } catch { }

        // If Razorpay provider, open Razorpay Checkout modal
        if (order.provider === 'RAZORPAY' && order.providerOrderId && typeof window !== 'undefined' && (window as any).Razorpay) {
          const userObj = (() => {
            try {
              return JSON.parse(localStorage.getItem('user') || '{}')
            } catch {
              return {}
            }
          })()

          const rzpKey = import.meta.env.VITE_RAZORPAY_KEY_ID || ''
          const rzpOptions = {
            key: rzpKey,
            amount: order.inrAmount * 100,
            currency: 'INR',
            name: 'TradeDrift',
            description: `Top up ${order.usdtAmount} Virtual USDT`,
            order_id: order.providerOrderId,
            prefill: {
              name: userObj.username || '',
              email: userObj.email || '',
            },
            theme: {
              color: '#10b981',
            },
            handler: function () {
              toast.success('Payment completed! Crediting USDT to wallet...')
              // Automatically poll to reflect backend webhook receipt and wallet reconciliation
              const pollDelays = [1200, 2500, 4500]
              pollDelays.forEach((delay) => {
                setTimeout(async () => {
                  try {
                    await loadData()
                    await useWalletStore.getState().invalidateBalances()
                  } catch (e) {
                    console.warn('Auto-refresh wallet failed:', e)
                  }
                }, delay)
              })
            },
            modal: {
              ondismiss: function () {
                toast('Checkout modal closed', { icon: 'ℹ️' })
              },
            },
          }

          const rzp = new (window as any).Razorpay(rzpOptions)
          rzp.open()
        } else {
          toast.success(
            `Top-up order created: ₹${order.inrAmount} for ${order.usdtAmount} USDT (${order.status}).`,
            { duration: 5000 }
          )
        }
      } else {
        toast('Top-up service is currently simulated. No funds were debited.', {
          icon: 'ℹ️',
          duration: 4000,
        })
      }
    } catch (err: any) {
      const responseData = err?.response?.data
      const status = err?.response?.status

      if (status === 429 || responseData?.code === 'DAILY_LIMIT_EXCEEDED') {
        const errorMsg = responseData?.error || 'Daily top-up limit exceeded. You cannot top up more than ₹10 (10,000 USDT) per day.'
        toast.error(errorMsg, { duration: 5000 })
        // Refresh daily usage to immediately reflect remaining quota in UI
        try {
          const fresh = await topupApi.getDailyUsage()
          if (fresh) setDailyUsage(fresh)
        } catch {}
      } else if (status === 409 || responseData?.code === 'IDEMPOTENCY_KEY_REUSED') {
        toast.error('Top-up request already in progress. Please try again.', { duration: 4000 })
      } else if (status === 400) {
        toast.error(responseData?.error || 'Invalid top-up amount.', { duration: 4000 })
      } else if (responseData?.error || responseData?.message) {
        toast.error(responseData.error || responseData.message, { duration: 4000 })
      } else if (err?.code === 'ERR_NETWORK' || !err?.response) {
        toast.error('Top-up service is offline or unreachable.', { duration: 4000 })
      } else {
        toast.error('Failed to create top-up order. Please try again.', { duration: 4000 })
      }
      console.warn('TopUp API error:', err)
    } finally {
      setSubmittingTopUp(false)
    }
  }, [loadData])

  return {
    rawBalances,
    enrichedAssets,
    summary,
    dailyUsage,
    topUpHistory,
    loading,
    isDemoData,
    error,
    submittingTopUp,
    initiateTopUp,
    refetch: loadData,
  }
}
