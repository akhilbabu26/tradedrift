import { Link } from 'react-router-dom'
import WebGLBackground from './WebGLBackground'

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

      {/* Glass card */}
      <div className="relative z-10 w-full max-w-[480px] bg-[#111318]/80 backdrop-blur-xl rounded-2xl shadow-2xl border border-[#1f2229] flex flex-col p-8 md:p-10">
        {/* Logo */}
        <div className="flex justify-center mb-8">
          <Link to="/">
            <img src="/logo.png" alt="TradeDrift" className="h-11 w-auto object-contain hover:opacity-90 transition-opacity" />
          </Link>
        </div>

        {children}
      </div>
    </div>
  )
}
