import { Link } from 'react-router-dom'
import { ArrowRight, ChevronDown, Cpu, Zap, Activity } from 'lucide-react'

const STATS = [
  { icon: <Zap size={14} />, label: 'Starting Balance', value: '10,000 USDT' },
  { icon: <Cpu size={14} />, label: 'Order Matching', value: 'Real-time' },
  { icon: <Activity size={14} />, label: 'Price Data', value: 'Live Charts' },
]

export default function HeroSection() {
  return (
    <section className="relative min-h-screen flex flex-col items-center justify-center text-center px-6 pt-8">
      {/* Badge */}
      <div className="inline-flex items-center gap-2 px-4 py-1.5 rounded-full glass border border-brand/20 text-brand text-xs font-medium mb-8">
        <span className="w-1.5 h-1.5 rounded-full bg-brand animate-pulse" />
        SIMULATION ENVIRONMENT
      </div>

      {/* Headline */}
      <h1 className="text-5xl md:text-7xl font-black tracking-tight text-white leading-[1.05] mb-4">
        Trade Crypto.
        <br />
        <span className="text-gradient glow-text">Risk Free.</span>
      </h1>

      {/* Sub-headline */}
      <p className="max-w-xl text-slate-400 text-lg md:text-xl leading-relaxed mb-10">
        A production-grade exchange simulator. Real mechanics, virtual assets, zero risk.
        Master the markets before you deploy real capital.
      </p>

      {/* CTA buttons */}
      <div className="flex flex-col sm:flex-row items-center gap-4 mb-16">
        <Link
          to="/register"
          className="group flex items-center gap-2 px-7 py-3.5 bg-brand hover:bg-brand-dark text-black font-bold rounded-xl transition-all duration-200 glow-green hover:scale-105"
        >
          Start Simulator
          <ArrowRight size={16} className="group-hover:translate-x-1 transition-transform" />
        </Link>
        <a
          href="#features"
          className="flex items-center gap-2 px-7 py-3.5 border border-surface-border hover:border-brand/50 text-slate-300 hover:text-white font-medium rounded-xl transition-all duration-200"
        >
          Documentation
        </a>
      </div>

      {/* Stats badges */}
      <div className="flex flex-wrap justify-center gap-4 mb-16">
        {STATS.map((s) => (
          <div key={s.value} className="flex items-center gap-2 px-4 py-2 glass-card rounded-lg border border-surface-border">
            <span className="text-brand">{s.icon}</span>
            <div className="text-left">
              <p className="text-white font-bold text-sm">{s.value}</p>
              <p className="text-slate-500 text-xs">{s.label}</p>
            </div>
          </div>
        ))}
      </div>

      {/* Scroll indicator */}
      <a href="#features" className="flex flex-col items-center gap-2 text-slate-600 hover:text-brand transition-colors animate-bounce-slow">
        <span className="text-xs uppercase tracking-widest">Scroll</span>
        <ChevronDown size={18} />
      </a>
    </section>
  )
}
