/**
 * src/hooks/useDashboardData.ts
 *
 * Single authoritative data boundary for DashboardPage.
 * SOURCE OF TRUTH: frontend/docs/all backend apis.md
 *
 * Data sources (by section):
 *   Portfolio header     → portfolioApi.getSummary()
 *   Simulator balance    → walletApi.getAllBalances()
 *   Holdings / My Coins  → portfolioApi.getHoldings() + marketApi.getMarketsOverview()
 *   Market Pulse         → marketApi.getMarketsOverview() + WS live tickers
 *   Active Orders        → orderApi.listOrders({ limit: 50 }) filtered client-side for OPEN/PENDING
 *   Recent Fills         → tradesApi.listTrades({ limit: 100 })
 *   Portfolio Chart      → Authentic baseline equity (no synthetic oscillating multipliers)
 *   Order Cancellation   → orderApi.cancelOrder(orderId) with automatic refresh
 *
 * Financial values: all arithmetic via toDecimal() from src/utils/decimal.ts
 */

import { useState, useEffect, useCallback, useRef } from 'react'
import { portfolioApi } from '../api/portfolio'
import { walletApi } from '../api/wallet'
import { marketApi, type MarketOverview, type Candle } from '../api/market'
import { orderApi, isOrderActive, type Order } from '../api/order'
import { tradesApi } from '../api/trades'
import { wsService, WsChannels } from '../api/ws'
import { useAuthStore } from '../store/authStore'
import { useMarketStore } from '../store/marketStore'
import { toDecimal } from '../utils/decimal'
import { extractApiError } from '../utils/apiError'
import { evaluateTrendCoverage } from '../utils/trendCoverage'
import { formatTurnover } from '../utils/formatters'

import type {
  DashboardData,
  CoinHolding,
  MarketRow,
  ActiveOrder,
  RecentFill,
  PeriodTab,
  PortfolioDataPoint,
} from '../types/dashboard'


// ── Known dashboard markets ──────────────────────────────────────────────────
const DASHBOARD_MARKETS = [
  { id: 'BTC-USDT', asset: 'BTC', iconBg: 'bg-[#f7931a]/15 border-[#f7931a]/30 text-[#f7931a]', iconLabel: '₿', color: '#f7931a' },
  { id: 'ETH-USDT', asset: 'ETH', iconBg: 'bg-[#627eea]/15 border-[#627eea]/30 text-[#627eea]', iconLabel: 'Ξ', color: '#627eea' },
  { id: 'SOL-USDT', asset: 'SOL', iconBg: 'bg-[#9945ff]/15 border-[#9945ff]/30 text-[#9945ff]', iconLabel: 'S', color: '#9945ff' },
] as const

/** Normalize array of price string points into 0..1 values for Sparkline */
export function normalizeTrend(trend: string[] | undefined, targetPoints = 16): number[] {
  if (!trend || trend.length < 2) return [0.5, 0.5]
  const nums = trend.map((v) => parseFloat(v)).filter((v) => !isNaN(v) && v > 0)
  if (nums.length < 2) return [0.5, 0.5]

  let sampled: number[] = []
  if (nums.length <= targetPoints) {
    sampled = nums
  } else {
    const step = (nums.length - 1) / (targetPoints - 1)
    for (let i = 0; i < targetPoints; i++) {
      const idx = Math.min(Math.round(i * step), nums.length - 1)
      sampled.push(nums[idx])
    }
  }

  const min = Math.min(...sampled)
  const max = Math.max(...sampled)
  const range = max - min
  if (range === 0) return sampled.map(() => 0.5)
  return sampled.map((v) => Math.round(((v - min) / range) * 1000) / 1000)
}

const formatVolume = formatTurnover

function mapOrderToActiveOrder(o: Order): ActiveOrder {
  const isBuy = o.side?.toUpperCase() === 'BUY'
  const isMarket = o.order_type?.toUpperCase().includes('MARKET')
  const type = `${isMarket ? 'Market' : 'Limit'} ${isBuy ? 'Buy' : 'Sell'}`
  const side = (isBuy ? 'BUY' : 'SELL') as 'BUY' | 'SELL'
  const status: ActiveOrder['status'] =
    o.status === 'OPEN' ? 'Open' : o.status === 'PARTIALLY_FILLED' ? 'Partial' : 'Cancelled'
  return {
    id: o.id,
    type,
    side,
    pair: o.market_id.replace('-', '/'),
    price: toDecimal(o.price || '0').toFixed(2),
    amount: toDecimal(o.quantity || '0').toFixed(4),
    status,
  }
}

// ── Clean initial state (zero/neutral values, zero mock data leaks) ──────────
const INITIAL_DASHBOARD_DATA: DashboardData = {
  portfolioValue: '0.00',
  portfolioChangeUsdt: '0.00',
  portfolioChangePercent: '0.00',
  simulatorBalance: '0.00',
  availablePercent: 100,
  inOrdersPercent: 0,
  availableUsdt: '0.00',
  inOrdersUsdt: '0.00',
  holdings: [],
  chartData: [],
  allTimeProfit: '0.00',
  winRate: 0,
  totalTrades: 0,
  marketRows: DASHBOARD_MARKETS.map((m) => ({
    pair: `${m.asset}/USDT`,
    symbol: m.asset,
    price: '--',
    change24h: '0.00',
    positive: true,
    iconBg: m.iconBg,
    iconLabel: m.iconLabel,
    sparklinePoints: [0.5, 0.5],
    volume24h: '$0',
    quoteVolume: 0,
  })),
  activeOrders: [],
  services: [
    { name: 'Matching Engine', status: 'Online', latencyMs: 4  },
    { name: 'Market Data',     status: 'Online', latencyMs: 12 },
    { name: 'Order Service',   status: 'Online', latencyMs: 8  },
    { name: 'Wallet Service',  status: 'Online', latencyMs: 11 },
  ],
  recentFills: [],
}

export interface UseDashboardDataReturn {
  data: DashboardData
  loading: boolean
  error: string | null
  isDemoData: boolean
  refetch: () => void
  cancelOrder: (orderId: string) => Promise<void>
  activePeriod: PeriodTab
  setActivePeriod: (period: PeriodTab) => void
  isChartLoading: boolean
  chartError: string | null
  retryChart: () => void
}

interface DashboardCacheEntry {
  userId: string
  data: DashboardData
  isDemoData: boolean
  historyCache: Partial<Record<PeriodTab, PortfolioDataPoint[]>>
  timestamp: number
}

import { registerCacheClearHandler } from '../utils/cacheRegistry'

let cachedDashboardEntry: DashboardCacheEntry | null = null

interface CachedCandles {
  candles: Candle[]
  fetchedAt: number
}
let candlesCache: Record<string, CachedCandles> = {}

async function getDashboardCandles(marketId: string): Promise<Candle[]> {
  const cached = candlesCache[marketId]
  const now = Date.now()
  if (cached && now - cached.fetchedAt < 60_000 && cached.candles.length > 0) {
    return cached.candles
  }
  try {
    const candles = await marketApi.getCandles(marketId, '1h', 168)
    candlesCache[marketId] = { candles, fetchedAt: now }
    return candles
  } catch {
    return cached?.candles ?? []
  }
}

export function clearDashboardCache() {
  cachedDashboardEntry = null
  candlesCache = {}
}

registerCacheClearHandler(clearDashboardCache)

export function useDashboardData(): UseDashboardDataReturn {
  const currentUserId = useAuthStore((s) => s.user?.userId) || ''
  const hasValidCache = Boolean(
    cachedDashboardEntry &&
    cachedDashboardEntry.userId === currentUserId &&
    currentUserId !== ''
  )

  const [data, setData]             = useState<DashboardData>(() =>
    hasValidCache && cachedDashboardEntry ? cachedDashboardEntry.data : INITIAL_DASHBOARD_DATA
  )
  const [loading, setLoading]       = useState(!hasValidCache)
  const [error, setError]           = useState<string | null>(null)
  const [isDemoData, setIsDemoData] = useState(() =>
    hasValidCache && cachedDashboardEntry ? cachedDashboardEntry.isDemoData : false
  )
  const [activePeriod, setActivePeriodState] = useState<PeriodTab>('7D')
  const [isChartLoading, setIsChartLoading] = useState(false)
  const [chartError, setChartError] = useState<string | null>(null)

  const historyCache = useRef<Partial<Record<PeriodTab, PortfolioDataPoint[]>>>(
    hasValidCache && cachedDashboardEntry ? { ...cachedDashboardEntry.historyCache } : {}
  )
  const latestTimeframeReqId = useRef(0)

  const userId = currentUserId
  const livePrices = useRef<Record<string, string>>({})
  const lastFetchTimeRef = useRef(0)
  const inFlightPromiseRef = useRef<Promise<void> | null>(null)

  const buildData = useCallback(async (): Promise<void> => {
    if (inFlightPromiseRef.current) {
      return inFlightPromiseRef.current
    }

    const task = (async () => {
      const authUserId = useAuthStore.getState().user?.userId || ''
      if (!cachedDashboardEntry || cachedDashboardEntry.userId !== authUserId || !authUserId) {
        setLoading(true)
      }
      setError(null)

      try {
        // ── Step 1: Critical data — portfolio, wallet, snapshots history, live markets, and hourly candles (cached) ─
        const candlesPromises = DASHBOARD_MARKETS.map((m) => getDashboardCandles(m.id))

        const [summaryRes, holdingsRes, balancesRes, overviewRes, historyRes, candlesList] = await Promise.all([
          portfolioApi.getSummary(),
          portfolioApi.getHoldings().catch(() => ({ userId: '', holdings: [] })),
          walletApi.getAllBalances().catch(() => []),
          marketApi.getMarketsOverview().catch(() => [] as MarketOverview[]),
          portfolioApi.getHistory('7D').catch(() => ({ userId: '', timeframe: '7D', points: [] })),
          Promise.all(candlesPromises),
        ])

      // Map market overview data into lookup records
      const overviewMap: Record<string, MarketOverview> = {}
      overviewRes.forEach((ov) => {
        overviewMap[ov.market_id] = ov
      })

      // ── Step 2: Open orders + recent fills (non-critical) ─────────────────
      const [allOrders, tradesRes] = await Promise.allSettled([
        orderApi.listOrders({ limit: 50 }),
        tradesApi.listTrades({ limit: 100 }),
      ])

      // ── Portfolio header ───────────────────────────────────────────────────
      const totalValue = toDecimal(summaryRes.totalValue || '0')
      const unrealizedPnl = toDecimal(summaryRes.unrealizedPnl || '0')
      const realizedPnl = toDecimal(summaryRes.realizedPnl || '0')
      const allTimePnl = unrealizedPnl.plus(realizedPnl)
      const startingValue = totalValue.minus(unrealizedPnl)
      const changePercent = startingValue.gt(0)
        ? unrealizedPnl.dividedBy(startingValue).times(100).toFixed(2)
        : '0.00'

      // ── Wallet balances (Simulator Cash = USDT) ───────────────────────────
      const balances = balancesRes || []
      const usdtWallet = balances.find((b) => b.asset.toUpperCase() === 'USDT')

      const availableUsdt = usdtWallet
        ? toDecimal(usdtWallet.availableBalance || '0')
        : toDecimal(summaryRes.cashBalance || '0')

      const inOrdersUsdt = usdtWallet
        ? toDecimal(usdtWallet.reservedBalance || '0')
        : toDecimal('0')

      const totalWallet = availableUsdt.plus(inOrdersUsdt)
      let availPct = 100
      let inOrdersPct = 0

      if (totalWallet.gt(0)) {
        if (inOrdersUsdt.lte(0)) {
          availPct = 100
          inOrdersPct = 0
        } else if (availableUsdt.lte(0)) {
          availPct = 0
          inOrdersPct = 100
        } else {
          availPct = Math.round(availableUsdt.dividedBy(totalWallet).times(100).toNumber())
          availPct = Math.min(99, Math.max(1, availPct))
          inOrdersPct = 100 - availPct
        }
      }

      // ── Holdings / My Coins ─────────────────────────────────────────────────
      const holdings: CoinHolding[] = (holdingsRes.holdings || []).map((h) => {
        const asset = h.asset.toUpperCase()
        const marketId = `${asset}-USDT`
        const ov = overviewMap[marketId]
        const livePrice = livePrices.current[asset] || ov?.last_price || h.currentPrice || '0'
        const price = toDecimal(livePrice)
        const qty = toDecimal(h.totalQuantity || '0')
        const valueUsdt = qty.times(price)
        const pnl = toDecimal(h.unrealizedPnl || '0')
        const entryValue = valueUsdt.minus(pnl)
        const pnlPct = entryValue.gt(0)
          ? pnl.dividedBy(entryValue).times(100).toFixed(2)
          : '0.00'
        const allocation = totalValue.gt(0)
          ? valueUsdt.dividedBy(totalValue).times(100).toNumber()
          : 0
        const mkt = DASHBOARD_MARKETS.find((m) => m.asset === asset)

        return {
          asset,
          symbol: `${asset}/USDT`,
          holdings: qty.toFixed(4),
          valueUsdt: valueUsdt.toFixed(2),
          pnlUsdt: pnl.toFixed(2),
          pnlPercent: pnlPct,
          allocation: Math.round(allocation * 10) / 10,
          color: mkt?.color ?? '#64748b',
        }
      })

      // ── Market Pulse rows with authentic trend coverage ───────────────────
      const marketRows: MarketRow[] = DASHBOARD_MARKETS.map((m, idx) => {
        const ov = overviewMap[m.id]
        const price = livePrices.current[m.asset] || ov?.last_price || '--'
        const changeStr = livePrices.current[`${m.asset}_change`] || ov?.price_change_24h_percent || '0'

        // Evaluate genuine coverage using timestamp span from 1h candles
        const candles = candlesList[idx] || []
        const candleCloses = candles.length > 0 ? candles.map((c) => c.close) : (ov?.trend || [])
        const startCandle = candles[0]
        const endCandle = candles[candles.length - 1]

        const coverage = evaluateTrendCoverage(
          candleCloses.length,
          startCandle?.start_time,
          endCandle?.start_time
        )

        // Draw sparkline with genuine available candle observations
        const sparklinePoints = candleCloses.length > 0
          ? candleCloses.map((c) => parseFloat(String(c))).filter((v) => !isNaN(v) && isFinite(v) && v > 0)
          : []

        const quoteVolNum = ov?.quote_volume_24h ? parseFloat(ov.quote_volume_24h) : 0

        return {
          pair: `${m.asset}/USDT`,
          symbol: m.asset,
          price,
          change24h: toDecimal(changeStr).abs().toFixed(2),
          positive: toDecimal(changeStr).gte(0),
          iconBg: m.iconBg,
          iconLabel: m.iconLabel,
          sparklinePoints,
          volume24h: formatVolume(ov?.quote_volume_24h),
          quoteVolume: isNaN(quoteVolNum) ? 0 : quoteVolNum,
          trendStatus: coverage.status,
          trendLabel: coverage.label,
          trendHours: coverage.hoursCovered,
        }
      })


      // ── Active Orders ──────────────────────────────────────────────────────
      const activeOrdersList: ActiveOrder[] =
        allOrders.status === 'fulfilled'
          ? allOrders.value
              .filter(isOrderActive)
              .slice(0, 5)
              .map(mapOrderToActiveOrder)
          : []

      // ── Recent Fills ───────────────────────────────────────────────────────
      const currentUserId = userId || useAuthStore.getState().user?.userId
      const userTrades = tradesRes.status === 'fulfilled' ? tradesRes.value.trades : []
      const recentFills: RecentFill[] = userTrades.slice(0, 4).map((t) => {
        const side: 'Buy' | 'Sell' = currentUserId && t.buyer_id
          ? (t.buyer_id === currentUserId ? 'Buy' : 'Sell')
          : (t.taker_side?.toUpperCase() === 'BUY' ? 'Buy' : 'Sell')
        const timeIso = t.executed_at ?? t.created_at ?? new Date().toISOString()
        return {
          id: t.id,
          type: side,
          pair: t.market_id.replace('-', '/'),
          price: toDecimal(t.price).toFixed(2),
          amount: toDecimal(t.quantity).toFixed(4),
          timeIso,
        }
      })

      const realTradesCount = userTrades.length

      // ── Authentic Win Rate Calculation ──────────────────────────────────────
      // Closed trades correspond to SELL executions where position was closed/reduced.
      // Win Rate = (Profitable Closed Fills / Total Closed Fills) * 100.
      const sellTrades = userTrades.filter((t) =>
        currentUserId && t.seller_id
          ? t.seller_id === currentUserId
          : (t.taker_side || '').toUpperCase() === 'SELL'
      )

      let winRate = 0
      if (sellTrades.length > 0) {
        let winningTradesCount = 0
        for (const st of sellTrades) {
          const baseAsset = (st.base_asset ?? st.market_id?.split('-')[0] ?? '').toUpperCase()
          const holding = (holdingsRes.holdings || []).find((h) => h.asset.toUpperCase() === baseAsset)
          const avgEntry = toDecimal(holding?.averageEntryPrice || '0')
          const sellPrice = toDecimal(st.price || '0')
          if (avgEntry.gt(0) && sellPrice.gt(avgEntry)) {
            winningTradesCount++
          }
        }
        winRate = Math.round((winningTradesCount / sellTrades.length) * 100)
      }

      // ── Authentic Portfolio Snapshots Chart ────────────────────────────────
      const baselineVal = totalValue.toNumber()
      const nowSec = Math.floor(Date.now() / 1000)
      let chartPoints: PortfolioDataPoint[] = []

      if (historyRes.points && historyRes.points.length > 0) {
        chartPoints = historyRes.points.map((pt) => ({
          time: pt.timestamp,
          value: parseFloat(pt.totalEquity),
        }))
        // Ensure latest live equity is anchored at current timestamp if last snapshot is > 60s old
        const lastPt = chartPoints[chartPoints.length - 1]
        if (lastPt && nowSec - lastPt.time > 60 && baselineVal > 0) {
          chartPoints.push({
            time: nowSec,
            value: baselineVal,
          })
        }
      } else if (baselineVal > 0) {
        // Honest baseline: only current timestamp if no historical snapshots recorded yet
        chartPoints = [
          {
            time: nowSec,
            value: baselineVal,
          },
        ]
      }

      historyCache.current['7D'] = chartPoints

      // ── Final compiled state ───────────────────────────────────────────────
      const dashData: DashboardData = {
        portfolioValue: totalValue.toFixed(2),
        portfolioChangeUsdt: unrealizedPnl.toFixed(2),
        portfolioChangePercent: changePercent,
        simulatorBalance: totalWallet.toFixed(2),
        availablePercent: availPct,
        inOrdersPercent: inOrdersPct,
        availableUsdt: availableUsdt.toFixed(2),
        inOrdersUsdt: inOrdersUsdt.toFixed(2),
        holdings,
        chartData: chartPoints,
        allTimeProfit: allTimePnl.toFixed(2),
        winRate,
        totalTrades: realTradesCount,
        marketRows,
        activeOrders: activeOrdersList,
        services: INITIAL_DASHBOARD_DATA.services,
        recentFills,
      }

      setData(dashData)
      setIsDemoData(false)
      setError(null)

      const authId = useAuthStore.getState().user?.userId || ''
      if (authId) {
        cachedDashboardEntry = {
          userId: authId,
          data: dashData,
          isDemoData: false,
          historyCache: { ...historyCache.current },
          timestamp: Date.now(),
        }
      }
      lastFetchTimeRef.current = Date.now()
    } catch (err) {
      const msg = extractApiError(err)
      const authId = useAuthStore.getState().user?.userId || ''
      if (!cachedDashboardEntry || cachedDashboardEntry.userId !== authId) {
        setError(msg)
        setIsDemoData(true)
      } else {
        console.warn('Background dashboard revalidation failed:', msg)
      }
    } finally {
      setLoading(false)
      inFlightPromiseRef.current = null
    }
  })()

  inFlightPromiseRef.current = task
  return task
  }, [userId])

  // ── Smooth Timeframe Switching ─────────────────────────────────────────────
  const changePeriod = useCallback(
    async (newPeriod: PeriodTab): Promise<void> => {
      // 1. Immediately update active period visual selection
      setActivePeriodState(newPeriod)
      setChartError(null)

      // 2. Instant cache hit — reuse valid cached points without making a redundant request
      const cached = historyCache.current[newPeriod]
      if (cached && cached.length > 0) {
        setData((prev) => ({
          ...prev,
          chartData: cached,
        }))
        return
      }

      // 3. Increment request ID to guard against out-of-order stale responses
      const reqId = ++latestTimeframeReqId.current
      setIsChartLoading(true)

      try {
        const res = await portfolioApi.getHistory(newPeriod)
        // Discard if user has clicked another timeframe before this resolved
        if (reqId !== latestTimeframeReqId.current) return

        const baselineVal = toDecimal(data.portfolioValue || '0').toNumber()
        const nowSec = Math.floor(Date.now() / 1000)
        let points: PortfolioDataPoint[] = []

        if (res.points && res.points.length > 0) {
          points = res.points.map((pt) => ({
            time: pt.timestamp,
            value: parseFloat(pt.totalEquity),
          }))
          const lastPt = points[points.length - 1]
          if (lastPt && nowSec - lastPt.time > 60 && baselineVal > 0) {
            points.push({
              time: nowSec,
              value: baselineVal,
            })
          }
        } else if (baselineVal > 0) {
          points = [
            { time: nowSec - 60, value: baselineVal },
            { time: nowSec, value: baselineVal },
          ]
        }

        // Cache points for this timeframe
        historyCache.current[newPeriod] = points

        setData((prev) => ({
          ...prev,
          chartData: points,
        }))
      } catch (err) {
        if (reqId !== latestTimeframeReqId.current) return
        setChartError(extractApiError(err))
      } finally {
        if (reqId === latestTimeframeReqId.current) {
          setIsChartLoading(false)
        }
      }
    },
    [data.portfolioValue]
  )

  const retryChart = useCallback(() => {
    changePeriod(activePeriod)
  }, [changePeriod, activePeriod])


  // ── Cancel Order Action ────────────────────────────────────────────────────
  const cancelOrder = useCallback(
    async (orderId: string): Promise<void> => {
      await orderApi.cancelOrder(orderId)
      await buildData()
    },
    [buildData]
  )

  // ── Centralized marketStore: subscribe to live ticker streams ──────────────
  useEffect(() => {
    const unsubs: Array<() => void> = []

    // Subscribe to each dashboard market using reference-counted marketStore
    for (const m of DASHBOARD_MARKETS) {
      const unsub = useMarketStore.getState().subscribeTicker(m.id)
      unsubs.push(unsub)
    }

    // Reactively update Dashboard state whenever marketStore updates tickers
    const unsubStore = useMarketStore.subscribe((state) => {
      let hasChange = false
      for (const m of DASHBOARD_MARKETS) {
        const t = state.tickers[m.id]
        if (t && t.last_price && t.last_price !== livePrices.current[m.asset]) {
          hasChange = true
          livePrices.current[m.asset] = t.last_price
          livePrices.current[`${m.asset}_change`] = t.price_change_24h_percent
        }
      }
      if (!hasChange) return

      setData((prev) => {
        const updatedMarketRows = prev.marketRows.map((row) => {
          const t = state.tickers[`${row.symbol}-USDT`]
          if (!t) return row
          const price = t.last_price
          const change = t.price_change_24h_percent
          const quoteVolNum = parseFloat(t.quote_volume_24h || '0')
          const numPrice = parseFloat(price || '0')
          let updatedSparkline = row.sparklinePoints
          if (updatedSparkline && updatedSparkline.length > 0 && numPrice > 0) {
            const lastIdx = updatedSparkline.length - 1
            if (updatedSparkline[lastIdx] !== numPrice) {
              updatedSparkline = [...updatedSparkline.slice(0, lastIdx), numPrice]
            }
          }
          return {
            ...row,
            price,
            change24h: toDecimal(change || '0').abs().toFixed(2),
            positive: toDecimal(change || '0').gte(0),
            sparklinePoints: updatedSparkline,
            volume24h: formatTurnover(t.quote_volume_24h),
            quoteVolume: isNaN(quoteVolNum) ? 0 : quoteVolNum,
          }
        })

        // Reactively recalculate holdings value and unrealized PnL based on live ticker
        let totalHoldingsVal = toDecimal(0)
        let totalUnrealized = toDecimal(0)

        const updatedHoldings = prev.holdings.map((h) => {
          const currentPrice = toDecimal(livePrices.current[h.asset] || '0')
          const qty = toDecimal(h.holdings)
          const val = currentPrice.gt(0) ? qty.times(currentPrice) : toDecimal(h.valueUsdt)
          const prevPnl = toDecimal(h.pnlUsdt)
          const prevVal = toDecimal(h.valueUsdt)
          const costBasis = prevVal.minus(prevPnl)
          const newPnl = costBasis.gt(0) ? val.minus(costBasis) : prevPnl
          const newPnlPct = costBasis.gt(0)
            ? newPnl.dividedBy(costBasis).times(100).toFixed(2)
            : h.pnlPercent

          totalHoldingsVal = totalHoldingsVal.plus(val)
          totalUnrealized = totalUnrealized.plus(newPnl)

          return {
            ...h,
            valueUsdt: val.toFixed(2),
            pnlUsdt: (newPnl.gte(0) ? '+' : '') + newPnl.toFixed(2),
            pnlPercent: (newPnl.gte(0) ? '+' : '') + newPnlPct,
          }
        })

        const cash = toDecimal(prev.availableUsdt)
        const newTotalPortfolio = cash.plus(totalHoldingsVal)

        const reallocatedHoldings = updatedHoldings.map((h) => ({
          ...h,
          allocation: newTotalPortfolio.gt(0)
            ? Math.round(toDecimal(h.valueUsdt).dividedBy(newTotalPortfolio).times(1000).toNumber()) / 10
            : h.allocation,
        }))

        const startingValue = newTotalPortfolio.minus(totalUnrealized)
        const changePct = startingValue.gt(0)
          ? totalUnrealized.dividedBy(startingValue).times(100).toFixed(2)
          : '0.00'

        return {
          ...prev,
          marketRows: updatedMarketRows,
          holdings: reallocatedHoldings,
          portfolioValue: newTotalPortfolio.gt(0) ? newTotalPortfolio.toFixed(2) : prev.portfolioValue,
          portfolioChangeUsdt: (totalUnrealized.gte(0) ? '+' : '') + totalUnrealized.toFixed(2),
          portfolioChangePercent: changePct,
        }
      })
    })
    unsubs.push(unsubStore)

    let debounceTimer: ReturnType<typeof setTimeout> | null = null
    const debouncedBuildData = () => {
      if (debounceTimer) clearTimeout(debounceTimer)
      debounceTimer = setTimeout(() => {
        buildData()
      }, 1500)
    }

    if (userId) {
      // User notification stream (trade fills, order updates) — debounced
      const unsubNotifications = wsService.subscribe(WsChannels.userNotifications(userId), () => {
        debouncedBuildData()
      })
      // User portfolio stream (balance & snapshot updates) — debounced
      const unsubPortfolio = wsService.subscribe(WsChannels.userPortfolio(userId), () => {
        debouncedBuildData()
      })
      unsubs.push(unsubNotifications, unsubPortfolio)
    }

    // Auto-refresh when reconnecting — debounced
    const unsubReconnect = wsService.onReconnect(() => {
      debouncedBuildData()
    })
    // Auto-refresh when returning to tab — throttled with 20s cooldown
    const onFocus = () => {
      const now = Date.now()
      if (now - lastFetchTimeRef.current > 20_000) {
        buildData()
      }
    }
    window.addEventListener('focus', onFocus)
    unsubs.push(
      unsubReconnect,
      () => {
        if (debounceTimer) clearTimeout(debounceTimer)
        window.removeEventListener('focus', onFocus)
      }
    )

    return () => unsubs.forEach((u) => u())
  }, [buildData, userId])

  // ── Initial load ──────────────────────────────────────────────────────────
  useEffect(() => {
    buildData()
  }, [buildData])

  return {
    data,
    loading,
    error,
    isDemoData,
    refetch: buildData,
    cancelOrder,
    activePeriod,
    setActivePeriod: changePeriod,
    isChartLoading,
    chartError,
    retryChart,
  }
}

