import { useEffect, useRef, useState, useCallback, memo } from 'react'
import {
  createChart,
  CandlestickSeries,
  AreaSeries,
  HistogramSeries,
  LineSeries,
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
import toast from 'react-hot-toast'
import {
  calculateEMA,
  calculateBollingerBands,
  calculateIncrementalEMA,
  calculateIncrementalBollingerBands,
  type ActiveIndicators,
  DEFAULT_ACTIVE_INDICATORS,
} from '../../utils/indicators'
import {
  getTimeframeSeconds,
  sanitizeCandles,
  type SanitizedBar,
} from '../../utils/chartSanitizer'
import ChartScreenshotModal from './ChartScreenshotModal'

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
  crosshairMarkerVisible: true,
  crosshairMarkerRadius:  5,
  crosshairMarkerBackgroundColor: '#10b981',
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



function TradingChart({
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
  const tooltipRef = useRef<HTMLDivElement>(null)
  const chartRef = useRef<IChartApi | null>(null)
  const candleRef = useRef<ISeriesApi<'Candlestick'> | null>(null)
  const areaRef = useRef<ISeriesApi<'Area'> | null>(null)
  const volumeRef = useRef<ISeriesApi<'Histogram'> | null>(null)

  // Overlay indicator line series refs
  const ema20Ref = useRef<ISeriesApi<'Line'> | null>(null)
  const ema50Ref = useRef<ISeriesApi<'Line'> | null>(null)
  const ema200Ref = useRef<ISeriesApi<'Line'> | null>(null)
  const bbUpperRef = useRef<ISeriesApi<'Line'> | null>(null)
  const bbMiddleRef = useRef<ISeriesApi<'Line'> | null>(null)
  const bbLowerRef = useRef<ISeriesApi<'Line'> | null>(null)

  // Incremental EMA completed bar cache refs (value at bar N-2)
  const completedEma20Ref = useRef<number | null>(null)
  const completedEma50Ref = useRef<number | null>(null)
  const completedEma200Ref = useRef<number | null>(null)

  const [chartType, setChartType] = useState<'candlestick' | 'line'>('candlestick')
  const [activeIndicators, setActiveIndicators] = useState<ActiveIndicators>(DEFAULT_ACTIVE_INDICATORS)
  const [screenshotModal, setScreenshotModal] = useState<{
    dataUrl: string
    blob: Blob | null
    filename: string
  } | null>(null)

  const handleToggleIndicator = useCallback((key: keyof ActiveIndicators) => {
    setActiveIndicators((prev) => ({ ...prev, [key]: !prev[key] }))
  }, [])

  const activeIndicatorsRef = useRef<ActiveIndicators>(activeIndicators)
  useEffect(() => {
    activeIndicatorsRef.current = activeIndicators
  }, [activeIndicators])

  const symbolRef = useRef<string>(symbol)
  useEffect(() => {
    symbolRef.current = symbol
  }, [symbol])

  const chartTypeRef = useRef<'candlestick' | 'line'>(chartType)
  useEffect(() => {
    chartTypeRef.current = chartType
  }, [chartType])

  const tickSizeRef = useRef<string | undefined>(tickSize)
  useEffect(() => {
    tickSizeRef.current = tickSize
  }, [tickSize])

  // Tracking refs for real-time live active candle, volume, deduplication, and market safety
  const currentBarsRef = useRef<SanitizedBar[]>([])
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

    const { precision, minMove } = getPrecisionFromTickSize(tickSizeRef.current)
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

    // 4. Overlay Indicators LineSeries (Category A)
    // EMA 20 (Cyan)
    const ema20Series = chart.addSeries(LineSeries, {
      color: '#06b6d4',
      lineWidth: 2,
      priceLineVisible: false,
      lastValueVisible: false,
      crosshairMarkerVisible: true,
      crosshairMarkerRadius: 3,
      crosshairMarkerBackgroundColor: '#06b6d4',
      priceFormat,
    })
    ema20Ref.current = ema20Series

    // EMA 50 (Amber)
    const ema50Series = chart.addSeries(LineSeries, {
      color: '#f59e0b',
      lineWidth: 2,
      priceLineVisible: false,
      lastValueVisible: false,
      crosshairMarkerVisible: true,
      crosshairMarkerRadius: 3,
      crosshairMarkerBackgroundColor: '#f59e0b',
      priceFormat,
    })
    ema50Ref.current = ema50Series

    // EMA 200 (Purple)
    const ema200Series = chart.addSeries(LineSeries, {
      color: '#a855f7',
      lineWidth: 2,
      priceLineVisible: false,
      lastValueVisible: false,
      crosshairMarkerVisible: true,
      crosshairMarkerRadius: 3,
      crosshairMarkerBackgroundColor: '#a855f7',
      priceFormat,
    })
    ema200Ref.current = ema200Series

    // Bollinger Bands (Sky Blue)
    const bbUpperSeries = chart.addSeries(LineSeries, {
      color: 'rgba(56, 189, 248, 0.75)',
      lineWidth: 1,
      lineStyle: 2,
      priceLineVisible: false,
      lastValueVisible: false,
      crosshairMarkerVisible: false,
      priceFormat,
    })
    bbUpperRef.current = bbUpperSeries

    const bbMiddleSeries = chart.addSeries(LineSeries, {
      color: 'rgba(56, 189, 248, 0.5)',
      lineWidth: 1,
      lineStyle: 0,
      priceLineVisible: false,
      lastValueVisible: false,
      crosshairMarkerVisible: false,
      priceFormat,
    })
    bbMiddleRef.current = bbMiddleSeries

    const bbLowerSeries = chart.addSeries(LineSeries, {
      color: 'rgba(56, 189, 248, 0.75)',
      lineWidth: 1,
      lineStyle: 2,
      priceLineVisible: false,
      lastValueVisible: false,
      crosshairMarkerVisible: false,
      priceFormat,
    })
    bbLowerRef.current = bbLowerSeries

    // ── Interactive Hover Tooltip ──────────────────────────────────────────
    chart.subscribeCrosshairMove((param) => {
      const tooltip = tooltipRef.current
      if (!tooltip || !containerRef.current) return

      if (
        !param.point ||
        !param.time ||
        param.point.x < 0 ||
        param.point.x > container.clientWidth ||
        param.point.y < 0 ||
        param.point.y > container.clientHeight
      ) {
        tooltip.style.display = 'none'
        return
      }

      const currentChartType = chartTypeRef.current
      const currentSymbol = symbolRef.current
      const currentTickSize = tickSizeRef.current
      const { precision } = getPrecisionFromTickSize(currentTickSize)

      const formatVal = (val: number) =>
        val.toLocaleString('en-US', {
          minimumFractionDigits: precision,
          maximumFractionDigits: precision,
        })

      const formatVol = (val: number) => {
        if (val >= 1_000_000) return `${(val / 1_000_000).toFixed(2)}M`
        if (val >= 1_000) return `${(val / 1_000).toFixed(2)}K`
        return val.toLocaleString('en-US', { maximumFractionDigits: 2 })
      }

      const dateObj = new Date((param.time as number) * 1000)
      const dateStr = dateObj.toLocaleDateString(undefined, {
        month: 'short',
        day: 'numeric',
        hour: '2-digit',
        minute: '2-digit',
      })

      // Extract active overlay indicator values
      const activeInd = activeIndicatorsRef.current
      const indRows: string[] = []

      if (activeInd.ema20 && ema20Ref.current) {
        const d = param.seriesData.get(ema20Ref.current) as { value: number } | undefined
        if (d && typeof d.value === 'number') {
          indRows.push(`<div class="flex justify-between items-center text-[#06b6d4]"><span>EMA 20</span><span>${formatVal(d.value)}</span></div>`)
        }
      }
      if (activeInd.ema50 && ema50Ref.current) {
        const d = param.seriesData.get(ema50Ref.current) as { value: number } | undefined
        if (d && typeof d.value === 'number') {
          indRows.push(`<div class="flex justify-between items-center text-[#f59e0b]"><span>EMA 50</span><span>${formatVal(d.value)}</span></div>`)
        }
      }
      if (activeInd.ema200 && ema200Ref.current) {
        const d = param.seriesData.get(ema200Ref.current) as { value: number } | undefined
        if (d && typeof d.value === 'number') {
          indRows.push(`<div class="flex justify-between items-center text-[#a855f7]"><span>EMA 200</span><span>${formatVal(d.value)}</span></div>`)
        }
      }
      if (activeInd.bollinger && bbUpperRef.current && bbLowerRef.current) {
        const u = param.seriesData.get(bbUpperRef.current) as { value: number } | undefined
        const m = bbMiddleRef.current ? (param.seriesData.get(bbMiddleRef.current) as { value: number } | undefined) : undefined
        const l = param.seriesData.get(bbLowerRef.current) as { value: number } | undefined
        if (u && l && typeof u.value === 'number' && typeof l.value === 'number') {
          indRows.push(`<div class="flex justify-between items-center text-[#38bdf8]"><span>BB Upper</span><span>${formatVal(u.value)}</span></div>`)
          if (m && typeof m.value === 'number') {
            indRows.push(`<div class="flex justify-between items-center text-[#38bdf8]/70"><span>BB Mid</span><span>${formatVal(m.value)}</span></div>`)
          }
          indRows.push(`<div class="flex justify-between items-center text-[#38bdf8]"><span>BB Lower</span><span>${formatVal(l.value)}</span></div>`)
        }
      }

      const indicatorHtml = indRows.length > 0
        ? `<div class="mt-1 pt-1 border-t border-[#292C30]/50 space-y-0.5 text-[10px] font-mono">${indRows.join('')}</div>`
        : ''

      if (currentChartType === 'candlestick') {
        const candleData = param.seriesData.get(candleSeries) as
          | { open: number; high: number; low: number; close: number }
          | undefined

        if (!candleData || typeof candleData.close !== 'number') {
          tooltip.style.display = 'none'
          return
        }

        const isBullish = candleData.close >= candleData.open
        const diff = candleData.close - candleData.open
        const pctChange = candleData.open > 0 ? ((diff / candleData.open) * 100).toFixed(2) : '0.00'
        const pctChangeSign = diff >= 0 ? '+' : ''
        const deltaColor = isBullish ? 'text-[#10b981]' : 'text-[#ef4444]'
        const badgeBg = isBullish ? 'bg-[#10b981]' : 'bg-[#ef4444]'

        const volumeData = param.seriesData.get(volumeSeries) as { value: number } | undefined

        tooltip.innerHTML = `
          <div class="flex items-center justify-between gap-3 mb-1.5 pb-1 border-b border-[#292C30]">
            <div class="flex items-center gap-1.5">
              <span class="w-1.5 h-1.5 rounded-full ${badgeBg}"></span>
              <span class="text-xs font-semibold text-[#f5f7fa] font-mono">${currentSymbol}</span>
            </div>
            <span class="text-[11px] font-mono font-medium ${deltaColor}">
              ${pctChangeSign}${pctChange}%
            </span>
          </div>
          <div class="grid grid-cols-2 gap-x-3 gap-y-0.5 text-[11px] font-mono text-slate-300">
            <div><span class="text-slate-500 text-[10px]">O:</span> ${formatVal(candleData.open)}</div>
            <div><span class="text-slate-500 text-[10px]">H:</span> ${formatVal(candleData.high)}</div>
            <div><span class="text-slate-500 text-[10px]">L:</span> ${formatVal(candleData.low)}</div>
            <div><span class="text-slate-500 text-[10px]">C:</span> ${formatVal(candleData.close)}</div>
          </div>
          ${
            volumeData && typeof volumeData.value === 'number'
              ? `<div class="mt-1 text-[10px] font-mono text-slate-400 border-t border-[#292C30]/50 pt-0.5 flex justify-between"><span>Vol:</span> <span class="text-slate-300">${formatVol(volumeData.value)}</span></div>`
              : ''
          }
          ${indicatorHtml}
          <div class="text-[9px] text-slate-500 mt-1">${dateStr}</div>
        `
      } else {
        // Line / Area mode
        const areaData = param.seriesData.get(areaSeries) as { value: number } | undefined

        if (!areaData || typeof areaData.value !== 'number') {
          tooltip.style.display = 'none'
          return
        }

        const quoteAsset = currentSymbol.split('/')[1] || 'USDT'

        tooltip.innerHTML = `
          <div class="flex items-center justify-between gap-3 mb-1 pb-1 border-b border-[#292C30]">
            <div class="flex items-center gap-1.5">
              <span class="w-2 h-2 rounded-full bg-[#10b981] inline-block shadow-[0_0_6px_rgba(16,185,129,0.5)]"></span>
              <span class="text-xs font-semibold text-[#f5f7fa] font-mono">${currentSymbol}</span>
            </div>
          </div>
          <div class="text-xs font-mono font-bold text-[#f5f7fa] mb-0.5">
            ${formatVal(areaData.value)} <span class="text-[10px] text-slate-400 font-normal">${quoteAsset}</span>
          </div>
          ${indicatorHtml}
          <div class="text-[10px] text-slate-500">${dateStr}</div>
        `
      }

      tooltip.style.display = 'block'

      const tooltipWidth = indRows.length > 0 ? (currentChartType === 'candlestick' ? 190 : 170) : (currentChartType === 'candlestick' ? 180 : 150)
      const baseHeight = currentChartType === 'candlestick' ? 115 : 65
      const tooltipHeight = baseHeight + (indRows.length * 15)

      let left = param.point.x + 15
      let top = param.point.y - 20

      if (left + tooltipWidth > container.clientWidth) {
        left = param.point.x - tooltipWidth - 15
      }
      if (left < 10) {
        left = 10
      }

      if (top < 10) {
        top = 10
      } else if (top + tooltipHeight > container.clientHeight) {
        top = container.clientHeight - tooltipHeight - 10
      }

      tooltip.style.left = `${left}px`
      tooltip.style.top = `${top}px`
    })

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

    const tooltipEl = tooltipRef.current
    return () => {
      resizeObserver.disconnect()
      chart.remove()
      chartRef.current    = null
      candleRef.current   = null
      areaRef.current     = null
      volumeRef.current   = null
      ema20Ref.current    = null
      ema50Ref.current    = null
      ema200Ref.current   = null
      bbUpperRef.current  = null
      bbMiddleRef.current = null
      bbLowerRef.current  = null
      if (tooltipEl) {
        tooltipEl.style.display = 'none'
      }
    }
  }, []) // Create once — never re-run

  // ── Dynamic Price Precision updates on tickSize change ──────────────────────
  useEffect(() => {
    const { precision, minMove } = getPrecisionFromTickSize(tickSize)
    const priceFormat = { type: 'price' as const, precision, minMove }
    candleRef.current?.applyOptions({ priceFormat })
    areaRef.current?.applyOptions({ priceFormat })
    ema20Ref.current?.applyOptions({ priceFormat })
    ema50Ref.current?.applyOptions({ priceFormat })
    ema200Ref.current?.applyOptions({ priceFormat })
    bbUpperRef.current?.applyOptions({ priceFormat })
    bbMiddleRef.current?.applyOptions({ priceFormat })
    bbLowerRef.current?.applyOptions({ priceFormat })
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
    currentBarsRef.current = []

    if (tooltipRef.current) {
      tooltipRef.current.style.display = 'none'
    }

    // Immediately clear previous market's or timeframe's series
    if (candleRef.current) candleRef.current.setData([])
    if (areaRef.current) areaRef.current.setData([])
    if (volumeRef.current) volumeRef.current.setData([])
    if (ema20Ref.current) ema20Ref.current.setData([])
    if (ema50Ref.current) ema50Ref.current.setData([])
    if (ema200Ref.current) ema200Ref.current.setData([])
    if (bbUpperRef.current) bbUpperRef.current.setData([])
    if (bbMiddleRef.current) bbMiddleRef.current.setData([])
    if (bbLowerRef.current) bbLowerRef.current.setData([])
    completedEma20Ref.current = null
    completedEma50Ref.current = null
    completedEma200Ref.current = null
  }, [marketId, timeframe])

  // ── Indicator Calculation & Series Updater ─────────────────────────────────
  const updateIndicatorSeries = useCallback((bars: SanitizedBar[], indicators: ActiveIndicators) => {
    const hasAnyActive = Boolean(indicators.ema20 || indicators.ema50 || indicators.ema200 || indicators.bollinger)

    if (!bars || bars.length === 0 || !hasAnyActive) {
      ema20Ref.current?.setData([])
      ema50Ref.current?.setData([])
      ema200Ref.current?.setData([])
      bbUpperRef.current?.setData([])
      bbMiddleRef.current?.setData([])
      bbLowerRef.current?.setData([])
      completedEma20Ref.current = null
      completedEma50Ref.current = null
      completedEma200Ref.current = null
      return
    }

    const closeData = bars.map((b) => ({ time: b.time, close: b.close }))

    // EMA 20
    if (indicators.ema20) {
      const data = calculateEMA(closeData, 20)
      ema20Ref.current?.setData(data)
      completedEma20Ref.current = data.length >= 2 ? data[data.length - 2].value : null
    } else {
      ema20Ref.current?.setData([])
      completedEma20Ref.current = null
    }

    // EMA 50
    if (indicators.ema50) {
      const data = calculateEMA(closeData, 50)
      ema50Ref.current?.setData(data)
      completedEma50Ref.current = data.length >= 2 ? data[data.length - 2].value : null
    } else {
      ema50Ref.current?.setData([])
      completedEma50Ref.current = null
    }

    // EMA 200
    if (indicators.ema200) {
      const data = calculateEMA(closeData, 200)
      ema200Ref.current?.setData(data)
      completedEma200Ref.current = data.length >= 2 ? data[data.length - 2].value : null
    } else {
      ema200Ref.current?.setData([])
      completedEma200Ref.current = null
    }

    // Bollinger Bands (20, 2)
    if (indicators.bollinger) {
      const { upper, middle, lower } = calculateBollingerBands(closeData, 20, 2)
      bbUpperRef.current?.setData(upper)
      bbMiddleRef.current?.setData(middle)
      bbLowerRef.current?.setData(lower)
    } else {
      bbUpperRef.current?.setData([])
      bbMiddleRef.current?.setData([])
      bbLowerRef.current?.setData([])
    }
  }, [])

  // Sync indicators whenever toggles change
  useEffect(() => {
    updateIndicatorSeries(currentBarsRef.current, activeIndicators)
  }, [activeIndicators, updateIndicatorSeries])

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
    currentBarsRef.current = bars

    candleRef.current.setData(bars)
    areaRef.current.setData(bars.map((b) => ({ time: b.time, value: b.close })))
    volumeRef.current.setData(volumes)
    updateIndicatorSeries(bars, activeIndicatorsRef.current)

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
  }, [candles, timeframe, updateIndicatorSeries])

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
        currentBarsRef.current = [newBar]

        candleRef.current.update(newBar)
        areaRef.current.update({ time: bucketTime, value: price })
        volumeRef.current.update({
          time: bucketTime,
          value: qty,
          color: 'rgba(16,185,129,0.45)',
        })
        updateIndicatorSeries(currentBarsRef.current, activeIndicatorsRef.current)
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

        if (currentBarsRef.current.length > 0) {
          currentBarsRef.current[currentBarsRef.current.length - 1] = updatedBar
        } else {
          currentBarsRef.current = [updatedBar]
        }

        const volColor = price >= updatedBar.open ? 'rgba(16,185,129,0.45)' : 'rgba(239,68,68,0.45)'

        candleRef.current.update(updatedBar)
        areaRef.current.update({ time: updatedBar.time, value: price })
        volumeRef.current.update({
          time: updatedBar.time,
          value: activeVolumeRef.current,
          color: volColor,
        })

        // Incremental O(1) in-bar indicator updates without full array recalculation or .setData()
        const ind = activeIndicatorsRef.current
        if (ind.ema20 && ema20Ref.current && completedEma20Ref.current !== null) {
          const pt = calculateIncrementalEMA(completedEma20Ref.current, price, 20, updatedBar.time)
          ema20Ref.current.update(pt)
        }
        if (ind.ema50 && ema50Ref.current && completedEma50Ref.current !== null) {
          const pt = calculateIncrementalEMA(completedEma50Ref.current, price, 50, updatedBar.time)
          ema50Ref.current.update(pt)
        }
        if (ind.ema200 && ema200Ref.current && completedEma200Ref.current !== null) {
          const pt = calculateIncrementalEMA(completedEma200Ref.current, price, 200, updatedBar.time)
          ema200Ref.current.update(pt)
        }
        if (ind.bollinger && bbUpperRef.current && bbMiddleRef.current && bbLowerRef.current) {
          const bars = currentBarsRef.current
          if (bars.length >= 20) {
            const bands = calculateIncrementalBollingerBands(
              bars.slice(0, -1),
              price,
              20,
              2,
              updatedBar.time
            )
            if (bands) {
              bbUpperRef.current.update(bands.upper)
              bbMiddleRef.current.update(bands.middle)
              bbLowerRef.current.update(bands.lower)
            }
          }
        }
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
        currentBarsRef.current.push(newBar)

        candleRef.current.update(newBar)
        areaRef.current.update({ time: bucketTime, value: price })
        volumeRef.current.update({
          time: bucketTime,
          value: qty,
          color: 'rgba(16,185,129,0.45)',
        })

        updateIndicatorSeries(currentBarsRef.current, activeIndicatorsRef.current)
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
  }, [marketId, timeframe, updateIndicatorSeries])

  // ── Secondary Toolbar Actions (Screenshot & Fullscreen) ─────────────────────
  const handleScreenshot = useCallback(() => {
    const container = containerRef.current
    if (!container) return

    try {
      // Find all canvas elements rendered inside the Lightweight Charts container
      const canvases = Array.from(container.querySelectorAll('canvas'))
      if (canvases.length === 0) {
        toast.error('No chart canvas found to capture')
        return
      }

      const containerRect = container.getBoundingClientRect()
      const width = container.clientWidth || containerRect.width
      const height = container.clientHeight || containerRect.height

      if (width <= 0 || height <= 0) {
        toast.error('Chart area has zero dimensions')
        return
      }

      const dpr = window.devicePixelRatio || 1
      const exportCanvas = document.createElement('canvas')
      exportCanvas.width = width * dpr
      exportCanvas.height = height * dpr

      const ctx = exportCanvas.getContext('2d')
      if (!ctx) {
        toast.error('Failed to create screenshot context')
        return
      }

      ctx.scale(dpr, dpr)

      // 1. Draw solid dark chart background (#0a0b0e) so the export is opaque
      ctx.fillStyle = '#0a0b0e'
      ctx.fillRect(0, 0, width, height)

      // 2. Composite all layered canvases in their exact DOM order and positions
      for (const canvas of canvases) {
        const rect = canvas.getBoundingClientRect()
        const x = rect.left - containerRect.left
        const y = rect.top - containerRect.top
        const w = rect.width
        const h = rect.height

        if (w > 0 && h > 0) {
          ctx.drawImage(canvas, x, y, w, h)
        }
      }

      // 3. Add TradeDrift watermark branding to the saved screenshot
      ctx.font = 'bold 12px "JetBrains Mono", monospace'
      ctx.fillStyle = 'rgba(255, 255, 255, 0.4)'
      ctx.fillText(`${symbol} · ${timeframe.toUpperCase()} · TradeDrift`, 16, 22)

      // 4. Open TradingView-style Screenshot Preview & Export Modal
      const sanitizedSymbol = (symbol || 'CHART').replace(/[/\\:]/g, '-')
      const filename = `${sanitizedSymbol}_${timeframe}_chart.png`
      const dataUrl = exportCanvas.toDataURL('image/png')

      exportCanvas.toBlob((blob) => {
        setScreenshotModal({
          dataUrl,
          blob,
          filename,
        })
      }, 'image/png')
    } catch (err) {
      console.error('Failed to take chart screenshot:', err)
      toast.error('Failed to capture chart screenshot')
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
        activeIndicators={activeIndicators}
        onToggleIndicator={handleToggleIndicator}
        onScreenshot={handleScreenshot}
        onFullscreen={handleFullscreen}
      />

      {/* Watermark row */}
      <div className="flex items-center gap-2 px-3 py-1 border-b border-[#1e2530]/50 bg-[#0a0b0e] flex-shrink-0 flex-wrap">
        <span className="text-[11px] text-slate-600 font-mono">
          {symbol} · {tfLabel} · TradeDrift
        </span>
        {lastPrice && lastPrice !== '0' && lastPrice !== '—' && (
          <span className="text-[11px] text-slate-500 font-mono">{lastPrice}</span>
        )}

        {/* Active Indicators Legend */}
        {activeIndicators.ema20 && (
          <span className="inline-flex items-center gap-1 text-[10px] font-mono px-1.5 py-0.5 rounded bg-cyan-500/10 text-cyan-400 border border-cyan-500/25">
            <span className="w-1.5 h-1.5 rounded-full bg-[#06b6d4]" />
            EMA 20
          </span>
        )}
        {activeIndicators.ema50 && (
          <span className="inline-flex items-center gap-1 text-[10px] font-mono px-1.5 py-0.5 rounded bg-amber-500/10 text-amber-400 border border-amber-500/25">
            <span className="w-1.5 h-1.5 rounded-full bg-[#f59e0b]" />
            EMA 50
          </span>
        )}
        {activeIndicators.ema200 && (
          <span className="inline-flex items-center gap-1 text-[10px] font-mono px-1.5 py-0.5 rounded bg-purple-500/10 text-purple-400 border border-purple-500/25">
            <span className="w-1.5 h-1.5 rounded-full bg-[#a855f7]" />
            EMA 200
          </span>
        )}
        {activeIndicators.bollinger && (
          <span className="inline-flex items-center gap-1 text-[10px] font-mono px-1.5 py-0.5 rounded bg-sky-500/10 text-sky-400 border border-sky-500/25">
            <span className="w-1.5 h-1.5 rounded-full bg-[#38bdf8]" />
            BB (20, 2)
          </span>
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

        {/* Interactive hover tooltip */}
        <div
          ref={tooltipRef}
          className="absolute z-20 pointer-events-none hidden bg-[#161B22]/95 backdrop-blur-sm border border-[#292C30] rounded-lg px-2.5 py-2 shadow-[0_4px_16px_rgba(0,0,0,0.6)]"
          style={{ transition: 'left 0.05s ease-out, top 0.05s ease-out' }}
        />

        <div
          ref={containerRef}
          className="w-full h-full"
          aria-label={`${symbol} candlestick chart`}
          role="img"
        />
      </div>

      {/* Screenshot Preview & Export Modal */}
      {screenshotModal && (
        <ChartScreenshotModal
          isOpen={true}
          onClose={() => setScreenshotModal(null)}
          dataUrl={screenshotModal.dataUrl}
          blob={screenshotModal.blob}
          filename={screenshotModal.filename}
          symbol={symbol}
          timeframe={timeframe}
        />
      )}
    </div>
  )
}

export default memo(TradingChart)

