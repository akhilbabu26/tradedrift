import ProfileHeader from '../../components/profile/ProfileHeader'
import ProfileHeroCard from '../../components/profile/ProfileHeroCard'
import TradingProfileCard from '../../components/profile/TradingProfileCard'
import AssetOverviewCard from '../../components/profile/AssetOverviewCard'
import AccountPreferencesCard from '../../components/profile/AccountPreferencesCard'
import SecurityShortcutCard from '../../components/profile/SecurityShortcutCard'
import ProfileInsightsSection from '../../components/profile/ProfileInsightsSection'
import ProfileFooter from '../../components/profile/ProfileFooter'
import { useProfileData } from '../../hooks/useProfileData'

/**
 * ProfilePage — Authenticated route at /profile.
 *
 * Connected to live backend APIs via useProfileData.
 * Shows account identity, trading statistics, real asset distribution,
 * terminal preferences, security status, and trading insights.
 */
export default function ProfilePage() {
  const { metrics, assets, preferences, security, insights } = useProfileData()

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
            <TradingProfileCard metrics={metrics} />
            <AccountPreferencesCard preferences={preferences} />
          </div>

          {/* Right Column: Asset Overview & Security Shortcut */}
          <div className="flex flex-col gap-5">
            <AssetOverviewCard assets={assets} />
            <SecurityShortcutCard security={security} />
          </div>
        </div>

        {/* Profile Insights */}
        <ProfileInsightsSection insights={insights} />

        {/* Page Footer */}
        <ProfileFooter />
      </div>
    </div>
  )
}
