import { useState } from 'react'
import WebGLBackground from '../components/WebGLBackground'
import Navbar from '../components/Navbar'
import TickerBar from '../components/TickerBar'
import HeroSection from '../components/HeroSection'
import FeaturesSection from '../components/FeaturesSection'
import HowItWorks from '../components/HowItWorks'
import DashboardPreview from '../components/DashboardPreview'
import Footer from '../components/Footer'
import LaunchSimulatorModal from '../components/common/LaunchSimulatorModal'

export default function LandingPage() {
  const [launchModalOpen, setLaunchModalOpen] = useState(false)

  const handleOpenLaunchModal = () => setLaunchModalOpen(true)
  const handleCloseLaunchModal = () => setLaunchModalOpen(false)

  return (
    <div className="relative min-h-screen bg-base">
      {/* WebGL shader background — fixed behind everything */}
      <div className="fixed inset-0 z-0">
        <WebGLBackground />
      </div>
      <div className="relative z-10">
        <Navbar onLaunchSimulator={handleOpenLaunchModal} />
        <TickerBar />
        <HeroSection onLaunchSimulator={handleOpenLaunchModal} />
        <FeaturesSection />
        <HowItWorks />
        <DashboardPreview />
        <Footer onLaunchSimulator={handleOpenLaunchModal} />
      </div>

      {/* Launch Simulator Dialog (Create Account / Sign In vs Explore as Guest) */}
      <LaunchSimulatorModal
        isOpen={launchModalOpen}
        onClose={handleCloseLaunchModal}
        destinationMarket="BTC-USDT"
      />
    </div>
  )
}
