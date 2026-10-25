import { useState, useEffect } from 'react'
import { Link } from 'react-router-dom'
import { Menu, X } from 'lucide-react'
import { TradeDriftSymbol } from './brand/TradeDriftLogo'

interface NavbarProps {
  onLaunchSimulator?: () => void
}

export default function Navbar({ onLaunchSimulator }: NavbarProps) {
  const [scrolled, setScrolled] = useState(false)
  const [mobileOpen, setMobileOpen] = useState(false)

  useEffect(() => {
    const onScroll = () => setScrolled(window.scrollY > 20)
    window.addEventListener('scroll', onScroll)
    return () => window.removeEventListener('scroll', onScroll)
  }, [])

  return (
    <nav
      className={`fixed top-0 left-0 right-0 z-50 transition-colors duration-300 border-b ${
        scrolled
          ? 'bg-[#0a0b0e]/85 backdrop-blur-md border-[#1e2530]'
          : 'bg-transparent border-transparent'
      }`}
    >
      <div className="max-w-7xl mx-auto px-6 h-16 flex items-center justify-between">
        {/* Logo */}
        <Link to="/" className="flex items-center gap-2.5 group" aria-label="TradeDrift home">
          <TradeDriftSymbol size={32} className="group-hover:scale-105 transition-transform" />
          <div className="flex flex-col leading-none">
            <span className="text-white font-bold text-lg tracking-tight">
              Trade<span className="text-gradient">Drift</span>
            </span>
            <span className="text-[8px] font-semibold text-[#10b981] tracking-[0.12em] uppercase mt-0.5">
              MARKETS · EXECUTION · INTELLIGENCE
            </span>
          </div>
        </Link>

        {/* Desktop nav */}
        <div className="hidden md:flex items-center gap-8">
          <a href="#features" className="text-slate-400 hover:text-white text-sm font-medium transition-colors">
            Simulator
          </a>
          <a href="#how" className="text-slate-400 hover:text-white text-sm font-medium transition-colors">
            How It Works
          </a>
          <a href="#" className="text-slate-400 hover:text-white text-sm font-medium transition-colors">
            Documentation
          </a>
        </div>

        {/* CTA buttons */}
        <div className="hidden md:flex items-center gap-3">
          <Link
            to="/trade"
            className="px-3.5 py-2 text-sm font-medium text-slate-300 hover:text-white transition-colors flex items-center gap-2"
          >
            <span className="w-1.5 h-1.5 rounded-full bg-[#10b981] animate-pulse" />
            <span>Explore as Guest</span>
          </Link>
          <Link
            to="/login"
            className="px-4 py-2 text-sm font-medium text-slate-300 border border-surface-border rounded-lg hover:border-brand hover:text-brand transition-all duration-200"
          >
            Login
          </Link>
          {onLaunchSimulator ? (
            <button
              type="button"
              onClick={onLaunchSimulator}
              className="px-4 py-2 text-sm font-semibold bg-brand hover:bg-brand-dark text-black rounded-lg transition-all duration-200 glow-green-sm hover:glow-green cursor-pointer"
            >
              Get Started
            </button>
          ) : (
            <Link
              to="/register"
              className="px-4 py-2 text-sm font-semibold bg-brand hover:bg-brand-dark text-black rounded-lg transition-all duration-200 glow-green-sm hover:glow-green"
            >
              Get Started
            </Link>
          )}
        </div>

        {/* Mobile menu toggle */}
        <button
          className="md:hidden text-slate-400 hover:text-white"
          onClick={() => setMobileOpen(!mobileOpen)}
        >
          {mobileOpen ? <X size={22} /> : <Menu size={22} />}
        </button>
      </div>

      {/* Mobile menu */}
      {mobileOpen && (
        <div className="md:hidden glass border-t border-surface-border px-6 py-4 flex flex-col gap-4">
          <a href="#features" className="text-slate-300 text-sm" onClick={() => setMobileOpen(false)}>Simulator</a>
          <a href="#how" className="text-slate-300 text-sm" onClick={() => setMobileOpen(false)}>How It Works</a>
          <a href="#" className="text-slate-300 text-sm" onClick={() => setMobileOpen(false)}>Documentation</a>
          <div className="flex flex-col gap-2 pt-2">
            <Link
              to="/trade"
              onClick={() => setMobileOpen(false)}
              className="w-full text-center px-4 py-2 text-sm font-medium text-emerald-400 bg-emerald-500/10 border border-emerald-500/20 rounded-lg flex items-center justify-center gap-2"
            >
              <span className="w-1.5 h-1.5 rounded-full bg-[#10b981] animate-pulse" />
              <span>Explore as Guest</span>
            </Link>
            <div className="flex gap-3">
              <Link to="/login" onClick={() => setMobileOpen(false)} className="flex-1 text-center px-4 py-2 text-sm border border-surface-border rounded-lg text-slate-300 hover:border-brand hover:text-brand transition-all">Login</Link>
              {onLaunchSimulator ? (
                <button
                  type="button"
                  onClick={() => {
                    setMobileOpen(false)
                    onLaunchSimulator()
                  }}
                  className="flex-1 text-center px-4 py-2 text-sm bg-brand text-black font-semibold rounded-lg cursor-pointer"
                >
                  Get Started
                </button>
              ) : (
                <Link to="/register" onClick={() => setMobileOpen(false)} className="flex-1 text-center px-4 py-2 text-sm bg-brand text-black font-semibold rounded-lg">Get Started</Link>
              )}
            </div>
          </div>
        </div>
      )}
    </nav>
  )
}
