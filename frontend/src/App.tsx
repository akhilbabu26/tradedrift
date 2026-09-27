import { useEffect } from 'react'
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { Toaster } from 'react-hot-toast'
import { useAuthStore } from './store/authStore'
import ProtectedRoute from './components/ProtectedRoute'
import MainLayout from './components/layout/MainLayout'
import LandingPage from './pages/LandingPage'
import LoginPage from './pages/auth/LoginPage'
import RegisterPage from './pages/auth/RegisterPage'
import VerifyEmailPage from './pages/auth/VerifyEmailPage'
import ForgotPasswordPage from './pages/auth/ForgotPasswordPage'
import ResetPasswordPage from './pages/auth/ResetPasswordPage'
import DashboardPage from './pages/dashboard/DashboardPage'
import TradePage from './pages/trade/TradePage'
import MarketsPage from './pages/markets/MarketsPage'
import PortfolioPage from './pages/portfolio/PortfolioPage'
import WalletPage from './pages/wallet/WalletPage'
import OrdersPage from './pages/orders/OrdersPage'
import AnalyticsPage from './pages/analytics/AnalyticsPage'
import SettingsPage from './pages/settings/SettingsPage'
import NotificationsPage from './pages/notifications/NotificationsPage'
import ProfilePage from './pages/profile/ProfilePage'

export default function App() {
  const hydrate = useAuthStore((s) => s.hydrate)

  // Restore session from localStorage on first load
  useEffect(() => { hydrate() }, [hydrate])

  return (
    <BrowserRouter>
      <Toaster
        position="top-right"
        toastOptions={{
          style: { background: '#111318', color: '#f1f5f9', border: '1px solid #1e2530' },
        }}
      />
      <Routes>
        {/* Public / Landing */}
        <Route path="/" element={<LandingPage />} />

        {/* Auth Pages */}
        <Route path="/login" element={<LoginPage />} />
        <Route path="/register" element={<RegisterPage />} />
        <Route path="/verify" element={<VerifyEmailPage />} />
        <Route path="/forgot-password" element={<ForgotPasswordPage />} />
        <Route path="/reset-password" element={<ResetPasswordPage />} />

        {/*
         * Protected App Pages — shared MainLayout
         *
         * ProtectedRoute guards authentication. MainLayout provides the
         * shared navbar and page shell. Child routes render inside <Outlet />.
         */}
        <Route
          element={
            <ProtectedRoute>
              <MainLayout />
            </ProtectedRoute>
          }
        >
          {/* Authenticated Core Routes */}
          <Route path="/dashboard" element={<DashboardPage />} />
          <Route path="/trade" element={<TradePage />} />
          <Route path="/markets" element={<MarketsPage />} />
          <Route path="/portfolio" element={<PortfolioPage />} />
          <Route path="/wallet" element={<WalletPage />} />
          <Route path="/orders" element={<OrdersPage />} />
          <Route path="/analytics" element={<AnalyticsPage />} />

          {/* Dedicated Account & Setting Pages */}
          <Route path="/settings" element={<SettingsPage />} />
          <Route path="/notifications" element={<NotificationsPage />} />
          <Route path="/profile" element={<ProfilePage />} />
        </Route>

        {/* Fallback */}
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </BrowserRouter>
  )
}
