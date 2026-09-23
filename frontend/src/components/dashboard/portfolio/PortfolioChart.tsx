import { useEffect, useRef } from 'react'
import { createChart, type IChartApi, type ISeriesApi, AreaSeries, type AreaSeriesOptions, type UTCTimestamp } from 'lightweight-charts'
import type { PortfolioDataPoint } from '../../../types/dashboard'

interface PortfolioChartProps {
  data: PortfolioDataPoint[]
  latestValue: string
}

/**
 * Portfolio value area chart using lightweight-charts (Canvas/TradingView).
 *
 * Key implementation details:
 * - ResizeObserver on the container div → chart.resize() on size changes
 * - chart.remove() on unmount (cleanup)
 * - resizeObserver.disconnect() on unmount (cleanup)
 * - No fixed width/height — responds to container size
 */
export default function PortfolioChart({ data }: PortfolioChartProps) {
  const containerRef = useRef<HTMLDivElement>(null)
  const chartRef     = useRef<IChartApi | null>(null)
  const seriesRef    = useRef<ISeriesApi<'Area'> | null>(null)

  useEffect(() => {
    if (!containerRef.current) return

    const container = containerRef.current

    // ── Create Chart ───────────────────────────────────────────────────────
    const chart = createChart(container, {
      width:  container.clientWidth,
      height: container.clientHeight,
      layout: {
        background: { color: 'transparent' },
        textColor:  '#64748b',
      },
      grid: {
        vertLines: { color: '#1e2530', style: 1 },
        horzLines: { color: '#1e2530', style: 1 },
      },
      rightPriceScale: {
        borderColor:  '#1e2530',
        textColor:    '#64748b',
        scaleMargins: { top: 0.1, bottom: 0.05 },
      },
      timeScale: {
        borderColor:     '#1e2530',
        fixLeftEdge:     true,
        fixRightEdge:    true,
        timeVisible:     true,
        secondsVisible:  false,
      },
      crosshair: {
        vertLine: { color: '#10b981', style: 1, width: 1, labelBackgroundColor: '#10b981' },
        horzLine: { color: '#10b981', style: 1, width: 1, labelBackgroundColor: '#10b981' },
      },
      handleScroll: false,
      handleScale:  false,
    })

    chartRef.current = chart

    // ── Add Area / Line Series ─────────────────────────────────────────────
    const seriesOptions: Partial<AreaSeriesOptions> = {
      lineColor:       '#10b981',
      lineWidth:       2,
      priceLineVisible: false,
      lastValueVisible: true,
      crosshairMarkerVisible: true,
      crosshairMarkerRadius:  5,
      crosshairMarkerBackgroundColor: '#10b981',
      topColor:    'rgba(16,185,129,0.20)',
      bottomColor: 'rgba(16,185,129,0.00)',
    }

    const series = chart.addSeries(AreaSeries, seriesOptions)

    seriesRef.current = series
    series.setData(data.map(d => ({ ...d, time: d.time as UTCTimestamp })))
    chart.timeScale().fitContent()

    // ── ResizeObserver — chart responds to container size changes ──────────
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

    // ── Cleanup on unmount ─────────────────────────────────────────────────
    return () => {
      resizeObserver.disconnect()
      chart.remove()
    }
  }, [data])

  return (
    <div className="flex flex-col">
      <p className="text-[11px] font-medium text-slate-400 mb-2 uppercase tracking-wider">
        Portfolio Value (USDT)
      </p>
      <div
        ref={containerRef}
        className="w-full"
        style={{ height: '220px' }}
        aria-label="Portfolio value chart"
        role="img"
      />
    </div>
  )
}
