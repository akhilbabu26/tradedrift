import { useId, useMemo } from 'react'

export interface SparklineProps {
  /** Array of price numbers, price strings, or pre-normalized values */
  data?: (number | string)[]
  /** Backwards-compatibility prop for existing callers */
  points?: (number | string)[]
  /**
   * Optional trend direction override.
   * If not provided, trend color is derived from data:
   *   last > first -> green (#10b981)
   *   last < first -> red   (#ef4444)
   *   last == first or < 2 points -> neutral (#94a3b8)
   */
  positive?: boolean
  /** Width in px (default 120) */
  width?: number
  /** Height in px (default 34) */
  height?: number
  /** Stroke color override */
  color?: string
  /** Whether to show a subtle gradient fill under the line (default true) */
  showGradient?: boolean
  /** Accessible label */
  ariaLabel?: string
  className?: string
}

/**
 * Compact, lightweight SVG Sparkline.
 * Zero external dependencies. Visualizes real TradeDrift price movement
 * with rounded joins, crisp lines, and an authentic gradient area fill.
 */
export default function Sparkline({
  data,
  points,
  positive,
  width = 120,
  height = 34,
  color,
  showGradient = true,
  ariaLabel,
  className,
}: SparklineProps) {
  // 1. Filter and parse valid positive numeric prices
  const values = useMemo(() => {
    const list = data ?? points ?? []
    return list
      .map((v) => (typeof v === 'number' ? v : parseFloat(String(v))))
      .filter((v) => !isNaN(v) && isFinite(v) && v > 0)
  }, [data, points])

  // 2. Derive trend semantics from valid historical data
  const first = values[0]
  const last = values[values.length - 1]

  const trendDirection = useMemo<'positive' | 'negative' | 'neutral'>(() => {
    // Insufficient observations (< 2 valid prices) must ALWAYS be neutral
    if (values.length < 2) return 'neutral'
    if (positive !== undefined) {
      return positive ? 'positive' : 'negative'
    }
    if (last > first) return 'positive'
    if (last < first) return 'negative'
    return 'neutral'
  }, [positive, values.length, first, last])

  const strokeColor = useMemo(() => {
    if (color) return color
    if (trendDirection === 'positive') return '#10b981'
    if (trendDirection === 'negative') return '#ef4444'
    return '#94a3b8' // Neutral slate-400
  }, [color, trendDirection])

  // Unique gradient ID per component instance to prevent SVG collisions
  const reactId = useId()
  const gradientId = useMemo(() => `sparkline-grad-${reactId.replace(/[^a-zA-Z0-9_-]/g, '')}`, [reactId])

  // 3. Compute SVG path geometry
  const { linePath, areaPath, isPlaceholder } = useMemo(() => {
    const padX = 2
    const padY = 3
    const innerW = Math.max(1, width - padX * 2)
    const innerH = Math.max(1, height - padY * 2)
    const midY = height / 2

    // Empty dataset: render a subtle neutral dashed placeholder line
    if (values.length === 0) {
      return {
        linePath: `M ${padX} ${midY} L ${width - padX} ${midY}`,
        areaPath: '',
        isPlaceholder: true,
      }
    }

    // 1-point dataset: render a flat horizontal line
    if (values.length === 1) {
      return {
        linePath: `M ${padX} ${midY} L ${width - padX} ${midY}`,
        areaPath: `M ${padX} ${height} L ${padX} ${midY} L ${width - padX} ${midY} L ${width - padX} ${height} Z`,
        isPlaceholder: false,
      }
    }

    const min = Math.min(...values)
    const max = Math.max(...values)
    const rawRange = max - min

    // Completely flat dataset: render centered flat line
    if (rawRange === 0) {
      return {
        linePath: `M ${padX} ${midY} L ${width - padX} ${midY}`,
        areaPath: `M ${padX} ${height} L ${padX} ${midY} L ${width - padX} ${midY} L ${width - padX} ${height} Z`,
        isPlaceholder: false,
      }
    }

    // Proportional financial scaling:
    // 1. Enforce a minimum proportional visual window (1.5% of mid price) so micro-movements
    //    (e.g. 0.1% to 0.2% price noise) are not exaggerated into full-height 100% vertical swings.
    // 2. For volatile movements (>1.5%), add 15% vertical headroom & footroom so peaks/dips
    //    never hit the absolute ceiling or floor boundaries.
    const mid = (min + max) / 2
    const minVisualRange = mid > 1 ? mid * 0.015 : 0.05
    const visualRange = rawRange < minVisualRange ? minVisualRange : rawRange * 1.15
    const effectiveMin = mid - visualRange / 2

    // Map genuine price observations to SVG coordinates
    const coords: [number, number][] = values.map((v, i) => {
      const x = padX + (i / (values.length - 1)) * innerW
      const normalizedY = (v - effectiveMin) / visualRange
      const clampedY = Math.max(0, Math.min(1, normalizedY))
      const y = height - padY - clampedY * innerH
      return [Number(x.toFixed(2)), Number(y.toFixed(2))]
    })

    const lPath = `M ${coords.map(([x, y]) => `${x} ${y}`).join(' L ')}`
    const aPath = `M ${coords[0][0]} ${height} L ${coords.map(([x, y]) => `${x} ${y}`).join(' L ')} L ${coords[coords.length - 1][0]} ${height} Z`

    return {
      linePath: lPath,
      areaPath: aPath,
      isPlaceholder: false,
    }
  }, [values, width, height])

  return (
    <svg
      width={width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      className={`overflow-visible select-none inline-block ${className || ''}`}
      role="img"
      aria-label={
        ariaLabel ||
        (isPlaceholder
          ? 'No trend data available'
          : `Trend sparkline: ${trendDirection} (${values.length} observations)`)
      }
    >
      <defs>
        <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={strokeColor} stopOpacity={0.22} />
          <stop offset="100%" stopColor={strokeColor} stopOpacity={0.0} />
        </linearGradient>
      </defs>

      {showGradient && areaPath && !isPlaceholder && (
        <path d={areaPath} fill={`url(#${gradientId})`} />
      )}

      <path
        d={linePath}
        fill="none"
        stroke={strokeColor}
        strokeWidth={1.5}
        strokeLinecap="round"
        strokeLinejoin="round"
        strokeDasharray={isPlaceholder ? '3 3' : undefined}
      />
    </svg>
  )
}
