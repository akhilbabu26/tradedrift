import { ShieldCheck, Wallet, BarChart3 } from 'lucide-react'

const FEATURES = [
  {
    icon: <ShieldCheck size={28} />,
    title: 'Real Authentication',
    description:
      'JWT-secured sessions, OTP email verification, device management, and session revocation. Exchange-grade security on every request.',
    tech: 'Go · Auth Service · Redis',
  },
  {
    icon: <Wallet size={28} />,
    title: 'Virtual Wallet',
    description:
      'Start with 10,000 USDT. Track available and reserved balances across 4 assets with a full transaction ledger.',
    tech: 'PostgreSQL · gRPC',
  },
  {
    icon: <BarChart3 size={28} />,
    title: 'Live Order Book',
    description:
      'Place market and limit orders against a real matching engine. Experience sub-millisecond order matching.',
    tech: 'Kafka · Matching Engine',
  },
]

export default function FeaturesSection() {
  return (
    <section id="features" className="relative py-28 px-6">
      <div className="max-w-6xl mx-auto">
        {/* Header */}
        <div className="text-center mb-16">
          <h2 className="text-4xl md:text-5xl font-black text-white mb-4">
            Engineered for <span className="text-gradient">Precision</span>
          </h2>
          <p className="text-slate-400 text-lg max-w-xl mx-auto">
            Experience a trading environment built with enterprise-grade microservices.
          </p>
        </div>

        {/* Cards */}
        <div className="grid md:grid-cols-3 gap-6">
          {FEATURES.map((f, i) => (
            <div
              key={i}
              className="group glass-card rounded-2xl p-8 border border-surface-border hover:border-brand/30 transition-all duration-300 hover:-translate-y-1 hover:glow-green-sm"
            >
              <div className="w-14 h-14 rounded-xl bg-brand/10 border border-brand/20 flex items-center justify-center text-brand mb-6 group-hover:bg-brand/20 transition-colors">
                {f.icon}
              </div>
              <h3 className="text-xl font-bold text-white mb-3">{f.title}</h3>
              <p className="text-slate-400 text-sm leading-relaxed mb-6">{f.description}</p>
              <p className="text-xs text-brand/60 font-mono tracking-wide">Tech: {f.tech}</p>
            </div>
          ))}
        </div>
      </div>
    </section>
  )
}
