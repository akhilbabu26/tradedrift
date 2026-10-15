export interface TradeDriftSymbolProps {
  size?: number | string
  className?: string
  ariaLabel?: string
  ariaHidden?: boolean
}

/**
 * TradeDrift Minimalist Silver Geometric TD Monogram
 *
 * Sourced directly from /TradeDrift/tradedrift-monogram.svg:
 * - Geometric interlocking 'T' and 'D' glyphs
 * - Crisp transparent background and 100x100 viewBox proportions
 * - Sleek metallic silver gradient: #F4F5F6 -> #D9DADD -> #AEB2B8
 * - Precision vector paths with transparent counter
 */
export function TradeDriftSymbol({
  size = 28,
  className = '',
  ariaLabel = 'TradeDrift Logo',
  ariaHidden = true,
}: TradeDriftSymbolProps) {
  return (
    <svg
      viewBox="0 0 100 100"
      width={size}
      height={size}
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      className={`flex-shrink-0 transition-transform ${className}`}
      aria-label={ariaHidden ? undefined : ariaLabel}
      aria-hidden={ariaHidden ? 'true' : undefined}
      role={ariaHidden ? undefined : 'img'}
    >
      <defs>
        <linearGradient id="silver-td-monogram" x1="0" y1="0" x2="0.9" y2="1">
          <stop offset="0" stopColor="#F4F5F6" />
          <stop offset="0.48" stopColor="#D9DADD" />
          <stop offset="1" stopColor="#AEB2B8" />
        </linearGradient>
      </defs>
      <g fill="url(#silver-td-monogram)">
        {/* T crossbar and stem */}
        <path d="M19 27.5 Q19 22.5 24 22.5 H69.5 Q75.5 22.5 80 28 L84 33 H46.5 V67 Q46.5 71 42.5 71 H38.5 V33 H24 Q19 33 19 28.5 Z" />
        {/* D bowl; transparent counter keeps mark crisp at all sizes */}
        <path
          fillRule="evenodd"
          d="M49.5 36.5 H62.5 C76.5 36.5 83 45 83 55 C83 66.5 75 73 62.5 73 H49.5 Z M58 43.5 V66 H62.5 C71 66 76 61.5 76 55 C76 48 71.5 43.5 62.5 43.5 Z"
        />
        {/* Small lower terminal on the T stem */}
        <path d="M38.5 73.5 H46.5 V77 Q46.5 81 42.5 83 L38.5 85 Z" />
      </g>
    </svg>
  )
}

export interface TradeDriftWordmarkProps {
  size?: 'sm' | 'md' | 'lg'
  showDescriptor?: boolean
  showTagline?: boolean
  className?: string
}

export function TradeDriftWordmark({
  size = 'md',
  showDescriptor = true,
  showTagline = false,
  className = '',
}: TradeDriftWordmarkProps) {
  const isSm = size === 'sm'
  const isLg = size === 'lg'

  return (
    <div className={`flex flex-col select-none leading-none ${className}`}>
      {/* Brand Name */}
      <span
        className={`font-bold tracking-tight text-[#f5f7fa] ${
          isSm ? 'text-xs' : isLg ? 'text-xl' : 'text-sm'
        }`}
      >
        TradeDrift
      </span>

      {/* Brand Descriptor: MARKETS · EXECUTION · INTELLIGENCE */}
      {showDescriptor && (
        <span
          className={`font-semibold uppercase text-[#10b981] tracking-[0.14em] mt-0.5 ${
            isSm ? 'text-[8px]' : isLg ? 'text-[10px]' : 'text-[9px]'
          }`}
        >
          MARKETS · EXECUTION · INTELLIGENCE
        </span>
      )}

      {/* Tagline */}
      {showTagline && (
        <span className="text-xs sm:text-sm text-slate-400 mt-1 font-normal leading-relaxed">
          Explore markets. Execute trades. Understand the engine.
        </span>
      )}
    </div>
  )
}

export interface TradeDriftLogoProps {
  variant?: 'navbar' | 'footer' | 'auth' | 'compact' | 'symbol'
  className?: string
  symbolSize?: number
}

/**
 * Universal TradeDrift Logo & Brand Lockup
 */
export default function TradeDriftLogo({
  variant = 'navbar',
  className = '',
  symbolSize,
}: TradeDriftLogoProps) {
  if (variant === 'symbol') {
    return (
      <TradeDriftSymbol
        size={symbolSize || 32}
        className={className}
        ariaHidden={false}
      />
    )
  }

  if (variant === 'navbar') {
    return (
      <div className={`flex items-center gap-2.5 ${className}`}>
        <TradeDriftSymbol size={symbolSize || 28} />
        <TradeDriftWordmark size="md" showDescriptor={true} />
      </div>
    )
  }

  if (variant === 'footer') {
    return (
      <div className={`flex items-start gap-3.5 sm:gap-4 ${className}`}>
        <div className="w-10 h-10 rounded-lg bg-[#0e131d] border border-[#1e2530] flex items-center justify-center flex-shrink-0 shadow-sm p-1">
          <TradeDriftSymbol size={symbolSize || 32} />
        </div>
        <div className="flex flex-col">
          <h2 className="text-lg font-bold text-white tracking-tight leading-tight">TradeDrift</h2>
          <p className="text-xs font-semibold text-[#10b981] tracking-[0.14em] uppercase mt-0.5">
            MARKETS · EXECUTION · INTELLIGENCE
          </p>
          <p className="text-xs sm:text-sm text-slate-400 mt-1 max-w-3xl leading-relaxed">
            Explore markets. Execute trades. Understand the engine.
          </p>
        </div>
      </div>
    )
  }

  if (variant === 'auth') {
    return (
      <div className={`flex flex-col items-center text-center gap-3 ${className}`}>
        <div className="w-14 h-14 rounded-2xl bg-[#0e131d] border border-[#1e2530] flex items-center justify-center shadow-lg p-2 hover:border-zinc-500/40 transition-colors">
          <TradeDriftSymbol size={symbolSize || 40} />
        </div>
        <div className="flex flex-col items-center">
          <span className="text-xl font-bold tracking-tight text-white">TradeDrift</span>
          <span className="text-[10px] font-semibold tracking-[0.16em] uppercase text-[#10b981] mt-0.5">
            MARKETS · EXECUTION · INTELLIGENCE
          </span>
        </div>
      </div>
    )
  }

  // Default compact
  return (
    <div className={`flex items-center gap-2 ${className}`}>
      <TradeDriftSymbol size={symbolSize || 24} />
      <span className="text-sm font-bold text-white tracking-tight">TradeDrift</span>
    </div>
  )
}
