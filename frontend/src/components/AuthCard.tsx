import { Link } from 'react-router-dom'
import WebGLBackground from './WebGLBackground'
import TradeDriftLogo from './brand/TradeDriftLogo'

interface AuthCardProps {
  children: React.ReactNode
  className?: string
  cardClassName?: string
  logoVariant?: 'auth' | 'navbar'
  hideLogo?: boolean
}

export default function AuthCard({
  children,
  className = '',
  cardClassName = '',
  logoVariant = 'navbar',
  hideLogo = false,
}: AuthCardProps) {
  return (
    <div className={`relative min-h-dvh w-full overflow-y-auto flex items-center justify-center bg-base px-4 py-4 sm:py-6 ${className}`}>
      {/* WebGL animated background */}
      <div className="fixed inset-0 z-0">
        <WebGLBackground />
      </div>

      {/* 3D Glass card */}
      <div
        className={`relative z-10 w-full flex flex-col ${
          cardClassName ||
          'max-w-[420px] p-6 sm:p-7 bg-[#131518]/90 backdrop-blur-xl rounded-2xl shadow-[0_16px_36px_-8px_rgba(0,0,0,0.8),0_4px_12px_rgba(0,0,0,0.5),inset_0_1px_0_rgba(255,255,255,0.14)] border border-[rgba(255,255,255,0.07)] border-t-[rgba(255,255,255,0.16)]'
        }`}
      >
        {/* Logo */}
        {!hideLogo && (
          <div className="flex justify-center mb-3.5 sm:mb-4">
            <Link to="/" className="group" aria-label="TradeDrift home">
              <TradeDriftLogo variant={logoVariant} symbolSize={logoVariant === 'auth' ? 34 : 26} className="group-hover:scale-105 transition-transform" />
            </Link>
          </div>
        )}

        {children}
      </div>
    </div>
  )
}


