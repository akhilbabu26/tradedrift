import { useEffect, useRef } from 'react'
import {
  createChart,
  AreaSeries,
  LineSeries,
  type IChartApi,
  type ISeriesApi,
  type AreaSeriesOptions,
  type UTCTimestamp,
} from 'lightweight-charts'
import { Loader2, RefreshCw, AlertCircle } from 'lucide-react'
import type { PerformanceDataPoint } from '../../types/portfolio'

interface Props {
  data: PerformanceDataPoint[]
  showBtcBenchmark?: boolean
  isLoading?: boolean
  error?: string | null
  onRetry?: () => void
}

/**
 * Portfolio performance chart using lightweight-charts v5.
 * Matches Dashboard PortfolioChart in visual theme, interactive tooltips, and smooth loading states.
 */
export default function PerformanceChart({
  data,
  showBtcBenchmark = false,
  isLoading = false,
  error = null,
  onRetry,
}: Props) {
  const containerRef = useRef<HTMLDivElement>(null)
  const tooltipRef = useRef<HTMLDivElement>(null)
  const chartRef = useRef<IChartApi | null>(null)
  const portfolioSeriesRef = useRef<ISeriesApi<'Area'> | null>(null)
  const btcSeriesRef = useRef<ISeriesApi<'Line'> | null>(null)

  // ── 1. Create chart once on mount (matches Dashboard PortfolioChart) ───────
  useEffect(() => {
    if (!containerRef.current) return
    const container = containerRef.current

    const chart = createChart(container, {
      width: container.clientWidth || 600,
      height: container.clientHeight || 220,
      layout: {
        background: { color: 'transparent' },
        textColor: '#777E87',
        fontSize: 11,
        fontFamily: "'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif",
      },
      grid: {
        vertLines: { color: '#202225', style: 1 },
        horzLines: { color: '#202225', style: 1 },
      },
      rightPriceScale: {
        borderColor: '#292C30',
        textColor: '#777E87',
        scaleMargins: { top: 0.12, bottom: 0.14 },
        autoScale: true,
        entireTextOnly: true,
        minimumWidth: 80,
        alignLabels: true,
      },
      timeScale: {
        borderColor: '#292C30',
        fixLeftEdge: true,
        fixRightEdge: true,
        timeVisible: true,
        secondsVisible: false,
        uniformDistribution: true,
        minimumHeight: 26,
      },
      crosshair: {
        vertLine: { color: '#475569', style: 1, width: 1, labelBackgroundColor: '#161B22' },
        horzLine: { color: '#475569', style: 1, width: 1, labelBackgroundColor: '#10B981' },
      },
      handleScroll: false,
      handleScale: false,
    })
    chartRef.current = chart

    // Portfolio equity area series
    const seriesOptions: Partial<AreaSeriesOptions> = {
      lineColor: '#10B981',
      lineWidth: 2,
      priceLineVisible: false,
      lastValueVisible: true,
      crosshairMarkerVisible: true,
      crosshairMarkerRadius: 5,
      crosshairMarkerBackgroundColor: '#10B981',
      topColor: 'rgba(16, 185, 129, 0.20)',
      bottomColor: 'rgba(16, 185, 129, 0.00)',
      priceFormat: {
        type: 'price',
        precision: 2,
        minMove: 0.01,
      },
    }
    const portfolioSeries = chart.addSeries(AreaSeries, seriesOptions)
    portfolioSeriesRef.current = portfolioSeries

    // BTC benchmark — dashed line series
    const btcSeries = chart.addSeries(LineSeries, {
      color: '#94a3b8',
      lineWidth: 1,
      lineStyle: 2,
      priceLineVisible: false,
      lastValueVisible: false,
      priceFormat: {
        type: 'price',
        precision: 2,
        minMove: 0.01,
      },
    })
    btcSeriesRef.current = btcSeries

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

      const price = param.seriesData.get(portfolioSeries) as { value: number } | undefined
      if (!price || typeof price.value !== 'number') {
        tooltip.style.display = 'none'
        return
      }

      const dateObj = new Date((param.time as number) * 1000)
      const dateStr = dateObj.toLocaleDateString(undefined, {
        month: 'short',
        day: 'numeric',
        hour: '2-digit',
        minute: '2-digit',
      })

      tooltip.innerHTML = `
        <div class="flex items-center gap-1.5 mb-0.5">
          <span class="w-2 h-2 rounded-full bg-[#10B981] inline-block shadow-[0_0_6px_rgba(16,185,129,0.5)]"></span>
          <span class="text-xs font-mono font-bold text-[#F2F3F5]">${price.value.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })} USDT</span>
        </div>
        <div class="text-[10px] text-[#777E87]">${dateStr}</div>
      `
      tooltip.style.display = 'block'

      const tooltipWidth = 150
      const tooltipHeight = 50
      let left = param.point.x + 12
      let top = param.point.y - 20

      if (left + tooltipWidth > container.clientWidth) {
        left = param.point.x - tooltipWidth - 12
      }
      if (top < 0) {
        top = 10
      } else if (top + tooltipHeight > container.clientHeight) {
        top = container.clientHeight - tooltipHeight - 10
      }

      tooltip.style.left = `${left}px`
      tooltip.style.top = `${top}px`
    })

    // ── ResizeObserver ─────────────────────────────────────────────────────
    const resizeObserver = new ResizeObserver((entries) => {
      for (const entry of entries) {
        const { width, height } = entry.contentRect
        if (width > 0 && height > 0) {
          chart.resize(width, height)
          chart.timeScale().fitContent()
        }
      }
    })
    resizeObserver.observe(container)

    return () => {
      resizeObserver.disconnect()
      chart.remove()
      chartRef.current = null
      portfolioSeriesRef.current = null
      btcSeriesRef.current = null
    }
  }, [])

  // ── 2. Update series without remounting ───────────────────────────────────
  useEffect(() => {
    if (!portfolioSeriesRef.current || !btcSeriesRef.current || !chartRef.current) return

    if (Array.isArray(data) && data.length > 0) {
      const validPortfolioPoints = data
        .filter(
          (d) =>
            d &&
            typeof d.time === 'number' &&
            !isNaN(d.time) &&
            typeof d.portfolioValue === 'number' &&
            !isNaN(d.portfolioValue)
        )
        .map((d) => ({
          time: Math.floor(d.time) as UTCTimestamp,
          value: d.portfolioValue,
        }))
        .sort((a, b) => (a.time as number) - (b.time as number))

      if (validPortfolioPoints.length > 0) {
        portfolioSeriesRef.current.setData(validPortfolioPoints)
        chartRef.current.timeScale().fitContent()
      }

      if (showBtcBenchmark) {
        const validBtcPoints = data
          .filter(
            (d) =>
              d &&
              typeof d.time === 'number' &&
              !isNaN(d.time) &&
              typeof d.btcBenchmarkValue === 'number' &&
              !isNaN(d.btcBenchmarkValue)
          )
          .map((d) => ({
            time: Math.floor(d.time) as UTCTimestamp,
            value: d.btcBenchmarkValue,
          }))
          .sort((a, b) => (a.time as number) - (b.time as number))

        btcSeriesRef.current.setData(validBtcPoints)
      } else {
        btcSeriesRef.current.setData([])
      }
    }
  }, [data, showBtcBenchmark])

  return (
    <div
      ref={containerRef}
      className="w-full h-full min-h-[210px] relative"
      role="img"
      aria-label="Portfolio performance chart"
    >
      {/* Interactive hover tooltip */}
      <div
        ref={tooltipRef}
        className="absolute z-20 pointer-events-none hidden bg-[#161B22]/95 backdrop-blur-sm border border-[#292C30] rounded-lg px-2.5 py-1.5 shadow-[0_4px_16px_rgba(0,0,0,0.6)]"
        style={{ transition: 'left 0.05s ease-out, top 0.05s ease-out' }}
      />

      {/* Subtle, non-blocking loading overlay during timeframe transitions */}
      {isLoading && (
        <div
          data-testid="chart-loading-overlay"
          className="absolute inset-0 z-10 flex items-center justify-center bg-[#111318]/40 backdrop-blur-[1px] rounded-lg transition-opacity duration-200"
        >
          <div className="flex items-center gap-2 px-3 py-1.5 rounded-lg bg-[#161B22]/90 border border-[#292C30] shadow-[0_4px_12px_rgba(0,0,0,0.5)]">
            <Loader2 size={15} className="text-[#10B981] animate-spin" />
            <span className="text-xs font-medium text-slate-300">Updating timeframe...</span>
          </div>
        </div>
      )}

      {/* Inline error overlay with retry button */}
      {!isLoading && error && (
        <div
          data-testid="chart-error-overlay"
          className="absolute inset-0 z-10 flex flex-col items-center justify-center gap-2 bg-[#111318]/80 backdrop-blur-[2px] rounded-lg p-4"
        >
          <div className="flex items-center gap-1.5 text-xs text-red-400 font-medium">
            <AlertCircle size={14} className="flex-shrink-0" />
            <span>Failed to load timeframe data</span>
          </div>
          {onRetry && (
            <button
              type="button"
              onClick={onRetry}
              className="flex items-center gap-1.5 text-xs text-slate-300 hover:text-[#F2F3F5] px-3 py-1 rounded bg-[#202225] border border-[#292C30] hover:border-slate-500 transition-colors"
            >
              <RefreshCw size={12} />
              Retry
            </button>
          )}
        </div>
      )}
    </div>
  )
}
