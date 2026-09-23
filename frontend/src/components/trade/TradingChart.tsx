import { useEffect, useRef, useState } from 'react'
import {
  createChart,
  CandlestickSeries,
  HistogramSeries,
  type IChartApi,
  type ISeriesApi,
  type UTCTimestamp,
  type CandlestickSeriesPartialOptions,
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

const VOLUME_OPTIONS: HistogramSeriesPartialOptions = {
  color:            '#10b981',
  priceFormat:      { type: 'volume' },
  priceScaleId:     'volume',
  priceLineVisible: false,
  lastValueVisible: false,
}

function candlesToChartData(candles: Candle[]) {
  return candles
    .map((c) => ({
      time: (Math.floor(new Date(c.start_time).getTime() / 1000)) as UTCTimestamp,
      open:  parseFloat(c.open),
      high:  parseFloat(c.high),
      low:   parseFloat(c.low),
      close: parseFloat(c.close),
    }))
    .filter((c) => !isNaN(c.time) && !isNaN(c.open))
    .sort((a, b) => (a.time as number) - (b.time as number))
}

function candlesToVolumeData(candles: Candle[]) {
  return candles
    .map((c) => {
      const open  = parseFloat(c.open)
      const close = parseFloat(c.close)
      return {
        time:  (Math.floor(new Date(c.start_time).getTime() / 1000)) as UTCTimestamp,
        value: parseFloat(c.volume),
        color: close >= open ? 'rgba(16,185,129,0.45)' : 'rgba(239,68,68,0.45)',
      }
    })
    .filter((c) => !isNaN(c.time) && !isNaN(c.value))
    .sort((a, b) => (a.time as number) - (b.time as number))
}

export default function TradingChart({
  marketId: _marketId,
  symbol,
  lastPrice,
  candles,
  loading,
  isDemoData,
  timeframe,
  onTimeframeChange,
}: TradingChartProps) {
  const containerRef   = useRef<HTMLDivElement>(null)
  const chartRef       = useRef<IChartApi | null>(null)
  const candleRef      = useRef<ISeriesApi<'Candlestick'> | null>(null)
  const volumeRef      = useRef<ISeriesApi<'Histogram'> | null>(null)
  const [chartType, setChartType] = useState<'candlestick' | 'line'>('candlestick')

  // ── Create chart once ──────────────────────────────────────────────────────
  useEffect(() => {
    if (!containerRef.current) return
    const container = containerRef.current

    const chart = createChart(container, {
      width:  container.clientWidth,
      height: container.clientHeight,
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

    // Candlestick series
    const candleSeries = chart.addSeries(CandlestickSeries, CANDLE_OPTIONS)
    candleRef.current = candleSeries

    // Volume histogram — overlay in volume price scale (not a separate pane)
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
      volumeRef.current = null
    }
  }, []) // Create once — never re-run

  // ── Feed candle data when it changes ──────────────────────────────────────
  useEffect(() => {
    if (!candleRef.current || !volumeRef.current || candles.length === 0) return
    const candleData = candlesToChartData(candles)
    const volumeData = candlesToVolumeData(candles)
    candleRef.current.setData(candleData)
    volumeRef.current.setData(volumeData)
    chartRef.current?.timeScale().fitContent()
  }, [candles])

  // ── Watermark label (market + timeframe) ──────────────────────────────────
  const tfLabel = timeframe.toUpperCase()

  return (
    <div className="h-full flex flex-col min-h-0 bg-[#0a0b0e]">
      {/* Chart Toolbar */}
      <ChartToolbar
        timeframe={timeframe}
        onTimeframeChange={onTimeframeChange}
        chartType={chartType}
        onChartTypeChange={setChartType}
        isDemoData={isDemoData}
      />

      {/* Watermark row */}
      <div className="flex items-center gap-2 px-3 py-1 border-b border-[#1e2530]/50 bg-[#0a0b0e] flex-shrink-0">
        <span className="text-[11px] text-slate-600 font-mono">
          {symbol} · {tfLabel} · TradeDrift
        </span>
        {lastPrice && lastPrice !== '0' && lastPrice !== '—' && (
          <>
            <span className="text-[11px] text-slate-700 font-mono">C{lastPrice}</span>
          </>
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
