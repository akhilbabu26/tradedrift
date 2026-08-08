import WebGLBackground from '../components/WebGLBackground'
import Navbar from '../components/Navbar'
import TickerBar from '../components/TickerBar'
import HeroSection from '../components/HeroSection'
import FeaturesSection from '../components/FeaturesSection'
import HowItWorks from '../components/HowItWorks'
import DashboardPreview from '../components/DashboardPreview'
import Footer from '../components/Footer'

export default function LandingPage() {
  return (
    <div className="relative min-h-screen bg-base">
      {/* WebGL shader background — fixed behind everything */}
      <div className="fixed inset-0 z-0">
        <WebGLBackground />
      </div>
      <div className="relative z-10">
        <Navbar />
        <TickerBar />
        <HeroSection />
        <FeaturesSection />
        <HowItWorks />
        <DashboardPreview />
        <Footer />
      </div>
    </div>
  )
}
