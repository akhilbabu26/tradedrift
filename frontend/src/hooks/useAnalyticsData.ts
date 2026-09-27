/**
 * src/hooks/useAnalyticsData.ts
 *
 * Derives trading analytics, performance breakdown, and execution journal
 * from live backend services (Portfolio, Wallet, Trades, and Markets).
 */

import { useState, useEffect, useCallback, useMemo } from 'react'
import { portfolioApi } from '../api/portfolio'
import { tradesApi, type BackendTrade } from '../api/trades'
import { marketApi } from '../api/market'
import { useAuthStore } from '../store/authStore'
import { toDecimal } from '../utils/decimal'
import { getAssetMetadata } from '../utils/marketMetadata'
import { formatPrice, formatQuantity, formatDate } from '../utils/formatters'
import type {
  KpiCardItem,
  AssetPerformanceItem,
  BySideData,
  ByAssetFillItem,
  ByMonthFillItem,
  ExecutionFillItem,
  DateRangeOption,
} from '../types/analytics'

function isWithinDateRange(timeStr: string | undefined, dateRange: DateRangeOption): boolean {
  if (!timeStr) return true
  const ts = new Date(timeStr).getTime()
  if (isNaN(ts) || ts <= 0) return true
  const now = Date.now()

  switch (dateRange) {
    case 'Last 7 Days':
      return now - ts <= 7 * 24 * 60 * 60 * 1000
    case 'Last 30 Days':
      return now - ts <= 30 * 24 * 60 * 60 * 1000
    case 'Last 90 Days':
      return now - ts <= 90 * 24 * 60 * 60 * 1000
    case 'Year to Date': {
      const startOfYear = new Date(new Date().getFullYear(), 0, 1).getTime()
      return ts >= startOfYear
    }
    default:
      return true
  }
}

export function useAnalyticsData(dateRange: DateRangeOption) {
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const [summary, setSummary] = useState({
    totalValue: '0.00',
    realizedPnl: '0.00',
    unrealizedPnl: '0.00',
    cashBalance: '0.00',
  })
  const [rawHoldings, setRawHoldings] = useState<any[]>([])
  const [rawTrades, setRawTrades] = useState<BackendTrade[]>([])
  const [marketPrices, setMarketPrices] = useState<Record<string, string>>({})

  const loadData = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const [sumRes, holdRes, trdRes, btcTicker, ethTicker, solTicker] = await Promise.allSettled([
        portfolioApi.getSummary(),
        portfolioApi.getHoldings(),
        tradesApi.listTrades({ limit: 100 }),
        marketApi.getTicker('BTC-USDT'),
        marketApi.getTicker('ETH-USDT'),
        marketApi.getTicker('SOL-USDT'),
      ])

      if (sumRes.status === 'fulfilled' && sumRes.value) {
        setSummary({
          totalValue: sumRes.value.totalValue || '0.00',
          realizedPnl: sumRes.value.realizedPnl || '0.00',
          unrealizedPnl: sumRes.value.unrealizedPnl || '0.00',
          cashBalance: sumRes.value.cashBalance || '0.00',
        })
      }

      if (holdRes.status === 'fulfilled' && holdRes.value?.holdings) {
        setRawHoldings(holdRes.value.holdings)
      } else {
        setRawHoldings([])
      }

      if (trdRes.status === 'fulfilled' && trdRes.value?.trades) {
        setRawTrades(trdRes.value.trades)
      } else {
        setRawTrades([])
      }

      const prices: Record<string, string> = {}
      if (btcTicker.status === 'fulfilled' && btcTicker.value?.last_price) {
        prices['BTC'] = btcTicker.value.last_price
      }
      if (ethTicker.status === 'fulfilled' && ethTicker.value?.last_price) {
        prices['ETH'] = ethTicker.value.last_price
      }
      if (solTicker.status === 'fulfilled' && solTicker.value?.last_price) {
        prices['SOL'] = solTicker.value.last_price
      }
      setMarketPrices(prices)
    } catch (err: any) {
      setError(err?.message || 'Failed to load analytics data')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    loadData()
  }, [loadData])

  // ── 0. Filter Trades by Date Range ────────────────────────────────────────
  const filteredTrades = useMemo(() => {
    return rawTrades.filter((t) => {
      const timeStr = t.executed_at ?? t.created_at
      return isWithinDateRange(timeStr, dateRange)
    })
  }, [rawTrades, dateRange])

  // ── 1. Calculate Real KPIs ────────────────────────────────────────────────
  const currentUserId = useAuthStore.getState().user?.userId

  const kpis: KpiCardItem[] = useMemo(() => {
    const netPnlDec = toDecimal(summary.realizedPnl).plus(toDecimal(summary.unrealizedPnl))
    const isNetPos = netPnlDec.gte(0)
    const netPnlAbsFormatted = Math.abs(netPnlDec.toNumber()).toLocaleString('en-US', {
      minimumFractionDigits: 2,
      maximumFractionDigits: 2,
    })

    // Volume across filtered trades
    let totalVolume = toDecimal(0)
    for (const t of filteredTrades) {
      const q = toDecimal(t.quantity || '0')
      const p = toDecimal(t.price || '0')
      totalVolume = totalVolume.plus(q.times(p))
    }

    // Real Win Rate from closed (SELL) trades
    const sellTrades = filteredTrades.filter((t) =>
      currentUserId && t.seller_id ? t.seller_id === currentUserId : (t.taker_side || '').toUpperCase() === 'SELL'
    )

    let winningTradesCount = 0
    for (const st of sellTrades) {
      const baseAsset = st.base_asset ?? st.market_id?.split('-')[0] ?? 'BTC'
      const holding = rawHoldings.find((h) => h.asset === baseAsset)
      const avgEntry = toDecimal(holding?.averageEntryPrice || '0')
      const sellPrice = toDecimal(st.price || '0')
      if (avgEntry.gt(0) && sellPrice.gte(avgEntry)) {
        winningTradesCount++
      }
    }

    const closedCount = sellTrades.length
    const winRatePct = closedCount > 0 ? (winningTradesCount / closedCount) * 100 : 0
    const winRateIsPos = closedCount > 0 ? winRatePct >= 50 : true

    return [
      {
        id: 'net_pnl',
        label: 'Net PnL',
        value: `${isNetPos ? '+' : '-'}$${netPnlAbsFormatted}`,
        trendValue: isNetPos ? '+100%' : '-100%',
        trendPeriod: 'vs prior',
        isPositive: isNetPos,
        visualType: 'sparkline',
      },
      {
        id: 'total_volume',
        label: 'Traded Volume',
        value: `$${totalVolume.toNumber().toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`,
        trendValue: totalVolume.gt(0) ? `${filteredTrades.length} fills` : '0 fills',
        trendPeriod: dateRange.toLowerCase(),
        isPositive: true,
        visualType: 'bars',
      },
      {
        id: 'win_rate',
        label: 'Win Rate',
        value: closedCount > 0 ? `${winRatePct.toFixed(1)}%` : '—',
        trendValue: closedCount > 0
          ? `${winningTradesCount}W / ${closedCount - winningTradesCount}L`
          : `${filteredTrades.length} fills`,
        trendPeriod: closedCount > 0 ? 'closed' : 'sample',
        isPositive: winRateIsPos,
        visualType: 'sparkline',
      },
      {
        id: 'active_assets',
        label: 'Active Holdings',
        value: `${rawHoldings.filter((h) => parseFloat(h.totalQuantity || '0') > 0).length}`,
        trendValue: 'portfolio',
        trendPeriod: 'assets',
        isPositive: true,
        visualType: 'bars',
      },
    ]
  }, [summary, filteredTrades, rawHoldings, currentUserId, dateRange])

  // ── 2. Asset Performance List ─────────────────────────────────────────────
  const assetPerformanceList: AssetPerformanceItem[] = useMemo(() => {
    const totalValNum = parseFloat(summary.totalValue) || 1
    const supported: Array<'BTC' | 'ETH' | 'SOL'> = ['BTC', 'ETH', 'SOL']

    const list: AssetPerformanceItem[] = supported.map((sym) => {
      const found = rawHoldings.find((h) => h.asset === sym)
      const meta = getAssetMetadata(sym)
      const qtyStr = found?.totalQuantity || '0.00000000'
      const avgPrice = found?.averageEntryPrice || '0.00'
      const curPrice = (found?.currentPrice && found.currentPrice !== '0.00' && found.currentPrice !== '0')
        ? found.currentPrice
        : (marketPrices[sym] || '0.00')
      const unPnl = found?.unrealizedPnl || '0.00'
      const unPnlDec = toDecimal(unPnl)
      const isPos = unPnlDec.gte(0)

      const mv = toDecimal(qtyStr).times(toDecimal(curPrice)).toNumber()
      const allocPct = Math.min(100, Math.round((mv / totalValNum) * 100))

      const costBasis = toDecimal(avgPrice).times(toDecimal(qtyStr))
      const pctNum = costBasis.gt(0) ? unPnlDec.dividedBy(costBasis).times(100).toNumber() : 0
      const isPctPos = pctNum >= 0

      return {
        asset: sym,
        name: meta.name,
        meta,
        holdings: `${formatQuantity(qtyStr, 4)} ${sym}`,
        avgEntryPrice: formatPrice(avgPrice),
        currentPrice: formatPrice(curPrice),
        unrealizedPnl: `${isPos ? '+' : '-'}$${Math.abs(unPnlDec.toNumber()).toFixed(2)}`,
        unrealizedPnlPct: `${isPctPos ? '+' : '-'}${Math.abs(pctNum).toFixed(2)}%`,
        allocationPct: allocPct,
        barColor: sym === 'BTC' ? '#f7931a' : sym === 'ETH' ? '#627eea' : '#9945ff',
      }
    })

    // Also include USDT cash
    const cashMeta = getAssetMetadata('USDT')
    const cashVal = parseFloat(summary.cashBalance) || 0
    const cashAlloc = Math.min(100, Math.round((cashVal / totalValNum) * 100))
    list.push({
      asset: 'USDT',
      name: cashMeta.name,
      meta: cashMeta,
      holdings: `${cashVal.toFixed(2)} USDT`,
      avgEntryPrice: '$1.00',
      currentPrice: '$1.00',
      unrealizedPnl: '$0.00',
      unrealizedPnlPct: '0.00%',
      allocationPct: cashAlloc,
      barColor: '#26a17b',
    })

    return list
  }, [rawHoldings, summary, marketPrices])

  // ── 3. Trading Activity Breakdown ─────────────────────────────────────────
  const bySide: BySideData = useMemo(() => {
    const total = filteredTrades.length
    if (total === 0) {
      return { totalFills: 0, buyFills: 0, buyPct: 0, sellFills: 0, sellPct: 0 }
    }
    const buyCount = filteredTrades.filter((t) =>
      currentUserId && t.buyer_id ? t.buyer_id === currentUserId : (t.taker_side || 'BUY').toUpperCase() === 'BUY'
    ).length
    const sellCount = total - buyCount
    return {
      totalFills: total,
      buyFills: buyCount,
      buyPct: Math.round((buyCount / total) * 100),
      sellFills: sellCount,
      sellPct: Math.round((sellCount / total) * 100),
    }
  }, [filteredTrades, currentUserId])

  const byAsset: ByAssetFillItem[] = useMemo(() => {
    const total = filteredTrades.length || 1
    const assets: Array<'BTC' | 'ETH' | 'SOL'> = ['BTC', 'ETH', 'SOL']

    return assets.map((sym) => {
      const meta = getAssetMetadata(sym)
      const count = filteredTrades.filter((t) => (t.market_id || '').includes(sym) || t.base_asset === sym).length
      return {
        asset: sym,
        name: meta.name,
        meta,
        fillsCount: count,
        percentage: Math.round((count / total) * 100),
        color: sym === 'BTC' ? '#f7931a' : sym === 'ETH' ? '#627eea' : '#9945ff',
      }
    })
  }, [filteredTrades])

  const byMonth: ByMonthFillItem[] = useMemo(() => {
    const months = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']
    const now = new Date()
    const curYear = now.getFullYear()
    const curMonthIdx = now.getMonth()

    // Last 3 months
    const last3 = [
      { year: curMonthIdx >= 2 ? curYear : curYear - 1, monthIdx: (curMonthIdx - 2 + 12) % 12 },
      { year: curMonthIdx >= 1 ? curYear : curYear - 1, monthIdx: (curMonthIdx - 1 + 12) % 12 },
      { year: curYear, monthIdx: curMonthIdx },
    ]

    const totalFillsCount = filteredTrades.length || 1

    return last3.map(({ year, monthIdx }) => {
      const monthName = months[monthIdx]
      const count = filteredTrades.filter((t) => {
        const timeStr = t.executed_at ?? t.created_at
        if (!timeStr) return false
        const d = new Date(timeStr)
        return d.getFullYear() === year && d.getMonth() === monthIdx
      }).length

      return {
        month: monthName,
        fillsCount: count,
        percentage: Math.round((count / totalFillsCount) * 100),
      }
    })
  }, [filteredTrades])

  // ── 4. Execution Journal Fills (Limit to 10 recent fills for overview) ─────
  const recentFills: ExecutionFillItem[] = useMemo(() => {
    return filteredTrades.slice(0, 10).map((t) => {
      const base = t.base_asset ?? t.market_id?.split('-')[0] ?? 'BTC'
      const qty = toDecimal(t.quantity || '0')
      const price = toDecimal(t.price || '0')
      const total = qty.times(price)
      const side: 'BUY' | 'SELL' = currentUserId && t.buyer_id
        ? (t.buyer_id === currentUserId ? 'BUY' : 'SELL')
        : (t.taker_side || 'BUY').toUpperCase() as 'BUY' | 'SELL'
      const timeStr = t.executed_at ?? t.created_at

      return {
        id: t.id,
        tradeId: t.id.startsWith('trd_') ? t.id : `trd_${t.id.substring(0, 6)}`,
        time: timeStr ? formatDate(timeStr) : 'Recent',
        pair: t.market_id?.replace('-', '/') ?? `${base}/USDT`,
        side,
        price: formatPrice(t.price || '0'),
        filledAmount: `${formatQuantity(t.quantity || '0', 4)} ${base}`,
        totalValue: formatPrice(total.toString()),
      }
    })
  }, [filteredTrades, currentUserId])

  return {
    loading,
    error,
    kpis,
    assetPerformanceList,
    bySide,
    byAsset,
    byMonth,
    recentFills,
    currentPortfolioValue: formatPrice(summary.totalValue),
    activeAssetsCount: rawHoldings.filter((h) => parseFloat(h.totalQuantity || '0') > 0).length,
    refetch: loadData,
  }
}

