import { useEffect, useRef } from 'react'
import {
  createChart,
  AreaSeries,
  LineSeries,
  type IChartApi,
  type ISeriesApi,
  type UTCTimestamp,
} from 'lightweight-charts'
import type { PerformanceDataPoint } from '../../types/portfolio'

interface Props {
  data: PerformanceDataPoint[]
  showBtcBenchmark: boolean
}

/**
 * Portfolio performance chart using lightweight-charts v5.
 *
 * - AreaSeries (green) for portfolio equity curve.
 * - LineSeries (dashed, slate) for BTC benchmark — toggled by showBtcBenchmark prop.
 * - ResizeObserver for dynamic responsive sizing.
 * - chart.remove() cleanup on unmount.
 * - Data is passed as deterministic props; no internal random generation.
 */
export default function PerformanceChart({ data, showBtcBenchmark }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)
  const chartRef = useRef<IChartApi | null>(null)
  const portfolioSeriesRef = useRef<ISeriesApi<'Area'> | null>(null)
  const btcSeriesRef = useRef<ISeriesApi<'Line'> | null>(null)

  // ── Create chart once ──────────────────────────────────────────────────────
  useEffect(() => {
    if (!containerRef.current) return
    const container = containerRef.current

    const chart = createChart(container, {
      width: container.clientWidth,
      height: container.clientHeight,
      layout: {
        background: { color: 'transparent' },
        textColor: '#64748b',
        fontSize: 11,
      },
      grid: {
        vertLines: { color: '#1e2530' },
        horzLines: { color: '#1e2530' },
      },
      rightPriceScale: {
        borderColor: '#1e2530',
        textColor: '#64748b',
        scaleMargins: { top: 0.1, bottom: 0.1 },
        autoScale: true,
      },
      timeScale: {
        borderColor: '#1e2530',
        timeVisible: true,
        secondsVisible: false,
        fixRightEdge: true,
        fixLeftEdge: false,
      },
      crosshair: {
        vertLine: { color: '#475569', style: 1, width: 1, labelBackgroundColor: '#1e2530' },
        horzLine: { color: '#475569', style: 1, width: 1, labelBackgroundColor: '#1e2530' },
        mode: 1,
      },
      handleScroll: { mouseWheel: true, pressedMouseMove: true },
      handleScale: { mouseWheel: true, pinch: true },
    })
    chartRef.current = chart

    // Portfolio equity area series
    const portfolioSeries = chart.addSeries(AreaSeries, {
      topColor: 'rgba(16, 185, 129, 0.3)',
      bottomColor: 'rgba(16, 185, 129, 0.01)',
      lineColor: '#10b981',
      lineWidth: 2,
      priceLineVisible: false,
      lastValueVisible: true,
    })
    portfolioSeriesRef.current = portfolioSeries

    // BTC benchmark — dashed line series
    const btcSeries = chart.addSeries(LineSeries, {
      color: '#94a3b8',
      lineWidth: 1,
      lineStyle: 2, // dashed
      priceLineVisible: false,
      lastValueVisible: false,
    })
    btcSeriesRef.current = btcSeries

    // ResizeObserver
    const resizeObserver = new ResizeObserver((entries) => {
      for (const entry of entries) {
        const { width, height } = entry.contentRect
        if (width > 0 && height > 0) chart.resize(width, height)
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
  }, []) // create once

  // ── Feed data when it changes ──────────────────────────────────────────────
  useEffect(() => {
    if (!portfolioSeriesRef.current || !btcSeriesRef.current || data.length === 0) return

    const portfolioData = data.map((d) => ({
      time: d.time as UTCTimestamp,
      value: d.portfolioValue,
    }))
    const btcData = data.map((d) => ({
      time: d.time as UTCTimestamp,
      value: d.btcBenchmarkValue,
    }))

    portfolioSeriesRef.current.setData(portfolioData)
    btcSeriesRef.current.setData(showBtcBenchmark ? btcData : [])
    chartRef.current?.timeScale().fitContent()
  }, [data, showBtcBenchmark])

  // ── Toggle BTC series visibility ───────────────────────────────────────────
  useEffect(() => {
    if (!btcSeriesRef.current || !chartRef.current || data.length === 0) return
    const btcData = data.map((d) => ({
      time: d.time as UTCTimestamp,
      value: d.btcBenchmarkValue,
    }))
    btcSeriesRef.current.setData(showBtcBenchmark ? btcData : [])
  }, [showBtcBenchmark, data])

  return (
    <div
      ref={containerRef}
      className="w-full h-full"
      role="img"
      aria-label="Portfolio performance chart"
    />
  )
}
