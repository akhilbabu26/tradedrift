export default function DashboardPreview() {
  return (
    <section className="relative py-28 px-6 overflow-hidden">
      <div className="max-w-5xl mx-auto text-center">
        <h2 className="text-4xl md:text-5xl font-black text-white mb-4">
          Immersive <span className="text-gradient">Trading Dashboard</span>
        </h2>
        <p className="text-slate-400 text-lg mb-16 max-w-xl mx-auto">
          A real exchange interface. Track prices, place orders, and manage your portfolio — all in one view.
        </p>

        {/* Floating image mockup */}
        <div className="relative mx-auto animate-float max-w-4xl">
          {/* Outer glow ring */}
          <div className="absolute -inset-1 rounded-2xl opacity-40 blur-xl"
            style={{ background: 'linear-gradient(135deg, #10b981 0%, #6366f1 100%)' }} />

          {/* Image container */}
          <div className="relative rounded-2xl overflow-hidden border border-brand/20 glow-green shadow-2xl">
            {/* Browser-like top bar */}
            <div className="flex items-center gap-2 px-4 py-2.5 bg-surface-card border-b border-surface-border">
              <div className="flex gap-1.5">
                <div className="w-3 h-3 rounded-full bg-red-500/70" />
                <div className="w-3 h-3 rounded-full bg-yellow-500/70" />
                <div className="w-3 h-3 rounded-full bg-brand/70" />
              </div>
              <div className="flex-1 mx-4">
                <div className="bg-surface-muted rounded-md px-3 py-1 text-xs text-slate-500 font-mono text-left max-w-xs mx-auto">
                  app.tradedrift.io/dashboard
                </div>
              </div>
            </div>

            {/* Dashboard screenshot */}
            <img
              src="/dashboard-preview.png"
              alt="TradeDrift Trading Dashboard"
              className="w-full h-auto block object-contain"
              style={{ imageRendering: 'crisp-edges', maxWidth: '100%' }}
              loading="lazy"
            />
          </div>

          {/* Floating stat badge — top right */}
          <div className="absolute -top-4 -right-4 glass-card border border-brand/30 rounded-xl px-4 py-2.5 glow-green-sm hidden md:block">
            <p className="text-brand text-xs font-mono">▲ +2.45%</p>
            <p className="text-white text-sm font-bold">BTC/USDT</p>
          </div>

          {/* Floating stat badge — bottom left */}
          <div className="absolute -bottom-4 -left-4 glass-card border border-surface-border rounded-xl px-4 py-2.5 hidden md:block">
            <p className="text-slate-400 text-xs">Total Balance</p>
            <p className="text-white text-sm font-bold">$33,909.80</p>
          </div>
        </div>

        {/* Sub-label */}
        <p className="mt-20 text-slate-600 text-sm font-mono">
          Real-time candlestick charts · Order book · Portfolio tracker · Asset management
        </p>
      </div>
    </section>
  )
}
