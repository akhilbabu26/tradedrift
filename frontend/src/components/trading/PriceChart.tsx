import { useEffect, useRef, useState } from 'react'
import {
  createChart,
  CandlestickSeries,
  HistogramSeries,
  LineSeries,
  type IChartApi,
  type ISeriesApi,
  type CandlestickData,
  type HistogramData,
  type LineData,
  type UTCTimestamp,
  ColorType,
  CrosshairMode,
} from 'lightweight-charts'
import {
  Maximize2,
  Camera,
  Activity,
  Sliders,
  Crosshair,
  TrendingUp,
  Type,
  Smile,
  Ruler,
  Magnet,
  Lock,
  Trash2,
} from 'lucide-react'
import type { Ticker24h } from '../../api/market'
import { marketApi } from '../../api/market'

interface PriceChartProps {
  selectedMarketId: string
  ticker: Ticker24h | null
}

interface HoverBarData {
  time: string
  open: number
  high: number
  low: number
  close: number
  change: number
  changePercent: number
  volume: number
  ma7?: number
  ma25?: number
  ma99?: number
  isUp: boolean
}

const TIMEFRAMES = ['1m', '5m', '15m', '1h', '4h', '1D']

// Helper to safely parse strings with commas
const cleanNumber = (val: string | number | undefined | null, fallback: number): number => {
  if (typeof val === 'number') return isNaN(val) ? fallback : val
  if (!val) return fallback
  const clean = String(val).replace(/,/g, '').trim()
  const num = parseFloat(clean)
  return isNaN(num) ? fallback : num
}

export default function PriceChart({ selectedMarketId, ticker }: PriceChartProps) {
  const chartContainerRef = useRef<HTMLDivElement>(null)
  const chartRef = useRef<IChartApi | null>(null)
  const candleSeriesRef = useRef<ISeriesApi<'Candlestick'> | null>(null)
  const volumeSeriesRef = useRef<ISeriesApi<'Histogram'> | null>(null)
  const ma7SeriesRef = useRef<ISeriesApi<'Line'> | null>(null)
  const ma25SeriesRef = useRef<ISeriesApi<'Line'> | null>(null)
  const ma99SeriesRef = useRef<ISeriesApi<'Line'> | null>(null)

  const [activeTimeframe, setActiveTimeframe] = useState('1h')
  const [activeTool, setActiveTool] = useState('crosshair')
  const [hoverData, setHoverData] = useState<HoverBarData | null>(null)

  const basePriceNum = cleanNumber(
    ticker?.last_price,
    selectedMarketId === 'BTC-USDT' ? 96450.00 : selectedMarketId === 'ETH-USDT' ? 2780.50 : 188.20
  )

  const priceRef = useRef(basePriceNum)
  priceRef.current = basePriceNum

  // Keep latest bar state to display when crosshair leaves
  const latestBarRef = useRef<HoverBarData | null>(null)

  useEffect(() => {
    if (!chartContainerRef.current) return

    let isDisposed = false
    const container = chartContainerRef.current

    // Clean up previous chart instance
    if (chartRef.current) {
      chartRef.current.remove()
      chartRef.current = null
    }

    const chart = createChart(container, {
      autoSize: true,
      layout: {
        background: { type: ColorType.Solid, color: '#07090e' },
        textColor: '#64748b',
        fontFamily: 'JetBrains Mono, -apple-system, BlinkMacSystemFont, monospace',
        fontSize: 11,
      },
      grid: {
        vertLines: { color: '#1b2230', style: 1 },
        horzLines: { color: '#1b2230', style: 1 },
      },
      crosshair: {
        mode: CrosshairMode.Normal,
        vertLine: {
          color: '#00e5ff',
          width: 1,
          style: 2,
          labelBackgroundColor: '#141a26',
        },
        horzLine: {
          color: '#00e5ff',
          width: 1,
          style: 2,
          labelBackgroundColor: '#141a26',
        },
      },
      rightPriceScale: {
        borderColor: '#1b2230',
        scaleMargins: { top: 0.08, bottom: 0.22 },
        alignLabels: true,
      },
      timeScale: {
        borderColor: '#1b2230',
        timeVisible: true,
        secondsVisible: false,
        barSpacing: 10,
        minBarSpacing: 4,
      },
    })

    // Candlestick Series
    const candleSeries = chart.addSeries(CandlestickSeries, {
      upColor: '#00e676',
      downColor: '#ff3366',
      borderUpColor: '#00e676',
      borderDownColor: '#ff3366',
      wickUpColor: '#00e676',
      wickDownColor: '#ff3366',
    })

    // Volume Histogram Series
    const volumeSeries = chart.addSeries(HistogramSeries, {
      priceFormat: { type: 'volume' },
      priceScaleId: 'volume',
    })
    chart.priceScale('volume').applyOptions({
      scaleMargins: { top: 0.8, bottom: 0 },
    })

    // Moving Averages Series (MA 7: Orange, MA 25: Cyan, MA 99: Purple)
    const ma7Series = chart.addSeries(LineSeries, {
      color: '#f59e0b',
      lineWidth: 1,
      priceLineVisible: false,
      crosshairMarkerVisible: false,
    })
    const ma25Series = chart.addSeries(LineSeries, {
      color: '#00e5ff',
      lineWidth: 1,
      priceLineVisible: false,
      crosshairMarkerVisible: false,
    })
    const ma99Series = chart.addSeries(LineSeries, {
      color: '#a855f7',
      lineWidth: 1,
      priceLineVisible: false,
      crosshairMarkerVisible: false,
    })

    chartRef.current = chart
    candleSeriesRef.current = candleSeries
    volumeSeriesRef.current = volumeSeries
    ma7SeriesRef.current = ma7Series
    ma25SeriesRef.current = ma25Series
    ma99SeriesRef.current = ma99Series

    // Helper to calculate moving averages
    const computeMAs = (candles: CandlestickData[]) => {
      const ma7: LineData[] = []
      const ma25: LineData[] = []
      const ma99: LineData[] = []
      const closes: number[] = []

      for (const c of candles) {
        closes.push(c.close)
        if (closes.length >= 7) {
          const avg = closes.slice(-7).reduce((a, b) => a + b, 0) / 7
          ma7.push({ time: c.time, value: avg })
        }
        if (closes.length >= 25) {
          const avg = closes.slice(-25).reduce((a, b) => a + b, 0) / 25
          ma25.push({ time: c.time, value: avg })
        }
        if (closes.length >= 99) {
          const avg = closes.slice(-99).reduce((a, b) => a + b, 0) / 99
          ma99.push({ time: c.time, value: avg })
        }
      }
      return { ma7, ma25, ma99 }
    }

    // Generate initial realistic candle data (strictly ascending time)
    const basePrice = Math.max(0.01, priceRef.current)
    const candleData: CandlestickData[] = []
    const volumeData: HistogramData[] = []

    const now = Math.floor(Date.now() / 1000)
    const intervalSeconds =
      activeTimeframe === '1m'
        ? 60
        : activeTimeframe === '5m'
        ? 300
        : activeTimeframe === '15m'
        ? 900
        : activeTimeframe === '1h'
        ? 3600
        : activeTimeframe === '4h'
        ? 14400
        : 86400

    let currentClose = basePrice * 0.96

    for (let i = 120; i >= 0; i--) {
      const time = (now - i * intervalSeconds) as UTCTimestamp
      const delta = (Math.random() - 0.48) * (basePrice * 0.007)
      const open = currentClose
      const close = Math.max(0.0001, open + delta)
      const high = Math.max(open, close) + Math.random() * (basePrice * 0.0035)
      const low = Math.max(0.0001, Math.min(open, close) - Math.random() * (basePrice * 0.0035))
      currentClose = close

      candleData.push({ time, open, high, low, close })
      const volVal = Math.floor(Math.random() * 500 + 80)
      volumeData.push({
        time,
        value: volVal,
        color: close >= open ? 'rgba(0, 230, 118, 0.35)' : 'rgba(255, 51, 102, 0.35)',
      })
    }

    const { ma7: ma7Data, ma25: ma25Data, ma99: ma99Data } = computeMAs(candleData)

    const lastCandle = candleData[candleData.length - 1]
    const lastVol = volumeData[volumeData.length - 1]

    const defaultHover: HoverBarData = {
      time: new Date((lastCandle.time as number) * 1000).toLocaleString('en-US', {
        month: 'short',
        day: 'numeric',
        hour: '2-digit',
        minute: '2-digit',
        hour12: false,
      }),
      open: lastCandle.open,
      high: lastCandle.high,
      low: lastCandle.low,
      close: lastCandle.close,
      change: lastCandle.close - lastCandle.open,
      changePercent: ((lastCandle.close - lastCandle.open) / lastCandle.open) * 100,
      volume: lastVol.value,
      ma7: ma7Data[ma7Data.length - 1]?.value,
      ma25: ma25Data[ma25Data.length - 1]?.value,
      ma99: ma99Data[ma99Data.length - 1]?.value,
      isUp: lastCandle.close >= lastCandle.open,
    }

    latestBarRef.current = defaultHover
    setHoverData(defaultHover)

    // Set initial chart data
    candleSeries.setData(candleData)
    volumeSeries.setData(volumeData)
    ma7Series.setData(ma7Data)
    ma25Series.setData(ma25Data)
    ma99Series.setData(ma99Data)

    // Resting Sell Limit Order overlay line (+1.5%)
    candleSeries.createPriceLine({
      price: basePrice * 1.015,
      color: '#ff3366',
      lineWidth: 1,
      lineStyle: 2,
      axisLabelVisible: true,
      title: `SELL LIMIT ${selectedMarketId.startsWith('SOL') ? '15.00 SOL' : selectedMarketId.startsWith('ETH') ? '2.50 ETH' : '0.25 BTC'}`,
    })

    // Resting Buy Limit Order overlay line (-2%)
    candleSeries.createPriceLine({
      price: basePrice * 0.98,
      color: '#00e676',
      lineWidth: 1,
      lineStyle: 2,
      axisLabelVisible: true,
      title: `BUY LIMIT ${selectedMarketId.startsWith('SOL') ? '15.00 SOL' : selectedMarketId.startsWith('ETH') ? '2.50 ETH' : '0.25 BTC'}`,
    })

    // Try fetching real API candles if backend is available (must be sorted ASC)
    const apiResolution = activeTimeframe.toLowerCase() === '4h' ? '1h' : activeTimeframe.toLowerCase()
    marketApi
      .getCandles(selectedMarketId, apiResolution, 120)
      .then((apiCandles) => {
        if (isDisposed || !candleSeriesRef.current || !volumeSeriesRef.current) return
        if (apiCandles && apiCandles.length > 0) {
          const apiCandleData: CandlestickData[] = apiCandles
            .map((c) => ({
              time: (new Date(c.start_time).getTime() / 1000) as UTCTimestamp,
              open: parseFloat(c.open),
              high: parseFloat(c.high),
              low: parseFloat(c.low),
              close: parseFloat(c.close),
            }))
            .sort((a, b) => (a.time as number) - (b.time as number))

          const apiVolData: HistogramData[] = apiCandles
            .map((c) => ({
              time: (new Date(c.start_time).getTime() / 1000) as UTCTimestamp,
              value: parseFloat(c.volume),
              color: parseFloat(c.close) >= parseFloat(c.open) ? 'rgba(0, 230, 118, 0.35)' : 'rgba(255, 51, 102, 0.35)',
            }))
            .sort((a, b) => (a.time as number) - (b.time as number))

          candleSeries.setData(apiCandleData)
          volumeSeries.setData(apiVolData)

          const { ma7: realMA7, ma25: realMA25, ma99: realMA99 } = computeMAs(apiCandleData)
          if (ma7SeriesRef.current) ma7SeriesRef.current.setData(realMA7)
          if (ma25SeriesRef.current) ma25SeriesRef.current.setData(realMA25)
          if (ma99SeriesRef.current) ma99SeriesRef.current.setData(realMA99)

          const latest = apiCandleData[apiCandleData.length - 1]
          const lVol = apiVolData[apiVolData.length - 1]
          if (latest) {
            const upHover: HoverBarData = {
              time: new Date((latest.time as number) * 1000).toLocaleString('en-US', {
                month: 'short',
                day: 'numeric',
                hour: '2-digit',
                minute: '2-digit',
                hour12: false,
              }),
              open: latest.open,
              high: latest.high,
              low: latest.low,
              close: latest.close,
              change: latest.close - latest.open,
              changePercent: ((latest.close - latest.open) / latest.open) * 100,
              volume: lVol ? lVol.value : 0,
              ma7: realMA7[realMA7.length - 1]?.value,
              ma25: realMA25[realMA25.length - 1]?.value,
              ma99: realMA99[realMA99.length - 1]?.value,
              isUp: latest.close >= latest.open,
            }
            latestBarRef.current = upHover
            setHoverData(upHover)
          }

          chart.timeScale().fitContent()
        }
      })
      .catch(() => {
        // keep generated data
      })

    // ── Binance-Style Crosshair Hover Event Listener ──
    chart.subscribeCrosshairMove((param) => {
      if (isDisposed) return
      if (!param.time || !param.seriesData) {
        setHoverData(latestBarRef.current)
        return
      }

      const cData = param.seriesData.get(candleSeries) as CandlestickData | undefined
      const vData = param.seriesData.get(volumeSeries) as HistogramData | undefined
      const m7Data = param.seriesData.get(ma7Series) as LineData | undefined
      const m25Data = param.seriesData.get(ma25Series) as LineData | undefined
      const m99Data = param.seriesData.get(ma99Series) as LineData | undefined

      if (cData) {
        const timeNum = typeof param.time === 'number' ? param.time : (param.time as { year: number }).year ? Date.now() / 1000 : 0
        const dateStr = new Date(timeNum * 1000).toLocaleString('en-US', {
          month: 'short',
          day: 'numeric',
          hour: '2-digit',
          minute: '2-digit',
          hour12: false,
        })
        const change = cData.close - cData.open
        const changePct = ((cData.close - cData.open) / cData.open) * 100

        setHoverData({
          time: dateStr,
          open: cData.open,
          high: cData.high,
          low: cData.low,
          close: cData.close,
          change,
          changePercent: changePct,
          volume: vData ? vData.value : 0,
          ma7: m7Data?.value,
          ma25: m25Data?.value,
          ma99: m99Data?.value,
          isUp: cData.close >= cData.open,
        })
      } else {
        setHoverData(latestBarRef.current)
      }
    })

    // Auto-fit content on initial mount
    chart.timeScale().fitContent()

    return () => {
      isDisposed = true
      if (chartRef.current) {
        chartRef.current.remove()
        chartRef.current = null
      }
      candleSeriesRef.current = null
      volumeSeriesRef.current = null
      ma7SeriesRef.current = null
      ma25SeriesRef.current = null
      ma99SeriesRef.current = null
    }
  }, [selectedMarketId, activeTimeframe])

  const formatPrice = (num?: number) => {
    if (num === undefined || isNaN(num)) return '0.00'
    const decimals = basePriceNum > 1000 ? 2 : basePriceNum > 10 ? 2 : 4
    return num.toLocaleString('en-US', { minimumFractionDigits: decimals, maximumFractionDigits: decimals })
  }

  return (
    <div className="flex-1 bg-[#0e121b] border border-[#1b2230] rounded-xl flex flex-col overflow-hidden relative shadow-xl min-h-[460px]">
      {/* ── Top Bar: Timeframes + Indicators + Chart Tools ── */}
      <div className="h-10 border-b border-[#1b2230] px-3 flex items-center justify-between bg-[#07090e]/60 flex-shrink-0 text-xs select-none">
        {/* Timeframe Selector Pills */}
        <div className="flex items-center gap-1">
          {TIMEFRAMES.map((tf) => (
            <button
              key={tf}
              type="button"
              onClick={() => setActiveTimeframe(tf)}
              className={`px-2.5 py-1 rounded-lg font-mono text-[11px] font-semibold transition-all ${
                activeTimeframe === tf
                  ? 'bg-[#00e5ff]/15 text-[#00e5ff] border border-[#00e5ff]/40 shadow-[0_0_10px_rgba(0,229,255,0.2)]'
                  : 'text-slate-400 hover:text-slate-200 hover:bg-[#141a26]'
              }`}
            >
              {tf}
            </button>
          ))}

          <div className="h-4 w-px bg-[#1b2230] mx-1.5" />

          {/* Indicators & Templates */}
          <button
            type="button"
            className="flex items-center gap-1.5 px-2.5 py-1 rounded-lg text-slate-400 hover:text-white hover:bg-[#141a26] text-[11px] transition-colors"
          >
            <Activity size={13} className="text-[#00e5ff]" />
            <span>Indicators</span>
          </button>

          <button
            type="button"
            className="flex items-center gap-1.5 px-2.5 py-1 rounded-lg text-slate-400 hover:text-white hover:bg-[#141a26] text-[11px] transition-colors"
          >
            <Sliders size={13} />
            <span>Templates</span>
          </button>
        </div>

        {/* Right Tools: Snapshot & Fullscreen */}
        <div className="flex items-center gap-1 text-slate-400">
          <button
            type="button"
            aria-label="Take screenshot"
            className="p-1.5 rounded-lg hover:text-white hover:bg-[#141a26] transition-colors"
          >
            <Camera size={14} />
          </button>
          <button
            type="button"
            aria-label="Fullscreen chart"
            className="p-1.5 rounded-lg hover:text-white hover:bg-[#141a26] transition-colors"
          >
            <Maximize2 size={14} />
          </button>
        </div>
      </div>

      {/* ── Main Chart Body with Left Drawing Toolbar ── */}
      <div className="flex-1 flex min-h-0 relative">
        {/* Left Vertical Drawing Toolbar */}
        <div className="w-10 border-r border-[#1b2230] bg-[#07090e]/80 flex flex-col items-center py-2 space-y-1.5 flex-shrink-0 z-10 select-none">
          {[
            { id: 'crosshair', icon: Crosshair, label: 'Crosshair' },
            { id: 'trend', icon: TrendingUp, label: 'Trend Line' },
            { id: 'text', icon: Type, label: 'Text' },
            { id: 'emoji', icon: Smile, label: 'Icons' },
            { id: 'ruler', icon: Ruler, label: 'Measure' },
            { id: 'magnet', icon: Magnet, label: 'Magnet' },
            { id: 'lock', icon: Lock, label: 'Lock Tools' },
            { id: 'trash', icon: Trash2, label: 'Clear Drawings' },
          ].map((tool) => {
            const Icon = tool.icon
            const isActive = activeTool === tool.id
            return (
              <button
                key={tool.id}
                type="button"
                aria-label={tool.label}
                onClick={() => setActiveTool(tool.id)}
                className={`p-2 rounded-lg text-xs transition-colors ${
                  isActive
                    ? 'bg-[#00e5ff]/15 text-[#00e5ff] border border-[#00e5ff]/30'
                    : 'text-slate-500 hover:text-slate-300 hover:bg-[#141a26]'
                }`}
              >
                <Icon size={14} />
              </button>
            )
          })}
        </div>

        {/* Lightweight Charts Canvas Mounting Container */}
        <div className="flex-1 relative bg-[#07090e] overflow-hidden flex flex-col select-none">
          {/* ── Binance-Style Real-Time Interactive OHLC Banner (Updates on Hover) ── */}
          <div className="px-3 py-2 z-10 flex flex-col gap-1 text-[11px] font-mono select-none bg-[#07090e]/80 border-b border-[#1b2230]/50 backdrop-blur-sm">
            <div className="flex items-center gap-3 flex-wrap">
              <span className="font-bold text-white tracking-tight font-sans text-xs">
                {selectedMarketId.replace('-', '/')} · {activeTimeframe} · TradeDrift
              </span>

              {hoverData && (
                <>
                  <span className="text-slate-400 text-[10px]">{hoverData.time}</span>
                  <span className="text-slate-400">
                    O <span className={hoverData.isUp ? 'text-[#00e676]' : 'text-[#ff3366]'}>{formatPrice(hoverData.open)}</span>
                  </span>
                  <span className="text-slate-400">
                    H <span className={hoverData.isUp ? 'text-[#00e676]' : 'text-[#ff3366]'}>{formatPrice(hoverData.high)}</span>
                  </span>
                  <span className="text-slate-400">
                    L <span className={hoverData.isUp ? 'text-[#00e676]' : 'text-[#ff3366]'}>{formatPrice(hoverData.low)}</span>
                  </span>
                  <span className="text-slate-400">
                    C <span className={hoverData.isUp ? 'text-[#00e676] font-bold' : 'text-[#ff3366] font-bold'}>{formatPrice(hoverData.close)}</span>
                  </span>
                  <span className={`font-semibold ${hoverData.isUp ? 'text-[#00e676]' : 'text-[#ff3366]'}`}>
                    {hoverData.change >= 0 ? '+' : ''}{hoverData.change.toFixed(2)} ({hoverData.changePercent >= 0 ? '+' : ''}{hoverData.changePercent.toFixed(2)}%)
                  </span>
                  <span className="text-slate-400 text-[10px]">
                    Vol: <span className="text-slate-200">{hoverData.volume.toLocaleString()}</span>
                  </span>
                </>
              )}
            </div>

            {/* Moving Average Values Indicator */}
            {hoverData && (
              <div className="flex items-center gap-3 text-[10px] text-slate-400">
                {hoverData.ma7 !== undefined && (
                  <span className="flex items-center gap-1">
                    <span className="w-2 h-0.5 bg-[#f59e0b] rounded" />
                    <span>MA 7 close</span>
                    <span className="text-[#f59e0b] font-medium">{formatPrice(hoverData.ma7)}</span>
                  </span>
                )}
                {hoverData.ma25 !== undefined && (
                  <span className="flex items-center gap-1">
                    <span className="w-2 h-0.5 bg-[#00e5ff] rounded" />
                    <span>MA 25 close</span>
                    <span className="text-[#00e5ff] font-medium">{formatPrice(hoverData.ma25)}</span>
                  </span>
                )}
                {hoverData.ma99 !== undefined && (
                  <span className="flex items-center gap-1">
                    <span className="w-2 h-0.5 bg-[#a855f7] rounded" />
                    <span>MA 99 close</span>
                    <span className="text-[#a855f7] font-medium">{formatPrice(hoverData.ma99)}</span>
                  </span>
                )}
              </div>
            )}
          </div>

          {/* Canvas Viewport */}
          <div ref={chartContainerRef} className="flex-1 w-full h-full min-h-[380px]" />
        </div>
      </div>
    </div>
  )
}
