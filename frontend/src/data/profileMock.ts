/**
 * Profile Mock Data — Deterministic fallback and initial profile data.
 *
 * Strictly adheres to TradeDrift supported assets: BTC, ETH, SOL, USDT.
 * Values reflect the trading simulation metrics and profile state.
 */

import { getAssetMetadata } from '../utils/marketMetadata'
import type {
  TradingProfileMetrics,
  ProfileAssetAllocation,
  AccountPreferenceItem,
  SecurityShortcutInfo,
  ProfileInsightItem,
} from '../types/profile'

export const MOCK_TRADING_PROFILE_METRICS: TradingProfileMetrics = {
  totalTrades: 42,
  totalVolume: '28,400.00',
  totalVolumeUnit: 'USDT',
  realizedPnl: '+1,450.20',
  realizedPnlUnit: 'USDT',
  winRate: '66.7%',
}

export const MOCK_PROFILE_ASSETS: ProfileAssetAllocation[] = [
  {
    asset: 'BTC',
    balance: '0.1200 BTC',
    weightPct: 38,
    meta: getAssetMetadata('BTC'),
  },
  {
    asset: 'ETH',
    balance: '1.5000 ETH',
    weightPct: 28,
    meta: getAssetMetadata('ETH'),
  },
  {
    asset: 'SOL',
    balance: '10.0000 SOL',
    weightPct: 22,
    meta: getAssetMetadata('SOL'),
  },
  {
    asset: 'USDT',
    balance: '4,250.00 USDT',
    weightPct: 12,
    meta: getAssetMetadata('USDT'),
  },
]

export const MOCK_ACCOUNT_PREFERENCES: AccountPreferenceItem[] = [
  {
    label: 'Theme',
    value: 'Dark',
    description: 'Terminal Dark Theme (Default)',
  },
  {
    label: 'Account Type',
    value: 'Simulated Spot Account',
    description: 'Paper trading with virtual assets',
  },
  {
    label: 'Base Currency',
    value: 'USDT',
    description: 'Primary quote currency for settlements',
  },
  {
    label: 'Language',
    value: 'English',
    description: 'Interface display language',
  },
]

export const MOCK_SECURITY_SHORTCUT: SecurityShortcutInfo = {
  passwordStatus: 'Last updated recently',
  emailVerification: 'Verified',
  activeSessions: '2 sessions',
}

export const MOCK_PROFILE_INSIGHTS: ProfileInsightItem[] = [
  {
    id: 'most-traded',
    label: 'Most Traded Asset',
    value: 'BTC',
    subValue: '62% of total order executions',
    badge: 'Dominant',
  },
  {
    id: 'best-performing',
    label: 'Best Performing Asset',
    value: 'SOL',
    subValue: '+76.6% unrealized gain',
    badge: 'Top Gainer',
  },
  {
    id: 'trading-activity',
    label: 'Trading Activity',
    value: '42 fills',
    subValue: '100% simulated execution rate',
    badge: 'Active',
  },
]
