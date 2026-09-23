import type { Balance } from '../api/wallet'
import type { DailyUsage, TopUpHistoryItem } from '../api/topup'

/**
 * Deterministic fallback mock data matching the Wallet reference screenshot.
 * Used ONLY when backend wallet, market, or top-up APIs are unreachable.
 */

export const MOCK_WALLET_BALANCES: Balance[] = [
  {
    asset: 'USDT',
    availableBalance: '14050.80',
    reservedBalance: '0.00',
  },
  {
    asset: 'BTC',
    availableBalance: '0.0000',
    reservedBalance: '0.1480',
  },
  {
    asset: 'ETH',
    availableBalance: '0.0000',
    reservedBalance: '0.0160',
  },
]

export const MOCK_DAILY_USAGE: DailyUsage = {
  userId: 'mock-user-123',
  dailyLimitInr: 10,
  reservedInr: 0,
  consumedInr: 2.5,
  availableInr: 7.5,
  timezone: 'Asia/Kolkata (IST)',
  usageDate: '2026-09-18',
  resetsAt: '2026-09-19T00:00:00+05:30',
}

export const MOCK_TOPUP_HISTORY: TopUpHistoryItem[] = [
  {
    id: '1',
    orderId: 'TOP-94812',
    amountInr: 50.00,
    usdtCredited: '+50,000 USDT',
    paymentMode: 'UPI (PhonePe)',
    status: 'Completed',
    date: 'Sep 14, 2026 10:32',
  },
  {
    id: '2',
    orderId: 'TOP-81723',
    amountInr: 10.00,
    usdtCredited: '+10,000 USDT',
    paymentMode: 'Card (Visa)',
    status: 'Completed',
    date: 'Sep 12, 2026 14:18',
  },
  {
    id: '3',
    orderId: 'WELCOME-01',
    amountInr: 0,
    usdtCredited: '+10,000 USDT',
    paymentMode: 'Signup Bonus',
    status: 'Credited',
    date: 'Sep 10, 2026 09:00',
  },
]

export const DEFAULT_ASSET_PRICES: Record<string, string> = {
  USDT: '1.00',
  BTC: '67851.35', // 0.1480 * 67851.35 = $10,042.00
  ETH: '3625.00',  // 0.0160 * 3625.00 = $58.00 (Total locked = $10,100.00)
  SOL: '152.80',
}
