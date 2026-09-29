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
import type { Candle } from '../../types/trade'
import ChartToolbar from './ChartToolbar'
import type { Timeframe } from '../../types/trade'

interface TradingChartProps {
  marketId: string
  symbol: string           // e.g. "BTC/USDT"
  lastPrice: string
  candles: Candle[]
  loading: boolean
  isDemoData: boolean
  timeframe: Timeframe
  onTimeframeChange: (tf: Timeframe) => void
}

const CANDLE_OPTIONS: CandlestickSeriesPartialOptions = {
  upColor:          '#10b981',
  downColor:        '#ef4444',
  borderUpColor:    '#10b981',
  borderDownColor:  '#ef4444',
  wickUpColor:      '#10b981',
  wickDownColor:    '#ef4444',
  priceLineVisible: false,
  lastValueVisible: true,
}

const AREA_OPTIONS: AreaSeriesPartialOptions = {
  lineColor:        '#10b981',
  topColor:         'rgba(16, 185, 129, 0.28)',
  bottomColor:      'rgba(16, 185, 129, 0.01)',
  lineWidth:        2,
  priceLineVisible: false,
  lastValueVisible: true,
  visible:          false,
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
  isDemoData,
  timeframe,
  onTimeframeChange,
}: TradingChartProps) {
  const containerRef = useRef<HTMLDivElement>(null)
  const chartWrapperRef = useRef<HTMLDivElement>(null)
  const chartRef = useRef<IChartApi | null>(null)
  const candleRef = useRef<ISeriesApi<'Candlestick'> | null>(null)
  const areaRef = useRef<ISeriesApi<'Area'> | null>(null)
  const volumeRef = useRef<ISeriesApi<'Histogram'> | null>(null)

  const [chartType, setChartType] = useState<'candlestick' | 'line'>('candlestick')

  // Tracking refs for real-time live active candle and market safety
  const lastBarRef = useRef<SanitizedBar | null>(null)
  const activeMarketRef = useRef<string>(marketId)
  const hasFittedRef = useRef<boolean>(false)

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
        scaleMargins: { top: 0.05, bottom: 0.22 },
        autoScale:    true,
      },
      timeScale: {
        borderColor:    '#1e2530',
        timeVisible:    true,
        secondsVisible: false,
        fixLeftEdge:    false,
        fixRightEdge:   true,
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

    // 1. Candlestick series
    const candleSeries = chart.addSeries(CandlestickSeries, CANDLE_OPTIONS)
    candleRef.current = candleSeries

    // 2. Area (Line) series
    const areaSeries = chart.addSeries(AreaSeries, AREA_OPTIONS)
    areaRef.current = areaSeries

    // 3. Volume histogram
    const volumeSeries = chart.addSeries(HistogramSeries, {
      ...VOLUME_OPTIONS,
      priceScaleId: 'volume',
    })
    volumeSeries.priceScale().applyOptions({
      scaleMargins: { top: 0.80, bottom: 0 },
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

  // ── Chart type toggle (Candlestick vs Line) ─────────────────────────────────
  useEffect(() => {
    if (!candleRef.current || !areaRef.current) return
    const isCandle = chartType === 'candlestick'
    candleRef.current.applyOptions({ visible: isCandle })
    areaRef.current.applyOptions({ visible: !isCandle })
  }, [chartType])

  // ── Market Switching Isolation ──────────────────────────────────────────────
  useEffect(() => {
    activeMarketRef.current = marketId
    hasFittedRef.current = false
    lastBarRef.current = null

    // Immediately clear previous market's series
    if (candleRef.current) candleRef.current.setData([])
    if (areaRef.current) areaRef.current.setData([])
    if (volumeRef.current) volumeRef.current.setData([])
  }, [marketId])

  // ── Historical Candles & Timeframe Updates ──────────────────────────────────
  useEffect(() => {
    if (!candleRef.current || !volumeRef.current || !areaRef.current) return

    if (candles.length === 0) {
      candleRef.current.setData([])
      areaRef.current.setData([])
      volumeRef.current.setData([])
      lastBarRef.current = null
      return
    }

    const { bars, volumes } = sanitizeCandles(candles)
    if (bars.length === 0) {
      candleRef.current.setData([])
      areaRef.current.setData([])
      volumeRef.current.setData([])
      lastBarRef.current = null
      return
    }

    candleRef.current.setData(bars)
    areaRef.current.setData(bars.map((b) => ({ time: b.time, value: b.close })))
    volumeRef.current.setData(volumes)

    const last = bars[bars.length - 1]
    lastBarRef.current = { ...last }

    // Fit content on initial load or timeframe switch without disrupting background refreshes
    if (!hasFittedRef.current) {
      chartRef.current?.timeScale().fitContent()
      hasFittedRef.current = true
    }
  }, [candles, timeframe])

  // ── Real-Time Active Candle Updates ─────────────────────────────────────────
  useEffect(() => {
    if (!lastPrice || lastPrice === '0' || lastPrice === '—') return
    // Market Safety: verify current market matches
    if (activeMarketRef.current !== marketId) return
    if (!candleRef.current || !areaRef.current) return

    const price = parseFloat(lastPrice)
    if (isNaN(price) || price <= 0) return

    const currentBar = lastBarRef.current
    if (!currentBar) return

    const tfSeconds = getTimeframeSeconds(timeframe)
    const nowSeconds = Math.floor(Date.now() / 1000)
    const currentBucketTime = (Math.floor(nowSeconds / tfSeconds) * tfSeconds) as UTCTimestamp

    if (currentBucketTime > (currentBar.time as number)) {
      // New candle interval started
      const newBar: SanitizedBar = {
        time: currentBucketTime,
        open: price,
        high: price,
        low: price,
        close: price,
      }
      lastBarRef.current = newBar
      candleRef.current.update(newBar)
      areaRef.current.update({ time: newBar.time, value: newBar.close })
    } else {
      // Mutate active candle
      const updatedBar: SanitizedBar = {
        time: currentBar.time,
        open: currentBar.open,
        high: Math.max(currentBar.high, price),
        low: Math.min(currentBar.low, price),
        close: price,
      }
      lastBarRef.current = updatedBar
      candleRef.current.update(updatedBar)
      areaRef.current.update({ time: updatedBar.time, value: updatedBar.close })
    }
  }, [lastPrice, marketId, timeframe])

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

