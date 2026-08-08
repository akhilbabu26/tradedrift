const STEPS = [
  { num: '01', title: 'Create Account', desc: 'Register and verify your email in seconds.' },
  { num: '02', title: 'Get 10,000 USDT', desc: 'Your virtual wallet is seeded instantly.' },
  { num: '03', title: 'Place Orders', desc: 'Trade BTC, ETH, SOL with real mechanics.' },
  { num: '04', title: 'Track Portfolio', desc: 'Monitor PnL, positions and trade history.' },
]

export default function HowItWorks() {
  return (
    <section id="how" className="relative py-28 px-6">
      <div className="max-w-5xl mx-auto">
        <div className="text-center mb-16">
          <h2 className="text-4xl md:text-5xl font-black text-white mb-4">
            How It <span className="text-gradient">Works</span>
          </h2>
          <p className="text-slate-400 text-lg">From zero to trading in under 2 minutes.</p>
        </div>

        {/* Steps */}
        <div className="relative">
          {/* Connecting line (desktop) */}
          <div className="hidden md:block absolute top-10 left-[12.5%] right-[12.5%] h-px bg-gradient-to-r from-transparent via-brand/40 to-transparent" />

          <div className="grid md:grid-cols-4 gap-8">
            {STEPS.map((s, i) => (
              <div key={i} className="flex flex-col items-center text-center group">
                {/* Number circle */}
                <div className="relative w-20 h-20 rounded-full glass-card border-2 border-brand/30 flex items-center justify-center mb-6 group-hover:border-brand group-hover:glow-green transition-all duration-300 z-10">
                  <span className="text-2xl font-black text-gradient">{s.num}</span>
                </div>
                <h3 className="text-white font-bold mb-2">{s.title}</h3>
                <p className="text-slate-400 text-sm leading-relaxed">{s.desc}</p>
              </div>
            ))}
          </div>
        </div>
      </div>
    </section>
  )
}
