export type NotificationCategory = 'all' | 'trading' | 'account' | 'system'

export type NotificationIconType =
  | 'buy'
  | 'sell'
  | 'price_alert'
  | 'welcome'
  | 'password'
  | 'market'
  | 'risk'
  | 'partial_fill'

export interface NotificationItem {
  id: string
  category: 'trading' | 'account' | 'system'
  title: string
  description: string
  timestamp: string
  isRead: boolean
  iconType: NotificationIconType
}

export interface NotificationStats {
  total: number
  totalChange: string
  trading: number
  tradingChange: string
  account: number
  accountChange: string
  system: number
  systemChange: string
}
