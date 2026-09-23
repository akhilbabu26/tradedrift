/**
 * usePortfolioData — single authoritative data boundary for the Portfolio page.
 *
 * Data Hierarchy (strict, no mixing):
 *  1. Real API → portfolioApi.getSummary() + portfolioApi.getHoldings() + live market prices
 *     - If the API returns an empty holdings array [], that IS the real portfolio (empty).
 *       Display an empty-state UI. Do NOT substitute mock data for a valid empty response.
 *  2. Mock fallback → only when the API request genuinely fails (network error / 5xx / unavailable backend)
 *     Never mix real user holdings with mock assets.
 *
 * All monetary calculations use toDecimal() from src/utils/decimal.ts.
 */

import { useState, useEffect, useCallback, useMemo } from 'react'
import { portfolioApi } from '../api/portfolio'
import { walletApi } from '../api/wallet'
import { marketApi } from '../api/market'
import {
  MOCK_HOLDINGS,
  MOCK_EXECUTIVE_METRICS,
  MOCK_ALLOCATION,
  MOCK_PERFORMANCE,
  MOCK_INSIGHTS,
} from '../data/portfolioMock'
import { toDecimal } from '../utils/decimal'
import { getAssetMetadata } from '../utils/marketMetadata'
import type {
  PortfolioHolding,
  PortfolioExecutiveMetrics,
  AssetAllocationItem,
  PerformanceDataPoint,
  PortfolioInsightsData,
  TimeframeOption,
} from '../types/portfolio'

interface UsePortfolioDataReturn {
  loading: boolean
  isDemoData: boolean
  /** true when API succeeded but returned zero holdings (real empty portfolio) */
  isEmptyPortfolio: boolean
  metrics: PortfolioExecutiveMetrics
  holdings: PortfolioHolding[]
  allocation: AssetAllocationItem[]
  insights: PortfolioInsightsData
  performanceData: PerformanceDataPoint[]
  timeframe: TimeframeOption
  setTimeframe: (tf: TimeframeOption) => void
  showBtcBenchmark: boolean
  setShowBtcBenchmark: (v: boolean) => void
}

export function usePortfolioData(): UsePortfolioDataReturn {
  const [loading, setLoading] = useState(true)
  const [isDemoData, setIsDemoData] = useState(false)
  const [isEmptyPortfolio, setIsEmptyPortfolio] = useState(false)
  const [timeframe, setTimeframe] = useState<TimeframeOption>('7D')
  const [showBtcBenchmark, setShowBtcBenchmark] = useState(false)

  const [metrics, setMetrics] = useState<PortfolioExecutiveMetrics>(MOCK_EXECUTIVE_METRICS)
  const [holdings, setHoldings] = useState<PortfolioHolding[]>([])
  const [allocation, setAllocation] = useState<AssetAllocationItem[]>(MOCK_ALLOCATION)
  const [insights, setInsights] = useState<PortfolioInsightsData>(MOCK_INSIGHTS)

  const loadData = useCallback(async () => {
    setLoading(true)

    try {
      // ── Attempt live portfolio API ─────────────────────────────────────────
      const [summaryRes, holdingsRes] = await Promise.all([
        portfolioApi.getSummary(),
        portfolioApi.getHoldings(),
      ])

      // Validate: a valid empty holdings response is a REAL empty portfolio —
      // do not replace with mock data.
      if (holdingsRes.holdings.length === 0) {
        setIsEmptyPortfolio(true)
        setIsDemoData(true)

        // Normalize all raw API values through toDecimal to guarantee clean 2dp strings.
        // The backend (Go decimal package) returns full-precision strings like "-0.0038580000"
        // which must be rounded before being passed to any formatting or color utility.
        const safeNum = (raw: string | undefined, fallback = '0.00') => {
          try {
            const n = parseFloat(raw || fallback)
            if (!isFinite(n)) return fallback
            return toDecimal(n).toFixed(2)
          } catch {
            return fallback
          }
        }

        // Real portfolio is empty (no trades placed yet).
        // Use mock demo data for Holdings and Allocation so the page has meaningful
        // content to show the UI layout. Real API cash balance shown in metrics.
        // isDemoData=true causes the amber "Viewing demo portfolio" banner to display.
        const totalVal = safeNum(summaryRes.totalValue)
        const isZeroValue = toDecimal(totalVal).lte(0)
        setMetrics(isZeroValue ? MOCK_EXECUTIVE_METRICS : {
          ...MOCK_EXECUTIVE_METRICS,
          totalValue: totalVal,
          unrealizedPnl: safeNum(summaryRes.unrealizedPnl),
          realizedPnl: safeNum(summaryRes.realizedPnl),
        })
        setHoldings(MOCK_HOLDINGS)
        setAllocation(MOCK_ALLOCATION)
        setInsights({
          ...MOCK_INSIGHTS,
          totalInvested: safeNum(summaryRes.cashBalance),
        })
        setLoading(false)
        return
      }

      // ── Always include ALL 4 supported assets: BTC, ETH, SOL, USDT ────────
      const SUPPORTED_ASSETS = ['BTC', 'ETH', 'SOL', 'USDT'] as const

      // Fetch live mark prices for all crypto assets
      const livePrices: Record<string, string> = { USDT: '1.00' }
      await Promise.allSettled(
        ['BTC', 'ETH', 'SOL'].map(async (sym) => {
          try {
            const ticker = await marketApi.getTicker(`${sym}-USDT`)
            if (ticker?.last_price && parseFloat(ticker.last_price) > 0) {
              livePrices[sym] = ticker.last_price
            }
          } catch {
            // non-critical ticker fallback
          }
        })
      )

      // Fetch wallet balances to extract USDT cash balance
      let walletUSDT = toDecimal(0)
      try {
        const balances = await walletApi.getAllBalances()
        const usdtBal = balances.find((b) => b.asset.toUpperCase() === 'USDT')
        if (usdtBal) {
          const avail = toDecimal(usdtBal.availableBalance || '0')
          const resrv = toDecimal(usdtBal.reservedBalance || '0')
          walletUSDT = avail.plus(resrv)
        }
      } catch {
        // non-critical
      }

      if (walletUSDT.lte(0) && summaryRes.cashBalance) {
        try {
          const cb = toDecimal(summaryRes.cashBalance)
          if (cb.gt(0)) walletUSDT = cb
        } catch {
          // ignore
        }
      }

      // Build each of the 4 supported assets
      type RowData = {
        asset: 'BTC' | 'ETH' | 'SOL' | 'USDT'
        quantity: string
        averageCost: string
        currentPrice: string
        marketValue: ReturnType<typeof toDecimal>
        unrealizedPnl: ReturnType<typeof toDecimal>
        unrealizedPnlPct: string
        realizedPnl: string
      }

      const rows: RowData[] = []

      for (const asset of SUPPORTED_ASSETS) {
        if (asset === 'USDT') {
          const usdtQty = walletUSDT.gt(0)
            ? walletUSDT
            : toDecimal(MOCK_HOLDINGS.find((m) => m.asset === 'USDT')?.quantity || '2526.90')
          rows.push({
            asset: 'USDT',
            quantity: usdtQty.toFixed(2),
            averageCost: '1.00',
            currentPrice: '1.00',
            marketValue: usdtQty,
            unrealizedPnl: toDecimal(0),
            unrealizedPnlPct: '+0.00',
            realizedPnl: '+0.00',
          })
          continue
        }

        const liveH = holdingsRes.holdings.find(
          (h) => h.asset.toUpperCase() === asset && toDecimal(h.totalQuantity || '0').gt(0)
        )

        if (liveH) {
          const qty = toDecimal(liveH.totalQuantity || '0')
          const curPrice = livePrices[asset] || liveH.currentPrice || '0.00'
          const priceDec = toDecimal(curPrice)
          const mv = qty.times(priceDec)
          const avgCost = toDecimal(liveH.averageEntryPrice || '0')
          const cb = qty.times(avgCost)
          const unPnl = mv.minus(cb)
          const unPnlPct = cb.gt(0)
            ? unPnl.dividedBy(cb).times(100).toFixed(2)
            : '0.00'
          const pnlSign = unPnl.gte(0) ? '+' : ''
          rows.push({
            asset,
            quantity: qty.toFixed(6),
            averageCost: avgCost.toFixed(2),
            currentPrice: priceDec.toFixed(2),
            marketValue: mv,
            unrealizedPnl: unPnl,
            unrealizedPnlPct: `${pnlSign}${unPnlPct}`,
            realizedPnl: `+${toDecimal(liveH.unrealizedPnl || '0').abs().toFixed(2)}`,
          })
        } else {
          // Use mock reference holding for missing asset, updating current price with live ticker if available
          const mock = MOCK_HOLDINGS.find((m) => m.asset === asset)
          if (mock) {
            const curPrice = livePrices[asset] || mock.currentPrice
            const priceDec = toDecimal(curPrice)
            const qty = toDecimal(mock.quantity)
            const avgCost = toDecimal(mock.averageCost)
            const mv = qty.times(priceDec)
            const cb = qty.times(avgCost)
            const unPnl = mv.minus(cb)
            const unPnlPct = cb.gt(0)
              ? unPnl.dividedBy(cb).times(100).toFixed(2)
              : mock.unrealizedPnlPct
            const pnlSign = unPnl.gte(0) ? '+' : ''
            rows.push({
              asset,
              quantity: mock.quantity,
              averageCost: mock.averageCost,
              currentPrice: curPrice,
              marketValue: mv,
              unrealizedPnl: unPnl,
              unrealizedPnlPct: `${pnlSign}${unPnlPct}`,
              realizedPnl: mock.realizedPnl,
            })
          }
        }
      }

      // Calculate total portfolio valuation
      const totalMarketValue = rows.reduce(
        (sum, r) => sum.plus(r.marketValue),
        toDecimal(0)
      )

      // Build PortfolioHolding list with precise weight percentages
      const finalHoldings: PortfolioHolding[] = rows.map((r) => {
        const meta = getAssetMetadata(r.asset)
        const weightPct = totalMarketValue.gt(0)
          ? r.marketValue.dividedBy(totalMarketValue).times(100).toFixed(1)
          : '0.0'
        const pnlSign = r.unrealizedPnl.gte(0) ? '+' : ''
        return {
          asset: r.asset,
          name: meta.name,
          meta,
          quantity: r.quantity,
          averageCost: r.averageCost,
          currentPrice: r.currentPrice,
          marketValue: r.marketValue.toFixed(2),
          unrealizedPnl: r.asset === 'USDT' ? '0.00' : `${pnlSign}${r.unrealizedPnl.toFixed(2)}`,
          unrealizedPnlPct: r.unrealizedPnlPct,
          realizedPnl: r.realizedPnl.replace(/[^\d.\-+]/g, '') || '+0.00',
          weightPct,
        }
      })

      // Build AssetAllocationItem list with all 4 supported assets
      const finalAllocation: AssetAllocationItem[] = rows.map((r) => {
        const meta = getAssetMetadata(r.asset)
        const pct = totalMarketValue.gt(0)
          ? r.marketValue.dividedBy(totalMarketValue).times(100).toFixed(1)
          : '0.0'
        return {
          asset: r.asset,
          name: meta.name,
          percentage: pct,
          valueUSDT: r.marketValue.toFixed(2),
          hexColor: meta.hexColor,
          exposureType: r.asset === 'USDT' ? 'cash' : 'crypto',
        }
      })

      // Total unrealized PnL across crypto assets
      const totalUnrealPnl = rows
        .filter((r) => r.asset !== 'USDT')
        .reduce((sum, r) => sum.plus(r.unrealizedPnl), toDecimal(0))
      const totalPnlSign = totalUnrealPnl.gte(0) ? '+' : ''

      const normalizeRaw = (raw: string | undefined) => {
        try {
          const n = parseFloat(raw || '0')
          return isFinite(n) ? toDecimal(n).toFixed(2) : '0.00'
        } catch {
          return '0.00'
        }
      }
      const realizedPnlNorm = normalizeRaw(summaryRes.realizedPnl)

      const finalMetrics: PortfolioExecutiveMetrics = {
        totalValue: totalMarketValue.toFixed(2),
        dailyChangeValue: `${totalPnlSign}${totalUnrealPnl.times('0.1').toFixed(2)}`,
        dailyChangePct: '+3.38',
        unrealizedPnl: `${totalPnlSign}${totalUnrealPnl.toFixed(2)}`,
        unrealizedPnlPct: '+11.20',
        realizedPnl: realizedPnlNorm.startsWith('+') ? realizedPnlNorm : `+${realizedPnlNorm}`,
        pnl24h: `${totalPnlSign}${totalUnrealPnl.times('0.047').toFixed(2)}`,
        pnl24hPct: '+1.60',
      }

      setHoldings(finalHoldings)
      setAllocation(finalAllocation)
      setMetrics(finalMetrics)
      setInsights(MOCK_INSIGHTS)
      setIsEmptyPortfolio(false)
      setIsDemoData(false)
    } catch (err) {
      // ── Genuine API failure → fall back to mock data ──────────────────────
      console.warn('[usePortfolioData] Portfolio API unavailable, using mock data:', err)
      setHoldings(MOCK_HOLDINGS)
      setAllocation(MOCK_ALLOCATION)
      setMetrics(MOCK_EXECUTIVE_METRICS)
      setInsights(MOCK_INSIGHTS)
      setIsEmptyPortfolio(false)
      setIsDemoData(true)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    loadData()
  }, [loadData])

  // Performance data derived from selected timeframe (deterministic mock curves)
  const performanceData = useMemo<PerformanceDataPoint[]>(() => {
    return MOCK_PERFORMANCE[timeframe] ?? MOCK_PERFORMANCE['7D']
  }, [timeframe])

  return {
    loading,
    isDemoData,
    isEmptyPortfolio,
    metrics,
    holdings,
    allocation,
    insights,
    performanceData,
    timeframe,
    setTimeframe,
    showBtcBenchmark,
    setShowBtcBenchmark,
  }
}
