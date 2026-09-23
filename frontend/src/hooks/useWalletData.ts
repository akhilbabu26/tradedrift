import { useState, useEffect, useCallback, useMemo } from 'react'
import { walletApi, type Balance } from '../api/wallet'
import { marketApi } from '../api/market'
import { topupApi, type DailyUsage, type TopUpHistoryItem } from '../api/topup'
import {
  MOCK_WALLET_BALANCES,
  MOCK_DAILY_USAGE,
  MOCK_TOPUP_HISTORY,
  DEFAULT_ASSET_PRICES,
} from '../data/walletMock'
import { toDecimal } from '../utils/decimal'
import { getAssetMetadata, type AssetMetadata } from '../utils/marketMetadata'
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

export function useWalletData() {
  const [rawBalances, setRawBalances] = useState<Balance[]>([])
  const [prices, setPrices] = useState<Record<string, string>>(DEFAULT_ASSET_PRICES)
  const [dailyUsage, setDailyUsage] = useState<DailyUsage>(MOCK_DAILY_USAGE)
  const [topUpHistory] = useState<TopUpHistoryItem[]>(MOCK_TOPUP_HISTORY)
  const [loading, setLoading] = useState(true)
  const [isDemoData, setIsDemoData] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [submittingTopUp, setSubmittingTopUp] = useState(false)

  // ── 1. Fetch Balances & Market Prices ──────────────────────────────────────
  const loadData = useCallback(async () => {
    setLoading(true)
    setError(null)

    let loadedBalances: Balance[] = []
    let isMock = false

    try {
      // 1. Try real wallet API
      const res = await walletApi.getAllBalances()
      if (res && res.length > 0) {
        loadedBalances = res
        isMock = false
      } else {
        loadedBalances = MOCK_WALLET_BALANCES
        isMock = true
      }
    } catch (err) {
      console.warn('Wallet API unreachable, falling back to mock balances', err)
      loadedBalances = MOCK_WALLET_BALANCES
      isMock = true
    }

    setRawBalances(loadedBalances)
    setIsDemoData(isMock)

    // 2. Fetch live prices for non-USDT assets
    const updatedPrices: Record<string, string> = { ...DEFAULT_ASSET_PRICES, USDT: '1.00' }
    const cryptoAssets = loadedBalances.filter((b) => b.asset.toUpperCase() !== 'USDT')

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
          // Keep default mock price if ticker fetch fails
        }
      })
    )
    setPrices(updatedPrices)

    // 3. Fetch daily usage from TopUp API
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

  useEffect(() => {
    loadData()
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
        toast.success(
          `Top-up order created: ₹${order.inrAmount} for ${order.usdtAmount} USDT (${order.status}). Note: Simulation mode active.`,
          { duration: 5000 }
        )
      } else {
        toast('Top-up service is currently simulated. No funds were debited.', {
          icon: 'ℹ️',
          duration: 4000,
        })
      }
    } catch (err: any) {
      const msg = err?.response?.data?.message || err?.message || 'Top-up endpoint unavailable'
      toast('Top-up service is offline. No real transaction occurred.', {
        icon: 'ℹ️',
        duration: 4000,
      })
      console.info('TopUp API response:', msg)
    } finally {
      setSubmittingTopUp(false)
    }
  }, [])

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
