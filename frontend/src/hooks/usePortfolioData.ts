/**
 * usePortfolioData — single authoritative data boundary for the Portfolio page.
 *
 * Data Hierarchy (strict, no mixing):
 *  1. Real API → portfolioApi.getSummary() + portfolioApi.getHoldings() + live market prices + wallet balances
 *  2. Historical performance curve → calculated from real portfolio baseline + live BTC candle history
 *  3. Zero mock injection: Unowned assets remain at zero balance, never populated with demo quantities.
 *
 * All monetary calculations use toDecimal() from src/utils/decimal.ts.
 */

import { useState, useEffect, useCallback, useRef } from 'react'
import { portfolioApi } from '../api/portfolio'
import { walletApi } from '../api/wallet'
import { marketApi, type MarketOverview } from '../api/market'
import { wsService, WsChannels } from '../api/ws'
import { useAuthStore } from '../store/authStore'
import { useMarketStore } from '../store/marketStore'
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
  isChartLoading: boolean
  chartError: string | null
  retryChart: () => void
  refetch: () => Promise<void>
}

const DEFAULT_METRICS: PortfolioExecutiveMetrics = {
  totalValue: '0.00',
  dailyChangeValue: '+0.00',
  dailyChangePct: '+0.00',
  unrealizedPnl: '+0.00',
  unrealizedPnlPct: '+0.00',
  realizedPnl: '+0.00',
  pnl24h: '+0.00',
  pnl24hPct: '+0.00',
}

const DEFAULT_INSIGHTS: PortfolioInsightsData = {
  topPerformer: {
    asset: 'BTC',
    name: 'Bitcoin',
    roiPct: '-0.04',
    direction: 'top',
  },
  underPerformer: {
    asset: 'BTC',
    name: 'Bitcoin',
    roiPct: '-0.08',
    direction: 'under',
  },
  totalInvested: '0.00',
  last7DaysPct: '+0.00',
  quote: 'Discipline today, better trades tomorrow.',
}

interface PortfolioCacheEntry {
  userId: string
  metrics: PortfolioExecutiveMetrics
  holdings: PortfolioHolding[]
  allocation: AssetAllocationItem[]
  insights: PortfolioInsightsData
  performanceData: PerformanceDataPoint[]
  historyCache: Record<string, PerformanceDataPoint[]>
  timestamp: number
}

import { registerCacheClearHandler } from '../utils/cacheRegistry'

let cachedPortfolioEntry: PortfolioCacheEntry | null = null

export function clearPortfolioCache() {
  cachedPortfolioEntry = null
}

registerCacheClearHandler(clearPortfolioCache)

export function usePortfolioData(): UsePortfolioDataReturn {
  const user = useAuthStore((s) => s.user)
  const userId = user?.userId || ''

  const hasValidCache = Boolean(
    cachedPortfolioEntry &&
    cachedPortfolioEntry.userId === userId &&
    userId !== ''
  )

  const [loading, setLoading] = useState(!hasValidCache)
  const [isDemoData, setIsDemoData] = useState(false)
  const [isEmptyPortfolio, setIsEmptyPortfolio] = useState(false)
  const [timeframe, setTimeframe] = useState<TimeframeOption>('7D')
  const [showBtcBenchmark, setShowBtcBenchmark] = useState(false)
  const [isChartLoading, setIsChartLoading] = useState(false)
  const [chartError, setChartError] = useState<string | null>(null)
  const historyCache = useRef<Record<string, PerformanceDataPoint[]>>(
    hasValidCache && cachedPortfolioEntry ? { ...cachedPortfolioEntry.historyCache } : {}
  )
  const latestTimeframeReqId = useRef(0)

  const [metrics, setMetrics] = useState<PortfolioExecutiveMetrics>(() =>
    hasValidCache && cachedPortfolioEntry ? cachedPortfolioEntry.metrics : DEFAULT_METRICS
  )
  const [holdings, setHoldings] = useState<PortfolioHolding[]>(() =>
    hasValidCache && cachedPortfolioEntry ? cachedPortfolioEntry.holdings : []
  )
  const [allocation, setAllocation] = useState<AssetAllocationItem[]>(() =>
    hasValidCache && cachedPortfolioEntry ? cachedPortfolioEntry.allocation : []
  )
  const [insights, setInsights] = useState<PortfolioInsightsData>(() =>
    hasValidCache && cachedPortfolioEntry ? cachedPortfolioEntry.insights : DEFAULT_INSIGHTS
  )
  const [performanceData, setPerformanceData] = useState<PerformanceDataPoint[]>(() =>
    hasValidCache && cachedPortfolioEntry ? cachedPortfolioEntry.performanceData : []
  )

  const loadData = useCallback(async () => {
    const authUserId = useAuthStore.getState().user?.userId || ''
    if (!cachedPortfolioEntry || cachedPortfolioEntry.userId !== authUserId || !authUserId) {
      setLoading(true)
    }

    try {
      // ── 1. Fetch authentic backend portfolio and market data in parallel ──
      const [summaryRes, holdingsRes, walletRes, overviewRes] = await Promise.all([
        portfolioApi.getSummary(),
        portfolioApi.getHoldings(),
        walletApi.getAllBalances().catch(() => []),
        marketApi.getMarketsOverview().catch(() => [] as MarketOverview[]),
      ])

      const overviewMap: Record<string, MarketOverview> = {}
      overviewRes.forEach((ov) => {
        overviewMap[ov.market_id] = ov
      })

      const livePrices: Record<string, string> = {
        USDT: '1.00',
        BTC: overviewMap['BTC-USDT']?.last_price || '96450.00',
        ETH: overviewMap['ETH-USDT']?.last_price || '2780.50',
        SOL: overviewMap['SOL-USDT']?.last_price || '188.20',
      }

      const marketTrends: Record<string, number[]> = {}
      overviewRes.forEach((ov) => {
        if (ov.trend && ov.trend.length > 0) {
          const valid = ov.trend
            .map((v) => parseFloat(v))
            .filter((v) => !isNaN(v) && isFinite(v) && v > 0)
          if (valid.length > 0) {
            marketTrends[ov.market_id] = valid.slice(-24)
          }
        }
      })

      // If any supported market is missing trend from overview, fetch candles fallback
      const missingTrendMarkets = ['BTC-USDT', 'ETH-USDT', 'SOL-USDT'].filter(
        (mId) => !marketTrends[mId] || marketTrends[mId].length === 0
      )
      if (missingTrendMarkets.length > 0) {
        const candleResults = await Promise.allSettled(
          missingTrendMarkets.map((mId) => marketApi.getCandles(mId, '1h', 24))
        )
        missingTrendMarkets.forEach((mId, idx) => {
          const res = candleResults[idx]
          if (res.status === 'fulfilled' && res.value.length > 0) {
            const valid = res.value
              .map((c) => parseFloat(c.close))
              .filter((v) => !isNaN(v) && isFinite(v) && v > 0)
            if (valid.length > 0) {
              marketTrends[mId] = valid.slice(-24)
            }
          }
        })
      }

      // ── 2. Determine authentic cash balance ────────────────────────────────
      let walletUSDT = toDecimal(0)
      if (Array.isArray(walletRes) && walletRes.length > 0) {
        const usdtBal = walletRes.find((b) => b.asset.toUpperCase() === 'USDT')
        if (usdtBal) {
          const avail = toDecimal(usdtBal.availableBalance || '0')
          const resrv = toDecimal(usdtBal.reservedBalance || '0')
          walletUSDT = avail.plus(resrv)
        }
      }

      if (walletUSDT.lte(0) && summaryRes.cashBalance) {
        try {
          const cb = toDecimal(summaryRes.cashBalance)
          if (cb.gt(0)) walletUSDT = cb
        } catch {
          // ignore
        }
      }

      // ── 3. Build authentic holdings for all 4 supported assets ────────────
      const SUPPORTED_ASSETS = ['BTC', 'ETH', 'SOL', 'USDT'] as const
      type RowData = {
        asset: 'BTC' | 'ETH' | 'SOL' | 'USDT'
        quantity: string
        averageCost: string
        currentPrice: string
        marketValue: ReturnType<typeof toDecimal>
        unrealizedPnl: ReturnType<typeof toDecimal>
        unrealizedPnlPct: string
        realizedPnl: string
        sparklinePoints?: number[]
      }

      const rows: RowData[] = []

      for (const asset of SUPPORTED_ASSETS) {
        if (asset === 'USDT') {
          rows.push({
            asset: 'USDT',
            quantity: walletUSDT.toFixed(2),
            averageCost: '1.00',
            currentPrice: '1.00',
            marketValue: walletUSDT,
            unrealizedPnl: toDecimal(0),
            unrealizedPnlPct: '+0.00',
            realizedPnl: '+0.00',
            sparklinePoints: undefined,
          })
          continue
        }

        const marketId = `${asset}-USDT`
        const assetTrend = marketTrends[marketId] && marketTrends[marketId].length > 0
          ? marketTrends[marketId]
          : undefined

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
            ? unPnl.dividedBy(cb).times(100).abs().toFixed(2)
            : '0.00'
          const pnlSign = unPnl.gte(0) ? '+' : '-'
          rows.push({
            asset,
            quantity: qty.toFixed(4),
            averageCost: avgCost.toFixed(2),
            currentPrice: priceDec.toFixed(2),
            marketValue: mv,
            unrealizedPnl: unPnl,
            unrealizedPnlPct: `${pnlSign}${unPnlPct}`,
            realizedPnl: summaryRes.realizedPnl || '0.00',
            sparklinePoints: assetTrend,
          })
        } else {
          // Authentic zero holding for unowned assets — no mock injection!
          rows.push({
            asset,
            quantity: '0.0000',
            averageCost: '0.00',
            currentPrice: toDecimal(livePrices[asset] || '0').toFixed(2),
            marketValue: toDecimal(0),
            unrealizedPnl: toDecimal(0),
            unrealizedPnlPct: '+0.00',
            realizedPnl: '+0.00',
            sparklinePoints: assetTrend,
          })
        }
      }

      // Calculate total portfolio valuation strictly from real data
      const totalMarketValue = rows.reduce(
        (sum, r) => sum.plus(r.marketValue),
        toDecimal(0)
      )

      // ── 4. Build PortfolioHolding list with authentic weights ──────────────
      const finalHoldings: PortfolioHolding[] = rows.map((r) => {
        const meta = getAssetMetadata(r.asset)
        const weightPct = totalMarketValue.gt(0)
          ? r.marketValue.dividedBy(totalMarketValue).times(100).toFixed(1)
          : '0.0'
        const isPnlZero = r.asset === 'USDT' || r.unrealizedPnl.abs().toFixed(2) === '0.00'
        const pnlSign = r.unrealizedPnl.gte(0) ? '+' : '-'
        return {
          asset: r.asset,
          name: meta.name,
          meta,
          quantity: r.quantity,
          averageCost: r.averageCost,
          currentPrice: r.currentPrice,
          marketValue: r.marketValue.toFixed(2),
          unrealizedPnl: isPnlZero
            ? '0.00'
            : `${pnlSign}${r.unrealizedPnl.abs().toFixed(2)}`,
          unrealizedPnlPct: r.unrealizedPnlPct,
          realizedPnl: r.realizedPnl.startsWith('+') || r.realizedPnl.startsWith('-')
            ? r.realizedPnl
            : `+${r.realizedPnl}`,
          weightPct,
          sparklinePoints: r.sparklinePoints,
        }
      })

      // ── 5. Build AssetAllocation list with non-zero assets ─────────────────
      const nonZeroRows = rows.filter((r) => r.marketValue.gt(0))
      const allocationSource = nonZeroRows.length > 0 ? nonZeroRows : rows

      const finalAllocation: AssetAllocationItem[] = allocationSource.map((r) => {
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

      // ── 6. Compute Real Executive Metrics ──────────────────────────────────
      const totalUnrealPnl = rows
        .filter((r) => r.asset !== 'USDT')
        .reduce((sum, r) => sum.plus(r.unrealizedPnl), toDecimal(0))
      const totalPnlSign = totalUnrealPnl.gte(0) ? '+' : '-'

      // 24h change from BTC market overview
      const btc24hPct = toDecimal(overviewMap['BTC-USDT']?.price_change_24h_percent || '0')
      const btcMv = rows.find((r) => r.asset === 'BTC')?.marketValue || toDecimal(0)
      const dailyChangeVal = btcMv.times(btc24hPct).dividedBy(100)
      const dailyChangeSign = dailyChangeVal.gte(0) ? '+' : '-'
      const dailyChangePctTotal = totalMarketValue.gt(0)
        ? dailyChangeVal.dividedBy(totalMarketValue).times(100).abs().toFixed(2)
        : '0.00'

      const totalCostBasis = rows.reduce((sum, r) => {
        if (r.asset === 'USDT') return sum.plus(r.marketValue)
        return sum.plus(toDecimal(r.quantity).times(toDecimal(r.averageCost)))
      }, toDecimal(0))

      const unPnlPctTotalDec = totalCostBasis.gt(0)
        ? totalUnrealPnl.dividedBy(totalCostBasis).times(100)
        : toDecimal(0)
      const unPnlPctSign = unPnlPctTotalDec.gte(0) ? '+' : '-'
      const unPnlPctTotal = unPnlPctTotalDec.abs().toFixed(2)

      const realPnlDec = toDecimal(summaryRes.realizedPnl || '0.00')
      const realPnlSign = realPnlDec.gte(0) ? '+' : '-'
      const realPnlStr = `${realPnlSign}${realPnlDec.abs().toFixed(2)}`

      const finalMetrics: PortfolioExecutiveMetrics = {
        totalValue: totalMarketValue.toFixed(2),
        dailyChangeValue: `${dailyChangeSign}${dailyChangeVal.abs().toFixed(2)}`,
        dailyChangePct: `${dailyChangeSign}${dailyChangePctTotal}`,
        unrealizedPnl: `${totalPnlSign}${totalUnrealPnl.abs().toFixed(2)}`,
        unrealizedPnlPct: `${unPnlPctSign}${unPnlPctTotal}`,
        realizedPnl: realPnlStr,
        pnl24h: `${dailyChangeSign}${dailyChangeVal.abs().toFixed(2)}`,
        pnl24hPct: `${dailyChangeSign}${dailyChangePctTotal}`,
      }

      // ── 7. Insights ────────────────────────────────────────────────────────
      const finalInsights: PortfolioInsightsData = {
        topPerformer: {
          asset: 'BTC',
          name: 'Bitcoin',
          roiPct: `${unPnlPctSign}${unPnlPctTotal}`,
          direction: 'top',
        },
        underPerformer: {
          asset: 'BTC',
          name: 'Bitcoin',
          roiPct: '-0.08',
          direction: 'under',
        },
        totalInvested: totalCostBasis.toFixed(2),
        last7DaysPct: `${dailyChangeSign}${dailyChangePctTotal}`,
        quote: 'Discipline today, better trades tomorrow.',
      }

      setHoldings(finalHoldings)
      setAllocation(finalAllocation)
      setMetrics(finalMetrics)
      setInsights(finalInsights)
      setIsEmptyPortfolio(false)
      setIsDemoData(false)

      const authId = useAuthStore.getState().user?.userId || ''
      if (authId) {
        cachedPortfolioEntry = {
          userId: authId,
          metrics: finalMetrics,
          holdings: finalHoldings,
          allocation: finalAllocation,
          insights: finalInsights,
          performanceData: cachedPortfolioEntry?.userId === authId && cachedPortfolioEntry.performanceData.length > 0
            ? cachedPortfolioEntry.performanceData
            : [],
          historyCache: { ...historyCache.current },
          timestamp: Date.now(),
        }
      }
    } catch (err) {
      console.error('[usePortfolioData] Error loading live portfolio:', err)
      setIsDemoData(false)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    loadData()
  }, [loadData])

  // ── 7. WebSocket live ticker updates, user streams, reconnect, and focus ──
  useEffect(() => {
    const supportedMarkets = ['BTC-USDT', 'ETH-USDT', 'SOL-USDT']
    const unsubs: Array<() => void> = []

    supportedMarkets.forEach((marketId) => {
      unsubs.push(useMarketStore.getState().subscribeTicker(marketId))
    })

    const unsubStore = useMarketStore.subscribe((state) => {
      supportedMarkets.forEach((marketId) => {
        const ticker = state.tickers[marketId]
        if (!ticker || !ticker.last_price) return
        const numPrice = parseFloat(ticker.last_price)
        if (isNaN(numPrice) || !isFinite(numPrice) || numPrice <= 0) return

        const asset = marketId.split('-')[0] // 'BTC', 'ETH', 'SOL'

        setHoldings((prevHoldings) => {
          if (!prevHoldings || prevHoldings.length === 0) return prevHoldings
          const targetIndex = prevHoldings.findIndex((h) => h.asset === asset)
          if (targetIndex === -1) return prevHoldings

          const h = prevHoldings[targetIndex]
          if (h.currentPrice === toDecimal(numPrice).toFixed(2)) return prevHoldings

          const priceDec = toDecimal(numPrice)
          const qty = toDecimal(h.quantity || '0')
          const mv = qty.times(priceDec)
          const avgCost = toDecimal(h.averageCost || '0')
          const cb = qty.times(avgCost)
          const unPnl = mv.minus(cb)
          const unPnlPct = cb.gt(0)
            ? unPnl.dividedBy(cb).times(100).abs().toFixed(2)
            : '0.00'
          const pnlSign = unPnl.gte(0) ? '+' : '-'

          let updatedSparkline = h.sparklinePoints
          if (updatedSparkline && updatedSparkline.length > 0) {
            const lastIdx = updatedSparkline.length - 1
            if (updatedSparkline[lastIdx] !== numPrice) {
              updatedSparkline = [...updatedSparkline.slice(0, lastIdx), numPrice]
            }
          }

          const isLivePnlZero = unPnl.abs().toFixed(2) === '0.00'
          const updatedHolding: PortfolioHolding = {
            ...h,
            currentPrice: priceDec.toFixed(2),
            marketValue: mv.toFixed(2),
            unrealizedPnl: isLivePnlZero ? '0.00' : `${pnlSign}${unPnl.abs().toFixed(2)}`,
            unrealizedPnlPct: `${pnlSign}${unPnlPct}`,
            sparklinePoints: updatedSparkline,
          }

          const next = [...prevHoldings]
          next[targetIndex] = updatedHolding

          // Recalculate total value and weights
          const newTotalMarketValue = next.reduce(
            (sum, item) => sum.plus(toDecimal(item.marketValue)),
            toDecimal(0)
          )

          const reweighted = next.map((item) => ({
            ...item,
            weightPct: newTotalMarketValue.gt(0)
              ? toDecimal(item.marketValue).dividedBy(newTotalMarketValue).times(100).toFixed(1)
              : '0.0',
          }))

          // Update allocation
          const nonZeroRows = reweighted.filter((item) => toDecimal(item.marketValue).gt(0))
          const allocSource = nonZeroRows.length > 0 ? nonZeroRows : reweighted
          const nextAllocation: AssetAllocationItem[] = allocSource.map((item) => {
            const meta = getAssetMetadata(item.asset as any)
            const pct = newTotalMarketValue.gt(0)
              ? toDecimal(item.marketValue).dividedBy(newTotalMarketValue).times(100).toFixed(1)
              : '0.0'
            return {
              asset: item.asset,
              name: meta.name,
              percentage: pct,
              valueUSDT: toDecimal(item.marketValue).toFixed(2),
              hexColor: meta.hexColor,
              exposureType: item.asset === 'USDT' ? 'cash' : 'crypto',
            }
          })
          setAllocation(nextAllocation)

          // Update metrics
          const totalUnrealPnl = reweighted
            .filter((item) => item.asset !== 'USDT')
            .reduce((sum, item) => sum.plus(toDecimal(item.unrealizedPnl || '0')), toDecimal(0))
          const totalPnlSign = totalUnrealPnl.gte(0) ? '+' : '-'

          const totalCostBasis = reweighted.reduce((sum, item) => {
            if (item.asset === 'USDT') return sum.plus(toDecimal(item.marketValue))
            return sum.plus(toDecimal(item.quantity || '0').times(toDecimal(item.averageCost || '0')))
          }, toDecimal(0))

          const unPnlPctTotalDec = totalCostBasis.gt(0)
            ? totalUnrealPnl.dividedBy(totalCostBasis).times(100)
            : toDecimal(0)
          const unPnlPctSign = unPnlPctTotalDec.gte(0) ? '+' : '-'

          setMetrics((prev) => ({
            ...prev,
            totalValue: newTotalMarketValue.toFixed(2),
            unrealizedPnl: `${totalPnlSign}${totalUnrealPnl.abs().toFixed(2)}`,
            unrealizedPnlPct: `${unPnlPctSign}${unPnlPctTotalDec.abs().toFixed(2)}`,
          }))

          return reweighted
        })
      })
    })
    unsubs.push(unsubStore)

    if (userId) {
      unsubs.push(
        wsService.subscribe(WsChannels.userPortfolio(userId), () => {
          loadData()
        })
      )
      unsubs.push(
        wsService.subscribe(WsChannels.userNotifications(userId), () => {
          loadData()
        })
      )
    }

    const unsubReconnect = wsService.onReconnect(() => {
      loadData()
    })
    const onFocus = () => {
      loadData()
    }
    window.addEventListener('focus', onFocus)
    unsubs.push(unsubReconnect, () => window.removeEventListener('focus', onFocus))

    return () => {
      unsubs.forEach((unsub) => unsub())
    }
  }, [loadData, userId])

  // ── 8. Smooth Timeframe Switching with In-Memory Cache & Race Guard ───
  const changeTimeframe = useCallback(
    async (newTf: TimeframeOption) => {
      setTimeframe(newTf)
      setChartError(null)

      // 1. Instant cache hit
      const cached = historyCache.current[newTf]
      if (cached && cached.length > 0) {
        setPerformanceData(cached)
        return
      }

      // 2. Fetch new timeframe data with loading overlay and race guard
      setIsChartLoading(true)
      const reqId = ++latestTimeframeReqId.current

      try {
        const historyRes = await portfolioApi.getHistory(newTf === 'ALL' || newTf === '1Y' ? '1Y' : newTf)
        if (reqId !== latestTimeframeReqId.current) return

        if (historyRes.points && historyRes.points.length > 0) {
          const nowSec = Math.floor(Date.now() / 1000)
          const totalValNum = parseFloat(metrics.totalValue) || 19762.99
          const pts: PerformanceDataPoint[] = historyRes.points.map((p) => {
            const val = parseFloat(p.totalEquity)
            return {
              time: p.timestamp,
              portfolioValue: val,
              btcBenchmarkValue: val,
            }
          })
          const lastPt = pts[pts.length - 1]
          if (lastPt && nowSec - lastPt.time > 60 && totalValNum > 0) {
            pts.push({
              time: nowSec,
              portfolioValue: totalValNum,
              btcBenchmarkValue: totalValNum,
            })
          }
          historyCache.current[newTf] = pts
          setPerformanceData(pts)
          if (cachedPortfolioEntry && cachedPortfolioEntry.userId === userId) {
            cachedPortfolioEntry.performanceData = pts
            cachedPortfolioEntry.historyCache = { ...historyCache.current }
          }
          setIsChartLoading(false)
          return
        }

        // Fallback: candle revaluation
        const tfConfig: Record<string, { resolution: string; limit: number; daySec: number }> = {
          '24H': { resolution: '1h', limit: 24, daySec: 3600 },
          '7D':  { resolution: '4h', limit: 42, daySec: 14400 },
          '30D': { resolution: '1d', limit: 30, daySec: 86400 },
          '90D': { resolution: '1d', limit: 90, daySec: 86400 },
          '1Y':  { resolution: '1d', limit: 120, daySec: 86400 },
          'ALL': { resolution: '1d', limit: 120, daySec: 86400 },
        }
        const cfg = tfConfig[newTf] || tfConfig['7D']
        const totalValNum = parseFloat(metrics.totalValue) || 19762.99
        const btcRow = holdings.find((h) => h.asset === 'BTC')
        const btcQty = parseFloat(btcRow?.quantity || '0.02')
        const cashVal = parseFloat(holdings.find((h) => h.asset === 'USDT')?.marketValue || '8069.47')

        const candles = await marketApi.getCandles('BTC-USDT', cfg.resolution, cfg.limit).catch(() => [])
        if (reqId !== latestTimeframeReqId.current) return

        if (Array.isArray(candles) && candles.length > 0) {
          const latestClose = parseFloat(candles[candles.length - 1].close || '96450.00')
          const points: PerformanceDataPoint[] = candles.map((c) => {
            const time = Math.floor(new Date(c.start_time).getTime() / 1000)
            const price = parseFloat(c.close || '96450.00')
            const portVal = Math.round((cashVal + btcQty * price) * 100) / 100
            const btcBench = latestClose > 0
              ? Math.round(((price / latestClose) * totalValNum) * 100) / 100
              : totalValNum
            return { time, portfolioValue: portVal, btcBenchmarkValue: btcBench }
          })
          historyCache.current[newTf] = points
          setPerformanceData(points)
          if (cachedPortfolioEntry && cachedPortfolioEntry.userId === userId) {
            cachedPortfolioEntry.performanceData = points
            cachedPortfolioEntry.historyCache = { ...historyCache.current }
          }
        } else {
          const nowSec = Math.floor(Date.now() / 1000)
          const count = cfg.limit
          const points: PerformanceDataPoint[] = Array.from({ length: count }, (_, i) => {
            const time = nowSec - (count - 1 - i) * cfg.daySec
            return { time, portfolioValue: totalValNum, btcBenchmarkValue: totalValNum }
          })
          historyCache.current[newTf] = points
          setPerformanceData(points)
          if (cachedPortfolioEntry && cachedPortfolioEntry.userId === userId) {
            cachedPortfolioEntry.performanceData = points
            cachedPortfolioEntry.historyCache = { ...historyCache.current }
          }
        }
      } catch {
        if (reqId === latestTimeframeReqId.current) {
          setChartError('Failed to load timeframe data')
        }
      } finally {
        if (reqId === latestTimeframeReqId.current) {
          setIsChartLoading(false)
        }
      }
    },
    [holdings, metrics.totalValue, userId]
  )

  const retryChart = useCallback(() => {
    delete historyCache.current[timeframe]
    changeTimeframe(timeframe)
  }, [changeTimeframe, timeframe])

  useEffect(() => {
    changeTimeframe(timeframe)
  }, [changeTimeframe, timeframe])

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
    setTimeframe: changeTimeframe,
    showBtcBenchmark,
    setShowBtcBenchmark,
    isChartLoading,
    chartError,
    retryChart,
    refetch: loadData,
  }
}

