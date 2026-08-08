import { useState, useEffect } from 'react'
import { Link } from 'react-router-dom'
import { TrendingUp, Menu, X } from 'lucide-react'

export default function Navbar() {
  const [scrolled, setScrolled] = useState(false)
  const [mobileOpen, setMobileOpen] = useState(false)

  useEffect(() => {
    const onScroll = () => setScrolled(window.scrollY > 20)
    window.addEventListener('scroll', onScroll)
    return () => window.removeEventListener('scroll', onScroll)
  }, [])

  return (
    <nav className={`fixed top-0 left-0 right-0 z-50 transition-all duration-300 ${scrolled ? 'glass border-b border-surface-border' : 'bg-transparent'
      }`}>
      <div className="max-w-7xl mx-auto px-6 h-16 flex items-center justify-between">
        {/* Logo */}
        <Link to="/" className="flex items-center gap-2 group">
          <div className="w-8 h-8 rounded-lg bg-brand flex items-center justify-center glow-green-sm group-hover:scale-110 transition-transform">
            <TrendingUp size={16} className="text-black" strokeWidth={2.5} />
          </div>
          <span className="text-white font-bold text-lg tracking-tight">
            Trade<span className="text-gradient">Drift</span>
          </span>
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
            to="/login"
            className="px-4 py-2 text-sm font-medium text-slate-300 border border-surface-border rounded-lg hover:border-brand hover:text-brand transition-all duration-200"
          >
            Login
          </Link>
          <Link
            to="/register"
            className="px-4 py-2 text-sm font-semibold bg-brand hover:bg-brand-dark text-black rounded-lg transition-all duration-200 glow-green-sm hover:glow-green"
          >
            Get Started
          </Link>
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
          <div className="flex gap-3 pt-2">
            <Link to="/login" className="flex-1 text-center px-4 py-2 text-sm border border-surface-border rounded-lg text-slate-300 hover:border-brand hover:text-brand transition-all">Login</Link>
            <Link to="/register" className="flex-1 text-center px-4 py-2 text-sm bg-brand text-black font-semibold rounded-lg">Get Started</Link>
          </div>
        </div>
      )}
    </nav>
  )
}
