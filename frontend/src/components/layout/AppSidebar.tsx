import { useState } from 'react'
import { Link, useLocation, useNavigate } from 'react-router-dom'
import {
  LayoutDashboard,
  TrendingUp,
  BarChart2,
  Briefcase,
  Wallet,
  ClipboardList,
  LineChart,
  User,
  Settings,
  LogOut,
  ChevronLeft,
  ChevronRight,
  X,
} from 'lucide-react'
import { TradeDriftSymbol } from '../brand/TradeDriftLogo'
import { useAuthStore } from '../../store/authStore'
import { authApi } from '../../api/auth'

export interface AppSidebarProps {
  collapsed?: boolean
  onToggleCollapse?: () => void
  mobileOpen?: boolean
  onCloseMobile?: () => void
}

interface NavItemConfig {
  to?: string
  label: string
  icon: React.ElementType
  action?: () => void
}

interface NavSectionConfig {
  title?: string
  items: NavItemConfig[]
}

const SIDEBAR_STORAGE_KEY = 'tradedrift_sidebar_collapsed'
const LAST_LOGIN_STORAGE_KEY = 'tradedrift_last_login_formatted'

/**
 * TradeDrift 3D Obsidian Sidebar Navigation
 *
 * Recreates the raised, glossy charcoal 3D active state and monochrome
 * trading-terminal design language from the reference design.
 */
export default function AppSidebar({
  collapsed: controlledCollapsed,
  onToggleCollapse,
  mobileOpen = false,
  onCloseMobile,
}: AppSidebarProps) {
  const { pathname } = useLocation()
  const navigate = useNavigate()
  const storeUser = useAuthStore((s) => s.user)
  const user = storeUser ?? useAuthStore.getState().user
  const logout = useAuthStore((s) => s.logout)

  // Local collapse state if not externally controlled
  const [internalCollapsed, setInternalCollapsed] = useState<boolean>(() => {
    if (typeof controlledCollapsed === 'boolean') return controlledCollapsed
    try {
      return localStorage.getItem(SIDEBAR_STORAGE_KEY) === 'true'
    } catch {
      return false
    }
  })

  const isCollapsed = typeof controlledCollapsed === 'boolean' ? controlledCollapsed : internalCollapsed

  const handleToggle = () => {
    if (onToggleCollapse) {
      onToggleCollapse()
    } else {
      setInternalCollapsed((prev) => {
        const next = !prev
        try {
          localStorage.setItem(SIDEBAR_STORAGE_KEY, String(next))
        } catch {
          /* ignore */
        }
        return next
      })
    }
  }

  // Last login formatted string
  const [lastLoginStr] = useState<string>(() => {
    try {
      if (typeof window !== 'undefined' && typeof window.localStorage !== 'undefined') {
        let stored = window.localStorage.getItem(LAST_LOGIN_STORAGE_KEY)
        if (!stored) {
          const now = new Date()
          const options: Intl.DateTimeFormatOptions = {
            day: 'numeric',
            month: 'short',
            year: 'numeric',
            hour: '2-digit',
            minute: '2-digit',
            hour12: false,
          }
          stored = now.toLocaleDateString('en-GB', options)
          window.localStorage.setItem(LAST_LOGIN_STORAGE_KEY, stored)
        }
        return stored
      }
      const now = new Date()
      return now.toLocaleDateString('en-GB', {
        day: 'numeric',
        month: 'short',
        year: 'numeric',
        hour: '2-digit',
        minute: '2-digit',
        hour12: false,
      })
    } catch {
      return ''
    }
  })

  const handleLogout = async () => {
    if (onCloseMobile) onCloseMobile()
    try {
      await authApi.logout()
    } catch {
      /* ignore */
    }
    logout()
    navigate('/login')
  }

  const displayName = user?.username || (user?.email ? user.email.split('@')[0] : 'Trader')

  // Primary navigation grouped exactly matching reference
  const SECTIONS: NavSectionConfig[] = [
    {
      title: 'OVERVIEW',
      items: [
        { to: '/dashboard', label: 'Dashboard', icon: LayoutDashboard },
      ],
    },
    {
      title: 'TRADING',
      items: [
        { to: '/trade', label: 'Trade', icon: TrendingUp },
        { to: '/markets', label: 'Markets', icon: BarChart2 },
      ],
    },
    {
      title: 'ACCOUNT',
      items: [
        { to: '/portfolio', label: 'Portfolio', icon: Briefcase },
        { to: '/wallet', label: 'Wallet', icon: Wallet },
        { to: '/orders', label: 'Orders', icon: ClipboardList },
      ],
    },
    {
      title: 'ANALYTICS',
      items: [
        { to: '/analytics', label: 'Analytics', icon: LineChart },
      ],
    },
    {
      title: 'SETTINGS',
      items: [
        { to: '/profile', label: 'Profile', icon: User },
        { to: '/settings', label: 'Settings', icon: Settings },
        { label: 'Log out', icon: LogOut, action: handleLogout },
      ],
    },
  ]

  // Shared 3D Active Pill styling
  const activeStyle: React.CSSProperties = {
    background: 'linear-gradient(180deg, #2D3036 0%, #1E2024 45%, #141619 100%)',
    border: '1px solid rgba(255, 255, 255, 0.08)',
    borderTop: '1px solid rgba(255, 255, 255, 0.22)',
    boxShadow:
      '0 8px 18px -3px rgba(0, 0, 0, 0.75), 0 2px 4px rgba(0, 0, 0, 0.5), inset 0 1px 0 rgba(255, 255, 255, 0.2), inset 0 -1px 0 rgba(0, 0, 0, 0.4)',
  }

  const renderNavContent = () => (
    <div className="flex flex-col h-full select-none">
      {/* ── Brand Header ─────────────────────────────────────────────────── */}
      <div className={`flex items-center ${isCollapsed ? 'justify-center py-4 px-2' : 'justify-between py-4 px-4'} border-b border-[#242629]/50 flex-shrink-0`}>
        <Link
          to="/dashboard"
          onClick={onCloseMobile}
          className="flex items-center gap-2.5 group focus:outline-none min-w-0"
          aria-label="TradeDrift home"
        >
          <TradeDriftSymbol size={28} className="flex-shrink-0 group-hover:scale-105 transition-transform" />
          {!isCollapsed && (
            <div className="flex flex-col min-w-0">
              <span className="text-[17px] font-bold text-white tracking-tight leading-none group-hover:text-zinc-200 transition-colors">
                TradeDrift
              </span>
              <span className="text-[10px] text-[#777D85] font-normal leading-none mt-1 truncate">
                Trade Smarter. Drift Further.
              </span>
            </div>
          )}
        </Link>

        {/* Desktop Collapse / Mobile Close */}
        {mobileOpen ? (
          <button
            type="button"
            onClick={onCloseMobile}
            aria-label="Close sidebar"
            className="w-7 h-7 rounded-lg bg-[#14161B] border border-[#242629] flex items-center justify-center text-[#777D85] hover:text-white transition-colors cursor-pointer"
          >
            <X size={15} />
          </button>
        ) : (
          !isCollapsed && (
            <button
              type="button"
              onClick={handleToggle}
              aria-label="Collapse sidebar"
              className="w-7 h-7 rounded-lg bg-[#14161B] border border-[#242629] flex items-center justify-center text-[#777D85] hover:text-white hover:border-[#34383F] transition-all cursor-pointer flex-shrink-0"
            >
              <ChevronLeft size={15} />
            </button>
          )
        )}
      </div>

      {/* Expand button on collapsed rail header */}
      {isCollapsed && !mobileOpen && (
        <div className="flex justify-center py-2 border-b border-[#242629]/40 flex-shrink-0">
          <button
            type="button"
            onClick={handleToggle}
            aria-label="Expand sidebar"
            className="w-7 h-7 rounded-lg bg-[#14161B] border border-[#242629] flex items-center justify-center text-[#777D85] hover:text-white hover:border-[#34383F] transition-all cursor-pointer"
          >
            <ChevronRight size={15} />
          </button>
        </div>
      )}

      {/* ── Scrollable Body ──────────────────────────────────────────────── */}
      <div className="flex-1 overflow-y-auto overflow-x-hidden py-3 px-3 scrollbar-thin">
        {/* Account Information (Only when expanded) */}
        {!isCollapsed && (
          <div className="px-2 pt-1 pb-3 mb-2 border-b border-[#242629]/50">
            <div className="text-[14px] font-semibold text-[#F5F5F5] leading-snug truncate">
              {displayName}
            </div>
            {lastLoginStr && (
              <div className="text-[11px] text-[#777D85] mt-0.5 truncate">
                Last login: {lastLoginStr}
              </div>
            )}
          </div>
        )}

        {/* Navigation Sections */}
        <div className="flex flex-col gap-4">
          {SECTIONS.map((section, sIdx) => {
            const isSettingsSection = section.title === 'SETTINGS'
            return (
              <div
                key={section.title || `section-${sIdx}`}
                className={isSettingsSection ? 'pt-3 mt-1 border-t border-[#242629]/50' : ''}
              >
                {/* Section Title */}
                {section.title && !isCollapsed && (
                  <div className="px-2 mb-1.5 text-[10px] font-semibold tracking-wider text-[#777D85] uppercase">
                    {section.title}
                  </div>
                )}

                {/* Section Items */}
                <div className="flex flex-col gap-1">
                  {section.items.map((item) => {
                    const Icon = item.icon
                    const isActive =
                      Boolean(item.to) &&
                      (pathname === item.to ||
                        (item.to !== '/dashboard' && pathname.startsWith(item.to!)))

                    if (item.action) {
                      return (
                        <button
                          key={item.label}
                          type="button"
                          onClick={item.action}
                          title={isCollapsed ? item.label : undefined}
                          className={`w-full flex items-center rounded-xl text-[13.5px] font-medium transition-colors select-none text-[#A7ABB2] hover:text-[#F5F5F5] hover:bg-white/[0.03] cursor-pointer ${
                            isCollapsed
                              ? 'justify-center p-2.5'
                              : 'gap-3 px-3 py-2 text-left'
                          }`}
                        >
                          <Icon size={17} className="text-[#777D85] group-hover:text-white flex-shrink-0" />
                          {!isCollapsed && <span>{item.label}</span>}
                        </button>
                      )
                    }

                    return (
                      <Link
                        key={item.label}
                        to={item.to || '#'}
                        onClick={onCloseMobile}
                        title={isCollapsed ? item.label : undefined}
                        aria-current={isActive ? 'page' : undefined}
                        style={isActive ? activeStyle : undefined}
                        className={`group flex items-center rounded-xl text-[13.5px] font-medium select-none transition-colors duration-150 ${
                          isActive
                            ? isCollapsed
                              ? 'justify-center p-2.5 text-white'
                              : 'gap-3 px-3.5 py-2.5 text-white'
                            : isCollapsed
                              ? 'justify-center p-2.5 text-[#A7ABB2] hover:text-[#F5F5F5] hover:bg-white/[0.03]'
                              : 'gap-3 px-3.5 py-2.5 text-[#A7ABB2] hover:text-[#F5F5F5] hover:bg-white/[0.03]'
                        }`}
                      >
                        <Icon
                          size={17}
                          className={`flex-shrink-0 transition-colors ${
                            isActive ? 'text-white' : 'text-[#777D85] group-hover:text-[#F5F5F5]'
                          }`}
                        />
                        {!isCollapsed && <span className="truncate">{item.label}</span>}
                      </Link>
                    )
                  })}
                </div>
              </div>
            )
          })}
        </div>
      </div>
    </div>
  )

  return (
    <>
      {/* ── Desktop Persistent Sidebar ───────────────────────────────────── */}
      <aside
        className={`hidden lg:flex flex-col h-screen bg-[#090A0C] border-r border-[#292C30] flex-shrink-0 z-30 transition-[width] duration-200 ease-in-out ${
          isCollapsed ? 'w-[68px]' : 'w-[230px]'
        }`}
        aria-label="Sidebar navigation"
      >
        {renderNavContent()}
      </aside>

      {/* ── Mobile Overlay Drawer ────────────────────────────────────────── */}
      {mobileOpen && (
        <div
          className="lg:hidden fixed inset-0 z-50 flex"
          role="dialog"
          aria-modal="true"
          aria-label="Navigation drawer"
        >
          {/* Backdrop */}
          <div
            className="fixed inset-0 bg-black/75 backdrop-blur-sm transition-opacity"
            onClick={onCloseMobile}
            aria-hidden="true"
          />

          {/* Drawer Sheet */}
          <aside className="relative flex flex-col w-[260px] max-w-[85vw] h-full bg-[#090A0C] border-r border-[#292C30] z-10 shadow-2xl">
            {renderNavContent()}
          </aside>
        </div>
      )}
    </>
  )
}
