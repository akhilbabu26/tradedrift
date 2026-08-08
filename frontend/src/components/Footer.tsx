import { Link } from 'react-router-dom'
import { ArrowRight, TrendingUp } from 'lucide-react'

export default function Footer() {
  return (
    <>
      {/* Bottom CTA */}
      <section className="relative py-28 px-6">
        <div className="max-w-3xl mx-auto text-center">
          {/* Radial glow */}
          <div className="absolute inset-0 flex items-center justify-center pointer-events-none">
            <div className="w-96 h-96 rounded-full opacity-20"
              style={{ background: 'radial-gradient(circle, #10b981 0%, transparent 70%)' }} />
          </div>

          <h2 className="relative text-5xl md:text-6xl font-black text-white mb-6">
            Ready to start <span className="text-gradient">trading?</span>
          </h2>
          <p className="relative text-slate-400 text-lg mb-10">
            Join TradeDrift and simulate real exchange mechanics — completely risk free.
          </p>
          <Link
            to="/register"
            className="group relative inline-flex items-center gap-3 px-10 py-4 bg-brand hover:bg-brand-dark text-black font-bold text-lg rounded-xl transition-all duration-200 glow-green hover:scale-105"
          >
            Launch Simulator
            <ArrowRight size={20} className="group-hover:translate-x-1 transition-transform" />
          </Link>
        </div>
      </section>

      {/* Footer */}
      <footer className="border-t border-surface-border py-12 px-6">
        <div className="max-w-7xl mx-auto">
          <div className="grid md:grid-cols-4 gap-8 mb-10">
            {/* Brand */}
            <div className="md:col-span-1">
              <div className="flex items-center gap-2 mb-4">
                <div className="w-7 h-7 rounded-lg bg-brand flex items-center justify-center">
                  <TrendingUp size={14} className="text-black" strokeWidth={2.5} />
                </div>
                <span className="text-white font-bold">Trade<span className="text-gradient">Drift</span></span>
              </div>
              <p className="text-slate-500 text-sm leading-relaxed">
                A production-grade Crypto Exchange Simulation Environment.
              </p>
              <div className="flex gap-2 mt-4 flex-wrap">
                {['Go', 'gRPC', 'Kafka', 'PostgreSQL', 'Redis'].map((t) => (
                  <span key={t} className="px-2 py-0.5 text-xs text-brand/60 border border-brand/10 rounded font-mono">{t}</span>
                ))}
              </div>
            </div>

            {/* Platform */}
            <div>
              <h4 className="text-white font-semibold text-sm mb-4">Platform</h4>
              <ul className="space-y-2">
                {['Simulator', 'Order Book', 'Portfolio', 'Changelog'].map((l) => (
                  <li key={l}><a href="#" className="text-slate-500 hover:text-brand text-sm transition-colors">{l}</a></li>
                ))}
              </ul>
            </div>

            {/* Resources */}
            <div>
              <h4 className="text-white font-semibold text-sm mb-4">Resources</h4>
              <ul className="space-y-2">
                {['Documentation', 'API Reference', 'Architecture', 'System Status'].map((l) => (
                  <li key={l}><a href="#" className="text-slate-500 hover:text-brand text-sm transition-colors">{l}</a></li>
                ))}
              </ul>
            </div>

            {/* Legal */}
            <div>
              <h4 className="text-white font-semibold text-sm mb-4">Legal</h4>
              <ul className="space-y-2">
                {['Terms of Service', 'Privacy Policy', 'Risk Disclosure'].map((l) => (
                  <li key={l}><a href="#" className="text-slate-500 hover:text-brand text-sm transition-colors">{l}</a></li>
                ))}
              </ul>
            </div>
          </div>

          <div className="border-t border-surface-border pt-6 flex flex-col md:flex-row justify-between items-center gap-4">
            <p className="text-slate-600 text-sm">© 2026 TradeDrift. All virtual assets. Zero financial risk.</p>
            <p className="text-slate-700 text-xs font-mono">MIT License · Open Source</p>
          </div>
        </div>
      </footer>
    </>
  )
}
