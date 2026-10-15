import { useState, useRef, useMemo, useEffect } from 'react'
import type { OrderBookSnapshot } from '../../types/trade'
import { formatPrice, formatQuantity } from '../../utils/formatters'

interface MarketDepthChartProps {
  orderBook: OrderBookSnapshot
  baseAsset: string
  quoteAsset: string
  lastPrice: string
  onSelectPrice?: (price: string) => void
}

interface HoverState {
  x: number
  y: number
  price: string
  volume: string
  totalUsdt: string
  side: 'bid' | 'ask'
}

export default function MarketDepthChart({
  orderBook,
  baseAsset,
  lastPrice,
  onSelectPrice,
}: MarketDepthChartProps) {
  const plotRef = useRef<HTMLDivElement>(null)
  const [dimensions, setDimensions] = useState({ width: 280, height: 200 })
  const [hover, setHover] = useState<HoverState | null>(null)

  // Track the actual plot container dimensions responsively
  useEffect(() => {
    if (!plotRef.current) return
    const ro = new ResizeObserver((entries) => {
      for (const entry of entries) {
        const { width, height } = entry.contentRect
        if (width > 0 && height > 0) {
          setDimensions({
            width: Math.floor(width),
            height: Math.floor(height),
          })
        }
      }
    })
    ro.observe(plotRef.current)
    return () => ro.disconnect()
  }, [])

  const { width, height } = dimensions

  // Prepare bid and ask cumulative series within the unified plot coordinate system
  const {
    bidPoints,
    askPoints,
    bidPath,
    askPath,
    bidArea,
    askArea,
    bidTotal,
    askTotal,
    bidRatio,
    askRatio,
    lowestBid,
    highestAsk,
    midPrice,
    baselineY,
    topPadding,
  } = useMemo(() => {
    const rawBids = orderBook.bids || []
    const rawAsks = orderBook.asks || []

    const lastNum = parseFloat(lastPrice || orderBook.lastPrice || '0')
    const bestBid = rawBids.length > 0 ? parseFloat(rawBids[0].price) : 0
    const bestAsk = rawAsks.length > 0 ? parseFloat(rawAsks[0].price) : 0
    const calculatedMid = bestBid > 0 && bestAsk > 0 ? (bestBid + bestAsk) / 2 : lastNum

    const topPad = 14
    const bottomPad = 12
    const baseLine = Math.max(30, height - bottomPad)
    const chartHeight = Math.max(30, baseLine - topPad)
    const midX = width / 2
    const gap = 2

    if (rawBids.length === 0 && rawAsks.length === 0) {
      return {
        bidPoints: [],
        askPoints: [],
        bidPath: '',
        askPath: '',
        bidArea: '',
        askArea: '',
        bidTotal: 0,
        askTotal: 0,
        bidRatio: 50,
        askRatio: 50,
        lowestBid: '0.00',
        highestAsk: '0.00',
        midPrice: calculatedMid,
        baselineY: baseLine,
        topPadding: topPad,
      }
    }

    // Sort bids from lowest price to highest price (left to center / mid-price)
    const sortedBids = [...rawBids].sort((a, b) => parseFloat(a.price) - parseFloat(b.price))
    // Sort asks from lowest price to highest price (center / mid-price to right)
    const sortedAsks = [...rawAsks].sort((a, b) => parseFloat(a.price) - parseFloat(b.price))

    const maxBidVol = sortedBids.length > 0 ? parseFloat(sortedBids[0].total) : 0
    const maxAskVol = sortedAsks.length > 0 ? parseFloat(sortedAsks[sortedAsks.length - 1].total) : 0
    // Give 8% headroom so highest volume stair never clips the top border
    const maxVol = Math.max(maxBidVol, maxAskVol, 0.0001) * 1.08

    // Scale volume to Y coordinate within plot boundaries [topPad, baseLine]
    const getY = (vol: number) => {
      const normalized = Math.min(1, Math.max(0, vol / maxVol))
      return topPad + chartHeight * (1 - normalized)
    }

    // ── Generate Bids points (leftX: 8 to rightX: midX - gap) ─────────────
    const bPoints: { x: number; y: number; price: string; volume: string }[] = []
    const bLeftX = 8
    const bRightX = midX - gap

    if (sortedBids.length > 0) {
      const stepX = sortedBids.length > 1 ? (bRightX - bLeftX) / (sortedBids.length - 1) : 0
      sortedBids.forEach((b, i) => {
        const x = bLeftX + i * stepX
        const vol = parseFloat(b.total)
        const y = getY(vol)
        bPoints.push({ x, y, price: b.price, volume: b.total })
      })
    }

    // ── Generate Asks points (leftX: midX + gap to rightX: width - 8) ──────
    const aPoints: { x: number; y: number; price: string; volume: string }[] = []
    const aLeftX = midX + gap
    const aRightX = width - 8

    if (sortedAsks.length > 0) {
      const stepX = sortedAsks.length > 1 ? (aRightX - aLeftX) / (sortedAsks.length - 1) : 0
      sortedAsks.forEach((a, i) => {
        const x = aLeftX + i * stepX
        const vol = parseFloat(a.total)
        const y = getY(vol)
        aPoints.push({ x, y, price: a.price, volume: a.total })
      })
    }

    // ── Stepped paths closing accurately against baselineY ─────────────────
    let bPath = ''
    let bArea = ''
    if (bPoints.length > 0) {
      bPath = `M ${bPoints[0].x} ${bPoints[0].y}`
      for (let i = 1; i < bPoints.length; i++) {
        bPath += ` H ${bPoints[i].x} V ${bPoints[i].y}`
      }
      // Step to center divider and drop down to baseline
      bPath += ` H ${bRightX} V ${baseLine}`
      // Area closes along the baseline back to the start
      bArea = `${bPath} H ${bPoints[0].x} V ${bPoints[0].y} Z`
    }

    let aPath = ''
    let aArea = ''
    if (aPoints.length > 0) {
      // Starts at baseline at center divider, steps up to first ask level
      aPath = `M ${aLeftX} ${baseLine} V ${aPoints[0].y}`
      for (let i = 1; i < aPoints.length; i++) {
        aPath += ` H ${aPoints[i].x} V ${aPoints[i].y}`
      }
      // Extend to right edge
      aPath += ` H ${aRightX}`
      // Area drops to baseline and closes along baseline back to center
      aArea = `${aPath} V ${baseLine} H ${aLeftX} Z`
    }

    const totalCombined = maxBidVol + maxAskVol || 1
    const bRatio = Math.round((maxBidVol / totalCombined) * 100)
    const aRatio = 100 - bRatio

    return {
      bidPoints: bPoints,
      askPoints: aPoints,
      bidPath: bPath,
      askPath: aPath,
      bidArea: bArea,
      askArea: aArea,
      bidTotal: maxBidVol,
      askTotal: maxAskVol,
      bidRatio: bRatio,
      askRatio: aRatio,
      lowestBid: sortedBids[0]?.price || '0.00',
      highestAsk: sortedAsks[sortedAsks.length - 1]?.price || '0.00',
      midPrice: calculatedMid,
      baselineY: baseLine,
      topPadding: topPad,
    }
  }, [orderBook, lastPrice, width, height])

  // Mouse move handler for interactive crosshair & inspection
  const handleMouseMove = (e: React.MouseEvent<SVGSVGElement>) => {
    const rect = e.currentTarget.getBoundingClientRect()
    const mouseX = e.clientX - rect.left

    const midX = width / 2
    if (mouseX <= midX && bidPoints.length > 0) {
      let closest = bidPoints[0]
      let minDist = Math.abs(mouseX - closest.x)
      for (const pt of bidPoints) {
        const d = Math.abs(mouseX - pt.x)
        if (d < minDist) {
          minDist = d
          closest = pt
        }
      }
      const priceNum = parseFloat(closest.price)
      const volNum = parseFloat(closest.volume)
      setHover({
        x: closest.x,
        y: closest.y,
        price: closest.price,
        volume: closest.volume,
        totalUsdt: (priceNum * volNum).toFixed(2),
        side: 'bid',
      })
    } else if (mouseX > midX && askPoints.length > 0) {
      let closest = askPoints[0]
      let minDist = Math.abs(mouseX - closest.x)
      for (const pt of askPoints) {
        const d = Math.abs(mouseX - pt.x)
        if (d < minDist) {
          minDist = d
          closest = pt
        }
      }
      const priceNum = parseFloat(closest.price)
      const volNum = parseFloat(closest.volume)
      setHover({
        x: closest.x,
        y: closest.y,
        price: closest.price,
        volume: closest.volume,
        totalUsdt: (priceNum * volNum).toFixed(2),
        side: 'ask',
      })
    }
  }

  const handleMouseLeave = () => {
    setHover(null)
  }

  if (bidPoints.length === 0 && askPoints.length === 0) {
    return (
      <div className="flex-1 flex flex-col items-center justify-center p-6 text-center select-none">
        <div className="w-10 h-10 rounded-full bg-[#1e2530] flex items-center justify-center mb-2">
          <span className="text-slate-500 text-sm">📊</span>
        </div>
        <p className="text-xs text-slate-400 font-medium">Waiting for market depth data</p>
        <p className="text-[10px] text-slate-600 mt-0.5">Live bid & ask walls will render automatically</p>
      </div>
    )
  }

  return (
    <div className="relative flex-1 min-h-0 w-full flex flex-col select-none overflow-hidden bg-[#0d0e12]">
      {/* ── Top Depth Sentiment Bar ────────────────────────────────────────── */}
      <div className="flex-shrink-0 px-2.5 py-1.5 border-b border-[#1e2530]/60 bg-[#0a0b0e]/70 flex flex-col gap-1">
        <div className="flex items-center justify-between text-[10px] font-mono">
          <span className="text-[#10b981] font-semibold flex items-center gap-1">
            <span className="w-1.5 h-1.5 rounded-full bg-[#10b981]" />
            Bids {bidRatio}% ({formatQuantity(bidTotal.toString(), 2)} {baseAsset})
          </span>
          <span className="text-[#ef4444] font-semibold flex items-center gap-1">
            Asks {askRatio}% ({formatQuantity(askTotal.toString(), 2)} {baseAsset})
            <span className="w-1.5 h-1.5 rounded-full bg-[#ef4444]" />
          </span>
        </div>

        {/* Visual Ratio Bar */}
        <div className="w-full h-1 bg-[#1e2530] rounded-full overflow-hidden flex">
          <div
            style={{ width: `${bidRatio}%` }}
            className="h-full bg-[#10b981] transition-all duration-300"
          />
          <div
            style={{ width: `${askRatio}%` }}
            className="h-full bg-[#ef4444] transition-all duration-300"
          />
        </div>
      </div>

      {/* ── Middle: Depth SVG Plot Area ────────────────────────────────────── */}
      <div ref={plotRef} className="relative flex-1 min-h-0 w-full overflow-hidden">
        <svg
          width={width}
          height={height}
          viewBox={`0 0 ${width} ${height}`}
          className="block w-full h-full cursor-crosshair"
          onMouseMove={handleMouseMove}
          onMouseLeave={handleMouseLeave}
          onClick={() => {
            if (hover && onSelectPrice) {
              onSelectPrice(hover.price)
            }
          }}
        >
          <defs>
            {/* Bid Gradient */}
            <linearGradient id="depthBidGrad" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="#10b981" stopOpacity="0.32" />
              <stop offset="70%" stopColor="#10b981" stopOpacity="0.10" />
              <stop offset="100%" stopColor="#10b981" stopOpacity="0.02" />
            </linearGradient>

            {/* Ask Gradient */}
            <linearGradient id="depthAskGrad" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="#ef4444" stopOpacity="0.32" />
              <stop offset="70%" stopColor="#ef4444" stopOpacity="0.10" />
              <stop offset="100%" stopColor="#ef4444" stopOpacity="0.02" />
            </linearGradient>
          </defs>

          {/* Baseline Grid Line */}
          <line
            x1="8"
            y1={baselineY}
            x2={width - 8}
            y2={baselineY}
            stroke="#1e2530"
            strokeWidth="1"
          />

          {/* Center Reference Line (Mid Price) */}
          <line
            x1={width / 2}
            y1={topPadding}
            x2={width / 2}
            y2={baselineY}
            stroke="rgba(255,255,255,0.15)"
            strokeDasharray="3 3"
            strokeWidth="1"
          />

          {/* Bid Area & Path */}
          {bidArea && <path d={bidArea} fill="url(#depthBidGrad)" />}
          {bidPath && <path d={bidPath} fill="none" stroke="#10b981" strokeWidth="1.75" />}

          {/* Ask Area & Path */}
          {askArea && <path d={askArea} fill="url(#depthAskGrad)" />}
          {askPath && <path d={askPath} fill="none" stroke="#ef4444" strokeWidth="1.75" />}

          {/* Hover Crosshair & Anchor */}
          {hover && (
            <>
              {/* Vertical line from top to baseline */}
              <line
                x1={hover.x}
                y1={topPadding}
                x2={hover.x}
                y2={baselineY}
                stroke={hover.side === 'bid' ? '#10b981' : '#ef4444'}
                strokeWidth="1"
                strokeDasharray="2 2"
                opacity="0.8"
              />
              {/* Target dot on curve */}
              <circle
                cx={hover.x}
                cy={hover.y}
                r="4"
                fill={hover.side === 'bid' ? '#10b981' : '#ef4444'}
                stroke="#0a0b0e"
                strokeWidth="2"
              />
            </>
          )}
        </svg>

        {/* Floating Tooltip Pill */}
        {hover && (
          <div
            className="absolute pointer-events-none z-20 px-2.5 py-1.5 rounded-lg bg-[#111318]/95 border border-[#1e2530] shadow-xl backdrop-blur-xs text-[10px] font-mono whitespace-nowrap transition-all duration-75"
            style={{
              left: Math.min(Math.max(10, hover.x - 60), width - 130),
              top: Math.max(8, hover.y - 56),
            }}
          >
            <div className="flex items-center gap-1.5 font-bold mb-0.5">
              <span
                className={`w-1.5 h-1.5 rounded-full ${
                  hover.side === 'bid' ? 'bg-[#10b981]' : 'bg-[#ef4444]'
                }`}
              />
              <span className={hover.side === 'bid' ? 'text-[#10b981]' : 'text-[#ef4444]'}>
                {hover.side === 'bid' ? 'Buy Wall' : 'Sell Wall'}
              </span>
              <span className="text-white">${formatPrice(hover.price)}</span>
            </div>
            <div className="text-slate-400 flex items-center justify-between gap-3 text-[9px]">
              <span>Cum. Vol:</span>
              <span className="text-slate-200">{hover.volume} {baseAsset}</span>
            </div>
            <div className="text-slate-400 flex items-center justify-between gap-3 text-[9px]">
              <span>Total:</span>
              <span className="text-slate-200">${formatPrice(hover.totalUsdt)}</span>
            </div>
          </div>
        )}
      </div>

      {/* ── Bottom X-Axis Price Labels ───────────────────────────────────────── */}
      <div className="flex-shrink-0 h-6 px-2.5 relative flex items-center justify-between text-[9px] font-mono border-t border-[#1e2530]/60 bg-[#0a0b0e]/95 select-none overflow-hidden">
        {/* Left: Bid boundary price */}
        <div className="flex items-center justify-start max-w-[32%] truncate">
          <span className="text-[#10b981]/90 font-medium truncate" title={`Deepest Bid: ${lowestBid}`}>
            ${formatPrice(lowestBid)}
          </span>
        </div>

        {/* Center: Reference / Mid price locked at exact 50% coordinate */}
        <div
          className="absolute left-1/2 -translate-x-1/2 flex items-center justify-center pointer-events-none"
          title={`Current Reference Price: ${midPrice}`}
        >
          <span className="px-1.5 py-0.5 rounded bg-[#1e2530]/90 border border-slate-700/60 text-slate-200 font-semibold shadow-xs whitespace-nowrap">
            ${formatPrice(midPrice.toString())}
          </span>
        </div>

        {/* Right: Ask boundary price */}
        <div className="flex items-center justify-end max-w-[32%] truncate ml-auto">
          <span className="text-[#ef4444]/90 font-medium truncate" title={`Deepest Ask: ${highestAsk}`}>
            ${formatPrice(highestAsk)}
          </span>
        </div>
      </div>
    </div>
  )
}
