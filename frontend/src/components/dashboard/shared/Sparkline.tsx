interface SparklineProps {
  /** Normalized values 0–1 */
  points: number[]
  positive: boolean
  width?: number
  height?: number
}

/**
 * Inline SVG sparkline for Market Pulse rows.
 * No external dependency — renders a polyline from normalized 0–1 points.
 */
export default function Sparkline({ points, positive, width = 64, height = 24 }: SparklineProps) {
  if (!points || points.length < 2) return null

  const color = positive ? '#10b981' : '#ef4444'
  const fillColor = positive ? 'rgba(16,185,129,0.12)' : 'rgba(239,68,68,0.12)'

  const xs = points.map((_, i) => (i / (points.length - 1)) * width)
  const ys = points.map((v) => height - v * (height - 2) - 1)

  const linePoints = xs.map((x, i) => `${x},${ys[i]}`).join(' ')
  const areaPoints = `${xs[0]},${height} ${linePoints} ${xs[xs.length - 1]},${height}`

  return (
    <svg
      width={width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      xmlns="http://www.w3.org/2000/svg"
      className="overflow-visible"
      aria-hidden="true"
    >
      <polygon points={areaPoints} fill={fillColor} />
      <polyline
        points={linePoints}
        fill="none"
        stroke={color}
        strokeWidth="1.5"
        strokeLinejoin="round"
        strokeLinecap="round"
      />
    </svg>
  )
}
