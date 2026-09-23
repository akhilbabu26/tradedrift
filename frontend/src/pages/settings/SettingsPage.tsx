import SettingsHeader from '../../components/settings/SettingsHeader'
import ProfileAccountCard from '../../components/settings/ProfileAccountCard'
import SecurityPasswordCard from '../../components/settings/SecurityPasswordCard'
import ActiveSessionsCard from '../../components/settings/ActiveSessionsCard'
import SignOutCard from '../../components/settings/SignOutCard'
import SettingsFooter from '../../components/settings/SettingsFooter'

/**
 * Settings page — authenticated route at /settings.
 *
 * Layout provided by MainLayout (App.tsx) via <Outlet />.
 * Displays user identity, password change form, demo active sessions, and sign out options.
 * Matches desktop-first proportions (max-w-[880px]) and dark terminal design system.
 */
export default function SettingsPage() {
  return (
    <div className="flex-1 overflow-y-auto bg-[#0a0b0e]">
      <div className="max-w-[880px] mx-auto px-4 sm:px-6 py-7 sm:py-8">
        <SettingsHeader />
        <ProfileAccountCard />
        <SecurityPasswordCard />
        <ActiveSessionsCard />
        <SignOutCard />
        <SettingsFooter />
      </div>
    </div>
  )
}
