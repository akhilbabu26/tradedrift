import type { AssetMetadata } from '../utils/marketMetadata'

export interface TradingProfileMetrics {
  totalTrades: number
  totalVolume: string
  totalVolumeUnit: string
  realizedPnl: string
  realizedPnlUnit: string
  winRate: string
}

export interface ProfileAssetAllocation {
  asset: 'BTC' | 'ETH' | 'SOL' | 'USDT'
  balance: string
  weightPct: number
  meta: AssetMetadata
}

export interface AccountPreferenceItem {
  label: string
  value: string
  description?: string
}

export interface SecurityShortcutInfo {
  passwordStatus: string
  emailVerification: string
  activeSessions: string
}

export interface ProfileInsightItem {
  id: string
  label: string
  value: string
  subValue?: string
  badge?: string
}
