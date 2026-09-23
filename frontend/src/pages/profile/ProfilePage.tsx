import ProfileHeader from '../../components/profile/ProfileHeader'
import ProfileHeroCard from '../../components/profile/ProfileHeroCard'
import TradingProfileCard from '../../components/profile/TradingProfileCard'
import AssetOverviewCard from '../../components/profile/AssetOverviewCard'
import AccountPreferencesCard from '../../components/profile/AccountPreferencesCard'
import SecurityShortcutCard from '../../components/profile/SecurityShortcutCard'
import ProfileInsightsSection from '../../components/profile/ProfileInsightsSection'
import ProfileFooter from '../../components/profile/ProfileFooter'
import {
  MOCK_TRADING_PROFILE_METRICS,
  MOCK_PROFILE_ASSETS,
  MOCK_ACCOUNT_PREFERENCES,
  MOCK_SECURITY_SHORTCUT,
  MOCK_PROFILE_INSIGHTS,
} from '../../data/profileMock'

/**
 * ProfilePage — Authenticated route at /profile.
 *
 * Rendered inside MainLayout via <Outlet />.
 * Shows account identity, trading statistics, asset distribution,
 * terminal preferences, security status, and trading insights.
 */
export default function ProfilePage() {
  return (
    <div className="flex-1 overflow-y-auto bg-[#0a0b0e] text-[#f5f7fa]">
      <div className="max-w-[1200px] mx-auto px-4 lg:px-6 py-5 flex flex-col gap-5">
        {/* Page Header */}
        <ProfileHeader />

        {/* Profile Hero & Account Information */}
        <ProfileHeroCard />

        {/* 2-Column Balanced Trading & Preferences Grid */}
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-5 items-start">
          {/* Left Column: Trading Profile & Preferences */}
          <div className="flex flex-col gap-5">
            <TradingProfileCard metrics={MOCK_TRADING_PROFILE_METRICS} />
            <AccountPreferencesCard preferences={MOCK_ACCOUNT_PREFERENCES} />
          </div>

          {/* Right Column: Asset Overview & Security Shortcut */}
          <div className="flex flex-col gap-5">
            <AssetOverviewCard assets={MOCK_PROFILE_ASSETS} />
            <SecurityShortcutCard security={MOCK_SECURITY_SHORTCUT} />
          </div>
        </div>

        {/* Profile Insights */}
        <ProfileInsightsSection insights={MOCK_PROFILE_INSIGHTS} />

        {/* Page Footer */}
        <ProfileFooter />
      </div>
    </div>
  )
}
