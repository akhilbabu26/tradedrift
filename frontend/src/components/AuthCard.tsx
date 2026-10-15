import { Link } from 'react-router-dom'
import WebGLBackground from './WebGLBackground'
import TradeDriftLogo from './brand/TradeDriftLogo'

interface AuthCardProps {
  children: React.ReactNode
}

export default function AuthCard({ children }: AuthCardProps) {
  return (
    <div className="relative min-h-screen w-screen overflow-auto flex items-center justify-center bg-base py-10 px-4">
      {/* WebGL animated background */}
      <div className="fixed inset-0 z-0">
        <WebGLBackground />
      </div>

      {/* 3D Glass card */}
      <div className="relative z-10 w-full max-w-[480px] bg-[#131518]/90 backdrop-blur-xl rounded-2xl shadow-[0_16px_36px_-8px_rgba(0,0,0,0.8),0_4px_12px_rgba(0,0,0,0.5),inset_0_1px_0_rgba(255,255,255,0.14)] border border-[rgba(255,255,255,0.07)] border-t-[rgba(255,255,255,0.16)] flex flex-col p-8 md:p-10">
        {/* Logo */}
        <div className="flex justify-center mb-6">
          <Link to="/" className="group" aria-label="TradeDrift home">
            <TradeDriftLogo variant="auth" symbolSize={38} className="group-hover:scale-105 transition-transform" />
          </Link>
        </div>

        {children}
      </div>
    </div>
  )
}
