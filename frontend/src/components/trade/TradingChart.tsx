import { useEffect, useRef, useState, useCallback } from 'react'
import {
  createChart,
  CandlestickSeries,
  AreaSeries,
  HistogramSeries,
  type IChartApi,
  type ISeriesApi,
  type UTCTimestamp,
  type CandlestickSeriesPartialOptions,
  type AreaSeriesPartialOptions,
  type HistogramSeriesPartialOptions,
} from 'lightweight-charts'
import type { Candle, Timeframe } from '../../types/trade'
import { wsService, WsChannels } from '../../api/ws'
import ChartToolbar from './ChartToolbar'
import { getPrecisionFromTickSize } from '../../utils/formatters'

interface TradingChartProps {
  marketId: string
  symbol: string           // e.g. "BTC/USDT"
  lastPrice: string
  candles: Candle[]
  loading: boolean
  isDemoData?: boolean
  timeframe: Timeframe
  tickSize?: string
  onTimeframeChange: (tf: Timeframe) => void
  onRefreshCandles?: () => void
}

const CANDLE_OPTIONS: CandlestickSeriesPartialOptions = {
  upColor:          '#10b981',
  downColor:        '#ef4444',
  borderUpColor:    '#10b981',
  borderDownColor:  '#ef4444',
  wickUpColor:      '#10b981',
  wickDownColor:    '#ef4444',
  borderVisible:    true,
  wickVisible:      true,
  priceLineVisible: true,
  lastValueVisible: true,
  priceFormat: {
    type: 'price',
    precision: 4,
    minMove: 0.0001,
  },
}

const AREA_OPTIONS: AreaSeriesPartialOptions = {
  lineColor:        '#10b981',
  topColor:         'rgba(16, 185, 129, 0.28)',
  bottomColor:      'rgba(16, 185, 129, 0.01)',
  lineWidth:        2,
  priceLineVisible: false,
  lastValueVisible: true,
  visible:          false,
  priceFormat: {
    type: 'price',
    precision: 4,
    minMove: 0.0001,
  },
}

const VOLUME_OPTIONS: HistogramSeriesPartialOptions = {
  color:            '#10b981',
  priceFormat:      { type: 'volume' },
  priceScaleId:     'volume',
  priceLineVisible: false,
  lastValueVisible: false,
}

interface SanitizedBar {
  time: UTCTimestamp
  open: number
  high: number
  low: number
  close: number
}

interface SanitizedVolume {
  time: UTCTimestamp
  value: number
  color: string
}

function getTimeframeSeconds(tf: Timeframe): number {
  switch (tf) {
    case '1m':  return 60
    case '5m':  return 300
    case '15m': return 900
    case '1h':  return 3600
    case '4h':  return 14400
    case '1d':  return 86400
    default:    return 3600
  }
}

/**
 * Validates, sorts ascending, and strictly deduplicates candle timestamps.
 * Guarantees time[i] < time[i + 1] to prevent Lightweight Charts invariant crashes.
 */
function sanitizeCandles(rawCandles: Candle[]): { bars: SanitizedBar[]; volumes: SanitizedVolume[] } {
  if (!Array.isArray(rawCandles) || rawCandles.length === 0) {
    return { bars: [], volumes: [] }
  }

  // 1. Validate numerical integrity and ISO timestamp
  const valid = rawCandles
    .map((c) => {
      if (!c || !c.start_time) return null
      const d = new Date(c.start_time)
      const ms = d.getTime()
      if (isNaN(ms) || ms <= 0) return null

      const time = Math.floor(ms / 1000) as UTCTimestamp
      const open = parseFloat(c.open)
      const high = parseFloat(c.high)
      const low = parseFloat(c.low)
      const close = parseFloat(c.close)
      const volume = parseFloat(c.volume || '0')

      if (
        isNaN(open) || isNaN(high) || isNaN(low) || isNaN(close) ||
        open <= 0 || high <= 0 || low <= 0 || close <= 0
      ) {
        return null
      }

      return {
        time,
        open,
        high: Math.max(high, open, close),
        low: Math.min(low, open, close),
        close,
        volume: isNaN(volume) || volume < 0 ? 0 : volume,
      }
    })
    .filter((c): c is NonNullable<typeof c> => c !== null)

  // 2. Sort ascending by timestamp
  valid.sort((a, b) => (a.time as number) - (b.time as number))

  // 3. Deduplicate timestamps ensuring time[i] < time[i + 1]
  const deduped: typeof valid = []
  for (const bar of valid) {
    if (deduped.length === 0) {
      deduped.push(bar)
    } else {
      const prev = deduped[deduped.length - 1]
      if ((bar.time as number) > (prev.time as number)) {
        deduped.push(bar)
      } else if ((bar.time as number) === (prev.time as number)) {
        deduped[deduped.length - 1] = bar
      }
    }
  }

  const bars: SanitizedBar[] = deduped.map((b) => ({
    time: b.time,
    open: b.open,
    high: b.high,
    low: b.low,
    close: b.close,
  }))

  const volumes: SanitizedVolume[] = deduped.map((b) => ({
    time: b.time,
    value: b.volume,
    color: b.close >= b.open ? 'rgba(16,185,129,0.45)' : 'rgba(239,68,68,0.45)',
  }))

  return { bars, volumes }
}

export default function TradingChart({
  marketId,
  symbol,
  lastPrice,
  candles,
  loading,
  isDemoData = false,
  timeframe,
  tickSize,
  onTimeframeChange,
  onRefreshCandles,
}: TradingChartProps) {
  const containerRef = useRef<HTMLDivElement>(null)
  const chartWrapperRef = useRef<HTMLDivElement>(null)
  const chartRef = useRef<IChartApi | null>(null)
  const candleRef = useRef<ISeriesApi<'Candlestick'> | null>(null)
  const areaRef = useRef<ISeriesApi<'Area'> | null>(null)
  const volumeRef = useRef<ISeriesApi<'Histogram'> | null>(null)

  const [chartType, setChartType] = useState<'candlestick' | 'line'>('candlestick')

  // Tracking refs for real-time live active candle, volume, deduplication, and market safety
  const lastBarRef = useRef<SanitizedBar | null>(null)
  const activeVolumeRef = useRef<number>(0)
  const activeMarketRef = useRef<string>(marketId)
  const hasFittedRef = useRef<boolean>(false)
  const totalBarsRef = useRef<number>(0)
  const seenTradeIdsRef = useRef<Set<string>>(new Set())

  // ── Create chart once ──────────────────────────────────────────────────────
  useEffect(() => {
    if (!containerRef.current) return
    const container = containerRef.current

    const chart = createChart(container, {
      width:  container.clientWidth || 600,
      height: container.clientHeight || 400,
      layout: {
        background: { color: 'transparent' },
        textColor:  '#64748b',
        fontSize:   11,
      },
      grid: {
        vertLines: { color: '#1e2530', style: 1 },
        horzLines: { color: '#1e2530', style: 1 },
      },
      rightPriceScale: {
        borderColor:  '#1e2530',
        textColor:    '#64748b',
        scaleMargins: { top: 0.10, bottom: 0.25 },
        autoScale:    true,
      },
      timeScale: {
        borderColor:         '#1e2530',
        timeVisible:         true,
        secondsVisible:      false,
        uniformDistribution: true,
        fixLeftEdge:         false,
        fixRightEdge:        false,
        rightOffset:         10,
        barSpacing:          6,
        minBarSpacing:       2,
      },
      crosshair: {
        vertLine: { color: '#475569', style: 1, width: 1, labelBackgroundColor: '#1e2530' },
        horzLine: { color: '#475569', style: 1, width: 1, labelBackgroundColor: '#1e2530' },
        mode: 1,
      },
      handleScroll: { mouseWheel: true, pressedMouseMove: true },
      handleScale:  { mouseWheel: true, pinch: true },
    })

    chartRef.current = chart

    const { precision, minMove } = getPrecisionFromTickSize(tickSize)
    const priceFormat = { type: 'price' as const, precision, minMove }

    // 1. Candlestick series
    const candleSeries = chart.addSeries(CandlestickSeries, {
      ...CANDLE_OPTIONS,
      priceFormat,
    })
    candleRef.current = candleSeries

    // 2. Area (Line) series
    const areaSeries = chart.addSeries(AreaSeries, {
      ...AREA_OPTIONS,
      priceFormat,
    })
    areaRef.current = areaSeries

    // 3. Volume histogram
    const volumeSeries = chart.addSeries(HistogramSeries, {
      ...VOLUME_OPTIONS,
      priceScaleId: 'volume',
    })
    volumeSeries.priceScale().applyOptions({
      scaleMargins: { top: 0.82, bottom: 0 },
    })
    volumeRef.current = volumeSeries

    // ResizeObserver — chart responds to container size
    const resizeObserver = new ResizeObserver((entries) => {
      for (const entry of entries) {
        const { width, height } = entry.contentRect
        if (width > 0 && height > 0) {
          chart.resize(width, height)
        }
      }
    })
    resizeObserver.observe(container)

    return () => {
      resizeObserver.disconnect()
      chart.remove()
      chartRef.current  = null
      candleRef.current = null
      areaRef.current   = null
      volumeRef.current = null
    }
  }, []) // Create once — never re-run

  // ── Dynamic Price Precision updates on tickSize change ──────────────────────
  useEffect(() => {
    const { precision, minMove } = getPrecisionFromTickSize(tickSize)
    const priceFormat = { type: 'price' as const, precision, minMove }
    candleRef.current?.applyOptions({ priceFormat })
    areaRef.current?.applyOptions({ priceFormat })
  }, [tickSize])

  // ── Chart type toggle (Candlestick vs Line) ─────────────────────────────────
  useEffect(() => {
    if (!candleRef.current || !areaRef.current) return
    const isCandle = chartType === 'candlestick'
    candleRef.current.applyOptions({ visible: isCandle })
    areaRef.current.applyOptions({ visible: !isCandle })
  }, [chartType])

  // ── Market Switching & Timeframe Reset ──────────────────────────────────────
  useEffect(() => {
    activeMarketRef.current = marketId
    hasFittedRef.current = false
    lastBarRef.current = null
    activeVolumeRef.current = 0
    totalBarsRef.current = 0
    seenTradeIdsRef.current.clear()

    // Immediately clear previous market's or timeframe's series
    if (candleRef.current) candleRef.current.setData([])
    if (areaRef.current) areaRef.current.setData([])
    if (volumeRef.current) volumeRef.current.setData([])
  }, [marketId, timeframe])

  // ── Helper: determine if user is currently near the real-time edge ──────────
  const isNearRealtimeEdge = useCallback((): boolean => {
    if (!chartRef.current) return true
    const range = chartRef.current.timeScale().getVisibleLogicalRange()
    if (!range) return true
    const currentTotal = totalBarsRef.current
    // In Lightweight Charts, the last data bar is at logical index (currentTotal - 1).
    // At the real-time edge, range.to extends past (currentTotal - 1) by rightOffset.
    // If range.to drops below (currentTotal - 2), the user has intentionally panned back into history.
    return range.to >= currentTotal - 2
  }, [])

  // Stable refs for callbacks to prevent subscription churn in WebSocket effect
  const onRefreshCandlesRef = useRef(onRefreshCandles)
  useEffect(() => {
    onRefreshCandlesRef.current = onRefreshCandles
  }, [onRefreshCandles])

  const isNearRealtimeEdgeRef = useRef(isNearRealtimeEdge)
  useEffect(() => {
    isNearRealtimeEdgeRef.current = isNearRealtimeEdge
  }, [isNearRealtimeEdge])

  // ── Historical Candles & Timeframe Updates (Authoritative REST sync) ────────
  useEffect(() => {
    if (!candleRef.current || !volumeRef.current || !areaRef.current) return

    if (candles.length === 0) {
      // If we already have an active live bar from WebSocket, keep it
      if (lastBarRef.current) {
        return
      }
      candleRef.current.setData([])
      areaRef.current.setData([])
      volumeRef.current.setData([])
      lastBarRef.current = null
      activeVolumeRef.current = 0
      totalBarsRef.current = 0
      return
    }

    const { bars, volumes } = sanitizeCandles(candles)
    if (bars.length === 0) {
      if (lastBarRef.current) {
        return
      }
      candleRef.current.setData([])
      areaRef.current.setData([])
      volumeRef.current.setData([])
      lastBarRef.current = null
      activeVolumeRef.current = 0
      totalBarsRef.current = 0
      return
    }

    // ── Phase 3 Reconciliation Tree: REST vs Live WebSocket State ───────────
    const currentBar = lastBarRef.current
    if (currentBar && bars.length > 0) {
      const lastIndex = bars.length - 1
      const restLast = bars[lastIndex]
      const restLastVol = volumes[lastIndex]?.value ?? 0

      if ((currentBar.time as number) === (restLast.time as number)) {
        // SAME BUCKET: Reconcile live state with authoritative REST baseline
        // 1. Open is always authoritative from REST (the bucket opening price)
        // 2. High is the maximum of REST high and live high
        // 3. Low is the minimum of REST low and live low
        // 4. Volume is max of REST volume and accumulated live volume
        // 5. Close is live close if live volume > REST volume (in-flight trades pending DB commit), else REST close
        const reconciledHigh = Math.max(restLast.high, currentBar.high)
        const reconciledLow = Math.min(restLast.low, currentBar.low)
        const reconciledVol = Math.max(restLastVol, activeVolumeRef.current)
        const reconciledClose = activeVolumeRef.current > restLastVol ? currentBar.close : restLast.close

        const mergedBar: SanitizedBar = {
          time: restLast.time,
          open: restLast.open,
          high: reconciledHigh,
          low: reconciledLow,
          close: reconciledClose,
        }

        bars[lastIndex] = mergedBar
        volumes[lastIndex] = {
          time: restLast.time,
          value: reconciledVol,
          color: mergedBar.close >= mergedBar.open ? 'rgba(16,185,129,0.45)' : 'rgba(239,68,68,0.45)',
        }
      } else if ((currentBar.time as number) > (restLast.time as number)) {
        // FUTURE BUCKET: WebSocket has already started a newer bucket not yet returned by REST.
        // Append current live bar so REST snapshot does not wipe out newer in-flight bucket.
        bars.push({ ...currentBar })
        volumes.push({
          time: currentBar.time,
          value: activeVolumeRef.current,
          color: currentBar.close >= currentBar.open ? 'rgba(16,185,129,0.45)' : 'rgba(239,68,68,0.45)',
        })
      }
      // If currentBar.time < restLast.time: REST has advanced past old live state, REST bars win completely.
    }

    const last = bars[bars.length - 1]
    const lastVol = volumes[volumes.length - 1]

    lastBarRef.current = { ...last }
    activeVolumeRef.current = lastVol ? lastVol.value : 0
    totalBarsRef.current = bars.length

    candleRef.current.setData(bars)
    areaRef.current.setData(bars.map((b) => ({ time: b.time, value: b.close })))
    volumeRef.current.setData(volumes)

    // Viewport positioning:
    // When history is dense (>= 45 bars), focus visible range on the most recent ~85 bars.
    // When history is sparse (< 45 bars), avoid forcing a narrow logical range that would
    // stretch bars across the full canvas width; instead preserve the configured target barSpacing
    // and anchor directly to the real-time edge.
    if (!hasFittedRef.current) {
      const total = bars.length
      if (total >= 45) {
        const visibleBars = Math.min(85, total)
        chartRef.current?.timeScale().setVisibleLogicalRange({
          from: total - visibleBars,
          to: total + 10,
        })
      } else {
        chartRef.current?.timeScale().scrollToRealTime()
      }
      hasFittedRef.current = true
    } else if (isNearRealtimeEdgeRef.current()) {
      // Follow the real-time edge only if user has not intentionally panned back in history
      chartRef.current?.timeScale().scrollToRealTime()
    }
  }, [candles, timeframe])

  // ── Real-Time Trades Stream (Kafka -> Gateway -> WebSocket) ────────────────
  useEffect(() => {
    let mounted = true
    const channel = WsChannels.trades(marketId)

    const unsubscribe = wsService.subscribe(channel, (tradeData: any) => {
      if (!mounted || !tradeData) return
      if (activeMarketRef.current !== marketId) return
      if (tradeData.marketId && tradeData.marketId !== marketId) return
      if (!candleRef.current || !volumeRef.current || !areaRef.current) return

      // Deduplicate trade IDs to prevent double counting on re-deliveries
      const tradeId = tradeData.tradeId || tradeData.trade_id || tradeData.id
      if (tradeId) {
        if (seenTradeIdsRef.current.has(tradeId)) return
        seenTradeIdsRef.current.add(tradeId)
        if (seenTradeIdsRef.current.size > 1000) {
          const first = seenTradeIdsRef.current.values().next().value
          if (first) seenTradeIdsRef.current.delete(first)
        }
      }

      const rawPrice = tradeData.price
      const rawQty = tradeData.quantity
      if (!rawPrice) return

      const price = parseFloat(rawPrice)
      const qty = parseFloat(rawQty) || 0
      if (isNaN(price) || price <= 0) return

      // Defensive timestamp parsing: handles both epoch ms number and RFC3339 string
      const rawTime = tradeData.executedAt ?? tradeData.executed_at
      const execMs = typeof rawTime === 'number'
        ? rawTime
        : (rawTime ? new Date(rawTime).getTime() : Date.now())
      if (isNaN(execMs) || execMs <= 0) return

      const tfSeconds = getTimeframeSeconds(timeframe)
      const tradeSeconds = Math.floor(execMs / 1000)
      const bucketTime = (Math.floor(tradeSeconds / tfSeconds) * tfSeconds) as UTCTimestamp

      const currentBar = lastBarRef.current

      if (!currentBar) {
        // First real trade ever in this market/timeframe!
        const newBar: SanitizedBar = {
          time: bucketTime,
          open: price,
          high: price,
          low: price,
          close: price,
        }
        activeVolumeRef.current = qty
        lastBarRef.current = newBar
        totalBarsRef.current = 1

        candleRef.current.update(newBar)
        areaRef.current.update({ time: bucketTime, value: price })
        volumeRef.current.update({
          time: bucketTime,
          value: qty,
          color: 'rgba(16,185,129,0.45)',
        })
        chartRef.current?.timeScale().scrollToRealTime()
        return
      }

      if (bucketTime === (currentBar.time as number)) {
        // Same timeframe bucket: update active candle with new high/low, latest close, and accumulated trade volume
        const updatedBar: SanitizedBar = {
          time: currentBar.time,
          open: currentBar.open,
          high: Math.max(currentBar.high, price),
          low: Math.min(currentBar.low, price),
          close: price,
        }
        activeVolumeRef.current += qty
        lastBarRef.current = updatedBar

        const volColor = price >= updatedBar.open ? 'rgba(16,185,129,0.45)' : 'rgba(239,68,68,0.45)'

        candleRef.current.update(updatedBar)
        areaRef.current.update({ time: updatedBar.time, value: price })
        volumeRef.current.update({
          time: updatedBar.time,
          value: activeVolumeRef.current,
          color: volColor,
        })
      } else if (bucketTime > (currentBar.time as number)) {
        // Timeframe boundary crossed: start new real candle bar
        const newBar: SanitizedBar = {
          time: bucketTime,
          open: price,
          high: price,
          low: price,
          close: price,
        }
        activeVolumeRef.current = qty
        lastBarRef.current = newBar
        totalBarsRef.current += 1

        candleRef.current.update(newBar)
        areaRef.current.update({ time: bucketTime, value: price })
        volumeRef.current.update({
          time: bucketTime,
          value: qty,
          color: 'rgba(16,185,129,0.45)',
        })
        if (isNearRealtimeEdgeRef.current()) {
          chartRef.current?.timeScale().scrollToRealTime()
        }
      } else {
        // bucketTime < currentBar.time
        // Out-of-order or late trade: don't fabricate anything. Trigger authoritative REST candles refresh!
        onRefreshCandlesRef.current?.()
      }
    })

    return () => {
      mounted = false
      unsubscribe()
    }
  }, [marketId, timeframe])

  // ── Secondary Toolbar Actions (Screenshot & Fullscreen) ─────────────────────
  const handleScreenshot = useCallback(() => {
    if (!chartRef.current) return
    try {
      const canvas = chartRef.current.takeScreenshot()
      const link = document.createElement('a')
      const sanitizedSymbol = symbol.replace(/[/\\:]/g, '-')
      link.download = `${sanitizedSymbol}_${timeframe}_chart.png`
      link.href = canvas.toDataURL('image/png')
      link.click()
    } catch (err) {
      console.warn('Failed to take chart screenshot:', err)
    }
  }, [symbol, timeframe])

  const handleFullscreen = useCallback(() => {
    const el = chartWrapperRef.current
    if (!el) return
    if (!document.fullscreenElement) {
      el.requestFullscreen?.().catch((err) => console.warn('Fullscreen error:', err))
    } else {
      document.exitFullscreen?.().catch((err) => console.warn('Exit fullscreen error:', err))
    }
  }, [])

  // ── Watermark label (market + timeframe) ──────────────────────────────────
  const tfLabel = timeframe.toUpperCase()

  return (
    <div ref={chartWrapperRef} className="h-full flex flex-col min-h-0 bg-[#0a0b0e]">
      {/* Chart Toolbar */}
      <ChartToolbar
        timeframe={timeframe}
        onTimeframeChange={onTimeframeChange}
        chartType={chartType}
        onChartTypeChange={setChartType}
        isDemoData={isDemoData}
        onScreenshot={handleScreenshot}
        onFullscreen={handleFullscreen}
      />

      {/* Watermark row */}
      <div className="flex items-center gap-2 px-3 py-1 border-b border-[#1e2530]/50 bg-[#0a0b0e] flex-shrink-0">
        <span className="text-[11px] text-slate-600 font-mono">
          {symbol} · {tfLabel} · TradeDrift
        </span>
        {lastPrice && lastPrice !== '0' && lastPrice !== '—' && (
          <span className="text-[11px] text-slate-500 font-mono">{lastPrice}</span>
        )}
      </div>

      {/* Chart area */}
      <div className="flex-1 relative min-h-0">
        {loading && (
          <div className="absolute inset-0 flex items-center justify-center bg-[#0a0b0e]/80 z-10">
            <div className="flex flex-col items-center gap-2">
              <div className="w-5 h-5 border-2 border-[#10b981] border-t-transparent rounded-full animate-spin" />
              <span className="text-xs text-slate-500">Loading chart...</span>
            </div>
          </div>
        )}
        <div
          ref={containerRef}
          className="w-full h-full"
          aria-label={`${symbol} candlestick chart`}
          role="img"
        />
      </div>
    </div>
  )
}

