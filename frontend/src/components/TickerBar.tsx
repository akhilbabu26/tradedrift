const TICKERS = [
  { symbol: 'BTC/USDT',  price: '67,842.50', change: '+2.34%',  up: true },
  { symbol: 'ETH/USDT',  price: '3,521.80',  change: '+1.87%',  up: true },
  { symbol: 'SOL/USDT',  price: '185.42',    change: '-0.62%',  up: false },
  { symbol: 'BNB/USDT',  price: '598.20',    change: '+3.11%',  up: true },
  { symbol: 'XRP/USDT',  price: '0.6123',    change: '+0.94%',  up: true },
  { symbol: 'ADA/USDT',  price: '0.4587',    change: '-1.23%',  up: false },
  { symbol: 'AVAX/USDT', price: '38.91',     change: '+4.52%',  up: true },
  { symbol: 'DOT/USDT',  price: '7.834',     change: '-0.38%',  up: false },
]

export default function TickerBar() {
  const doubled = [...TICKERS, ...TICKERS] // seamless loop

  return (
    <div className="relative w-full overflow-hidden bg-surface-muted border-b border-surface-border py-2 z-40 mt-16">
      <div className="flex animate-ticker whitespace-nowrap">
        {doubled.map((t, i) => (
          <span key={i} className="inline-flex items-center gap-2 px-6 text-xs font-mono">
            <span className="text-slate-400">{t.symbol}</span>
            <span className="text-white font-semibold">${t.price}</span>
            <span className={t.up ? 'text-brand' : 'text-red-400'}>{t.change}</span>
            <span className="text-surface-border mx-2">|</span>
          </span>
        ))}
      </div>
    </div>
  )
}
