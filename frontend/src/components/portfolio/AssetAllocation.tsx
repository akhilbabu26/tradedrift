import { toDecimal } from '../../utils/decimal'
import { formatUSDT } from '../../utils/formatters'
import type { AssetAllocationItem } from '../../types/portfolio'
import { RaisedSurface } from '../common/Surface'

interface Props {
  allocation: AssetAllocationItem[]
  totalValue: string
}

/**
 * Asset Allocation card — SVG donut chart + breakdown list.
 * All text uses TradeDrift light tokens. No dark inherited classes.
 */
export default function AssetAllocation({ allocation, totalValue }: Props) {
  const handleWheel = (e: React.WheelEvent<HTMLDivElement>) => {
    if (e.deltaY !== 0 && e.currentTarget.scrollWidth > e.currentTarget.clientWidth) {
      const { scrollLeft, scrollWidth, clientWidth } = e.currentTarget
      const maxScroll = scrollWidth - clientWidth
      const canScrollRight = e.deltaY > 0 && Math.ceil(scrollLeft) < maxScroll
      const canScrollLeft = e.deltaY < 0 && scrollLeft > 0

      if (canScrollRight || canScrollLeft || e.shiftKey) {
        e.currentTarget.scrollLeft += e.deltaY
      }
    }
  }

  // Compute crypto vs cash exposure percentages
  const cryptoPct = allocation
    .filter((a) => a.exposureType === 'crypto')
    .reduce((sum, a) => sum + parseFloat(a.percentage), 0)
  const cashPct = 100 - cryptoPct

  // ── SVG Donut Chart ──────────────────────────────────────────────────────────
  const SIZE = 150
  const RADIUS = 58
  const STROKE = 18
  const CIRCUMFERENCE = 2 * Math.PI * RADIUS
  const cx = SIZE / 2
  const cy = SIZE / 2

  // Build arc segments with standard SVG circumference offsets starting at 12 o'clock
  let cumulativeLength = 0
  const segments = allocation.map((item) => {
    const pct = parseFloat(item.percentage) || 0
    const arcLength = (pct / 100) * CIRCUMFERENCE
    const gap = allocation.length > 1 ? 2 : 0
    const visibleLength = Math.max(0, arcLength - gap)
    const seg = {
      color: item.hexColor,
      asset: item.asset,
      percentage: pct,
      dashArray: `${visibleLength} ${CIRCUMFERENCE - visibleLength}`,
      dashOffset: -cumulativeLength,
    }
    cumulativeLength += arcLength
    return seg
  })

  return (
    <RaisedSurface variant="raised-subtle" noPadding className="flex flex-col overflow-hidden h-full">
      {/* Header */}
      <div className="px-5 pt-4 pb-3 border-b border-[#1e2530] flex-shrink-0">
        <div className="flex items-center justify-between flex-wrap gap-2">
          <h2 className="text-sm font-semibold text-[#f5f7fa]">
            Asset Allocation
          </h2>
          <div className="flex items-center gap-2">
            {cryptoPct > 0 && (
              <span className="text-[11px] px-2 py-0.5 rounded-full bg-[#10b981]/10 border border-[#10b981]/20 text-[#10b981]">
                Crypto Exposure: {cryptoPct.toFixed(0)}%
              </span>
            )}
            {cashPct > 0 && (
              <span className="text-[11px] px-2 py-0.5 rounded-full bg-[#475569]/20 border border-[#475569]/30 text-[#94a3b8]">
                Cash: {cashPct.toFixed(0)}%
              </span>
            )}
          </div>
        </div>
      </div>

      {/* Body — donut left, breakdown right with horizontal scrolling */}
      <div
        onWheel={handleWheel}
        className="flex flex-row items-center gap-5 p-4 flex-1 min-w-0 w-full overflow-x-auto trade-scrollbar-x"
        tabIndex={0}
        aria-label="Asset allocation chart and breakdown"
      >
        <div className="flex items-center gap-5 min-w-[440px] flex-shrink-0 w-full">
          {/* Donut chart */}
          {allocation.length > 0 ? (
            <div className="relative flex-shrink-0" style={{ width: SIZE, height: SIZE }}>
              <svg width={SIZE} height={SIZE} viewBox={`0 0 ${SIZE} ${SIZE}`}>
                {/* Background ring */}
                <circle
                  cx={cx}
                  cy={cy}
                  r={RADIUS}
                  fill="none"
                  stroke="#1e2530"
                  strokeWidth={STROKE}
                />
                {/* Colored segments rotated to start at 12 o'clock */}
                <g transform={`rotate(-90 ${cx} ${cy})`}>
                  {segments.map((seg) => (
                    <circle
                      key={seg.asset}
                      cx={cx}
                      cy={cy}
                      r={RADIUS}
                      fill="none"
                      stroke={seg.color}
                      strokeWidth={STROKE}
                      strokeDasharray={seg.dashArray}
                      strokeDashoffset={seg.dashOffset}
                      strokeLinecap="butt"
                      className="transition-all duration-300"
                    />
                  ))}
                </g>
              </svg>
              {/* Center text */}
              <div className="absolute inset-0 flex flex-col items-center justify-center pointer-events-none">
                <span className="text-[13px] font-bold text-[#f5f7fa] font-mono leading-tight">
                  {toDecimal(totalValue || '0').toFixed(0).replace(/\B(?=(\d{3})+(?!\d))/g, ',')}
                </span>
                <span className="text-[9px] text-[#94a3b8]">USDT</span>
              </div>
            </div>
          ) : (
            <div
              className="flex-shrink-0 rounded-full border border-[#1e2530] flex items-center justify-center"
              style={{ width: SIZE, height: SIZE }}
            >
              <span className="text-xs text-[#475569]">No data</span>
            </div>
          )}

          {/* Breakdown list */}
          <div className="flex-1 flex flex-col gap-3 min-w-[220px] w-full">
            {allocation.map((item) => (
              <div key={item.asset} className="flex items-center gap-3">
                <span
                  className="w-2.5 h-2.5 rounded-full flex-shrink-0"
                  style={{ backgroundColor: item.hexColor }}
                />
                <span className="text-xs font-semibold text-[#f5f7fa] w-10 flex-shrink-0 whitespace-nowrap">
                  {item.asset}
                </span>
                <div className="flex-1 min-w-[36px] h-1 bg-[#1e2530] rounded-full overflow-hidden">
                  <div
                    className="h-full rounded-full transition-all duration-300"
                    style={{
                      width: `${Math.min(parseFloat(item.percentage), 100)}%`,
                      backgroundColor: item.hexColor,
                    }}
                  />
                </div>
                <span className="text-xs text-[#94a3b8] w-12 text-right flex-shrink-0 font-mono whitespace-nowrap">
                  {parseFloat(item.percentage).toFixed(1)}%
                </span>
                <span className="text-xs text-[#cbd5e1] text-right flex-shrink-0 w-24 font-mono font-medium whitespace-nowrap">
                  {formatUSDT(item.valueUSDT)}
                </span>
              </div>
            ))}
            {allocation.length === 0 && (
              <p className="text-xs text-[#475569] text-center py-4">
                No holdings to display.
              </p>
            )}
          </div>
        </div>
      </div>
    </RaisedSurface>
  )
}
