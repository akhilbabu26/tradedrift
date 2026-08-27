import { useState, useEffect, useMemo } from 'react'
import { marketApi, type Market, type Ticker24h } from '../../api/market'
import { wsService } from '../../api/ws'

interface HighlightCardProps {
  markets?: Market[]
  tickers?: Record<string, Ticker24h>
}

export default function MarketHighlightCards({ markets = [], tickers: initialTickers = {} }: HighlightCardProps) {
  const [liveTickers, setLiveTickers] = useState<Record<string, Ticker24h>>(initialTickers)

  useEffect(() => {
    setLiveTickers((prev) => ({ ...prev, ...initialTickers }))
  }, [initialTickers])

  useEffect(() => {
    // If no markets were passed in props, fetch them directly
    if (markets.length === 0) {
      marketApi.getMarkets().then((mList) => {
        mList.forEach((m) => {
          marketApi.getTicker(m.id).then((t) => {
            if (t && t.last_price) {
              setLiveTickers((prev) => ({ ...prev, [m.id]: t }))
            }
          }).catch(() => {})
        })
      }).catch(() => {})
    }

    // Subscribe to live tickers
    const unsubs = ['BTC-USDT', 'ETH-USDT', 'SOL-USDT'].map((id) =>
      wsService.subscribe(`market:ticker:${id}`, (data: Ticker24h) => {
        if (data && data.market_id) {
          setLiveTickers((prev) => ({ ...prev, [data.market_id]: data }))
        }
      })
    )

    return () => {
      unsubs.forEach((u) => u())
    }
  }, [markets])

  const { topGainer, volumeLeader, newListing } = useMemo(() => {
    const tickerEntries = Object.values(liveTickers)

    // Sort by 24h change for Top Gainer
    const sortedByGain = [...tickerEntries].sort((a, b) => {
      const gA = parseFloat(a.price_change_24h_percent || '0')
      const gB = parseFloat(b.price_change_24h_percent || '0')
      return gB - gA
    })

    // Sort by quote volume for Volume Leader
    const sortedByVolume = [...tickerEntries].sort((a, b) => {
      const vA = parseFloat(a.quote_volume_24h || a.volume_24h || '0')
      const vB = parseFloat(b.quote_volume_24h || b.volume_24h || '0')
      return vB - vA
    })

    const gainerTicker = sortedByGain[0] || liveTickers['SOL-USDT']
    const volumeTicker = sortedByVolume[0] || liveTickers['BTC-USDT']
    const newTicker = liveTickers['SOL-USDT'] || liveTickers['ETH-USDT']

    return {
      topGainer: gainerTicker,
      volumeLeader: volumeTicker,
      newListing: newTicker,
    }
  }, [liveTickers])

  const cards = [
    {
      category: 'Top Gainer',
      symbol: topGainer?.market_id?.replace('-', '/') || 'SOL/USDT',
      price: topGainer?.last_price
        ? `$${parseFloat(topGainer.last_price).toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
        : '--',
      change: topGainer?.price_change_24h_percent
        ? `${parseFloat(topGainer.price_change_24h_percent) >= 0 ? '+' : ''}${parseFloat(topGainer.price_change_24h_percent).toFixed(2)}%`
        : '--',
      volumeText: topGainer?.quote_volume_24h
        ? `24h Vol $${(parseFloat(topGainer.quote_volume_24h) / 1e6).toFixed(2)}M`
        : 'Live Stream',
      isUp: (parseFloat(topGainer?.price_change_24h_percent || '0')) >= 0,
      accentColor: 'emerald' as const,
      iconBg: 'bg-[#00e5ff]/15 border-[#00e5ff]/30',
      iconText: 'text-[#00e5ff]',
      iconChar: 'S',
      sparklinePath: 'M0,32 Q20,28 35,22 T60,20 T80,12 T100,6',
    },
    {
      category: '24h Volume Leader',
      symbol: volumeLeader?.market_id?.replace('-', '/') || 'BTC/USDT',
      price: volumeLeader?.last_price
        ? `$${parseFloat(volumeLeader.last_price).toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
        : '--',
      change: volumeLeader?.price_change_24h_percent
        ? `${parseFloat(volumeLeader.price_change_24h_percent) >= 0 ? '+' : ''}${parseFloat(volumeLeader.price_change_24h_percent).toFixed(2)}%`
        : undefined,
      volumeText: volumeLeader?.quote_volume_24h
        ? `$${(parseFloat(volumeLeader.quote_volume_24h) / 1e6).toFixed(2)}M Vol`
        : 'Leading Pair',
      isUp: (parseFloat(volumeLeader?.price_change_24h_percent || '0')) >= 0,
      accentColor: 'cyan' as const,
      iconBg: 'bg-[#f7931a]/15 border-[#f7931a]/30',
      iconText: 'text-[#f7931a]',
      iconChar: '₿',
      sparklinePath: 'M0,30 Q25,24 40,26 T70,16 T85,12 T100,4',
    },
    {
      category: 'New Listing',
      symbol: newListing?.market_id?.replace('-', '/') || 'SOL/USDT',
      price: newListing?.last_price
        ? `$${parseFloat(newListing.last_price).toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
        : '--',
      change: newListing?.price_change_24h_percent
        ? `${parseFloat(newListing.price_change_24h_percent) >= 0 ? '+' : ''}${parseFloat(newListing.price_change_24h_percent).toFixed(2)}%`
        : undefined,
      volumeText: newListing?.quote_volume_24h
        ? `24h Vol $${(parseFloat(newListing.quote_volume_24h) / 1e6).toFixed(2)}M`
        : 'Active Spot Market',
      isUp: (parseFloat(newListing?.price_change_24h_percent || '0')) >= 0,
      accentColor: 'emerald' as const,
      iconBg: 'bg-[#00e676]/15 border-[#00e676]/30',
      iconText: 'text-[#00e676]',
      iconChar: '⚡',
      sparklinePath: 'M0,35 Q20,30 40,24 T65,18 T85,10 T100,5',
    },
  ]

  return (
    <div className="grid grid-cols-1 md:grid-cols-3 gap-4 select-none">
      {cards.map((card) => {
        const isCyan = card.accentColor === 'cyan'
        const strokeColor = isCyan ? '#00e5ff' : '#00e676'
        const gradientId = `spark-${card.category.replace(/\s+/g, '-').toLowerCase()}`

        return (
          <div
            key={card.category}
            className="bg-[#0e121b] border border-[#1b2230] rounded-xl p-5 relative overflow-hidden flex flex-col justify-between shadow-xl group hover:border-[#1b2230]/80 transition-all"
          >
            {/* Card Header Label */}
            <div className="text-[11px] font-sans font-medium text-slate-400">
              {card.category}
            </div>

            {/* Main Stats & Sparkline */}
            <div className="flex items-center justify-between mt-3">
              {/* Left Details */}
              <div className="flex flex-col">
                <div className="flex items-center gap-2 mb-1.5">
                  <div
                    className={`w-6 h-6 rounded-full flex items-center justify-center font-bold text-xs flex-shrink-0 border ${card.iconBg} ${card.iconText}`}
                  >
                    {card.iconChar}
                  </div>
                  <span className="font-bold text-white font-sans text-xs tracking-tight">
                    {card.symbol}
                  </span>
                </div>

                <div className="flex items-baseline gap-2">
                  <span className="text-xl lg:text-2xl font-black font-mono text-white tracking-tight">
                    {card.price}
                  </span>
                  {card.change && (
                    <span
                      className={`text-[11px] font-mono font-bold ${
                        card.isUp ? 'text-[#00e676]' : 'text-[#ff3366]'
                      }`}
                    >
                      {card.change}
                    </span>
                  )}
                </div>

                <div className="text-[11px] font-mono text-slate-400 mt-1">
                  {card.volumeText}
                </div>
              </div>

              {/* Right Sparkline SVG */}
              <div className="w-32 h-14 flex-shrink-0">
                <svg className="w-full h-full" viewBox="0 0 100 40" preserveAspectRatio="none">
                  <defs>
                    <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stopColor={strokeColor} stopOpacity="0.35" />
                      <stop offset="100%" stopColor={strokeColor} stopOpacity="0.0" />
                    </linearGradient>
                  </defs>
                  <path
                    d={`${card.sparklinePath} L100,40 L0,40 Z`}
                    fill={`url(#${gradientId})`}
                  />
                  <path
                    d={card.sparklinePath}
                    fill="none"
                    stroke={strokeColor}
                    strokeWidth="2.2"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  />
                </svg>
              </div>
            </div>
          </div>
        )
      })}
    </div>
  )
}
