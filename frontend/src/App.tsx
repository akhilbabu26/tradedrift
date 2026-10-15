import { useEffect, lazy, Suspense } from 'react'
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { Toaster } from 'react-hot-toast'
import { useAuthStore } from './store/authStore'
import ProtectedRoute from './components/ProtectedRoute'
import MainLayout from './components/layout/MainLayout'
import PageLoadingFallback from './components/common/PageLoadingFallback'

// Public & Auth Pages (Deferred Chunks)
const LandingPage = lazy(() => import('./pages/LandingPage'))
const LoginPage = lazy(() => import('./pages/auth/LoginPage'))
const RegisterPage = lazy(() => import('./pages/auth/RegisterPage'))
const VerifyEmailPage = lazy(() => import('./pages/auth/VerifyEmailPage'))
const ForgotPasswordPage = lazy(() => import('./pages/auth/ForgotPasswordPage'))
const ResetPasswordPage = lazy(() => import('./pages/auth/ResetPasswordPage'))

// Authenticated Core Pages (Deferred Chunks)
const DashboardPage = lazy(() => import('./pages/dashboard/DashboardPage'))
const TradePage = lazy(() => import('./pages/trade/TradePage'))
const MarketsPage = lazy(() => import('./pages/markets/MarketsPage'))
const PortfolioPage = lazy(() => import('./pages/portfolio/PortfolioPage'))
const WalletPage = lazy(() => import('./pages/wallet/WalletPage'))
const OrdersPage = lazy(() => import('./pages/orders/OrdersPage'))
const AnalyticsPage = lazy(() => import('./pages/analytics/AnalyticsPage'))

// Dedicated Account & Settings Pages (Deferred Chunks)
const SettingsPage = lazy(() => import('./pages/settings/SettingsPage'))
const NotificationsPage = lazy(() => import('./pages/notifications/NotificationsPage'))
const ProfilePage = lazy(() => import('./pages/profile/ProfilePage'))

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
      <Suspense fallback={<PageLoadingFallback />}>
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
           * shared navbar, sidebar, and page shell. Child routes render inside <Outlet />.
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
      </Suspense>
    </BrowserRouter>
  )
}

