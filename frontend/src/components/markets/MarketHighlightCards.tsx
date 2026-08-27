interface HighlightCardData {
  category: 'Top Gainer' | '24h Volume Leader' | 'New Listing'
  symbol: string
  price: string
  change?: string
  volumeText: string
  isUp?: boolean
  accentColor: 'emerald' | 'cyan'
  iconBg: string
  iconText: string
  iconChar: string
  sparklinePath: string
}

const HIGHLIGHTS: HighlightCardData[] = [
  {
    category: 'Top Gainer',
    symbol: 'SOL/USDT',
    price: '$188.20',
    change: '+12.45%',
    volumeText: '24h Vol $256.31M',
    isUp: true,
    accentColor: 'emerald',
    iconBg: 'bg-[#00e5ff]/15 border-[#00e5ff]/30',
    iconText: 'text-[#00e5ff]',
    iconChar: 'S',
    sparklinePath: 'M0,32 Q20,28 35,22 T60,20 T80,12 T100,6',
  },
  {
    category: '24h Volume Leader',
    symbol: 'BTC/USDT',
    price: '$96,450.00',
    volumeText: '$1.42B Vol',
    accentColor: 'cyan',
    iconBg: 'bg-[#f7931a]/15 border-[#f7931a]/30',
    iconText: 'text-[#f7931a]',
    iconChar: '₿',
    sparklinePath: 'M0,30 Q25,24 40,26 T70,16 T85,12 T100,4',
  },
  {
    category: 'New Listing',
    symbol: 'SUI/USDT',
    price: '$1.3820',
    change: '+8.20%',
    volumeText: '24h Vol $187.65M',
    isUp: true,
    accentColor: 'emerald',
    iconBg: 'bg-[#3b82f6]/15 border-[#3b82f6]/30',
    iconText: 'text-[#3b82f6]',
    iconChar: '💧',
    sparklinePath: 'M0,35 Q20,30 40,24 T65,18 T85,10 T100,5',
  },
]

export default function MarketHighlightCards() {
  return (
    <div className="grid grid-cols-1 md:grid-cols-3 gap-4 select-none">
      {HIGHLIGHTS.map((card) => {
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
                    <span className="text-[11px] font-mono font-bold text-[#00e676]">
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
