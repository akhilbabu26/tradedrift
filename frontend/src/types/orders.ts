export type OrderSide = 'BUY' | 'SELL'
export type OrderType = 'Limit' | 'Market'

export type OrderStatusUI = 'Open' | 'Filled' | 'Canceled' | 'Partially Filled' | 'Expired' | 'Rejected'

export interface OpenOrderItem {
  id: string
  time: string
  pair: string             // e.g. "BTC/USDT"
  type: OrderType
  side: OrderSide
  price: string            // formatted e.g. "67,200.00"
  amount: string           // e.g. "0.1000 BTC"
  filledRemaining: string  // e.g. "0.0500 / 0.0500"
  progress: number         // 0 to 100
  status: 'Open'
  rawStatus?: string
}

export interface OrderHistoryItem {
  id: string
  orderId: string          // e.g. "ord_7f2a9c3b8e1a"
  time: string
  timestamp: number        // Epoch milliseconds for exact date filtering
  pair: string
  type: OrderType
  side: OrderSide
  avgFilledPrice: string   // e.g. "67,150.00"
  executedTotal: string    // e.g. "0.1000 / 0.1000"
  totalValueUSDT: string   // e.g. "6,715.00"
  status: OrderStatusUI
  rawStatus?: string
}

export interface TradeFillItem {
  id: string
  tradeId: string          // e.g. "trd_9a1f2c"
  time: string
  timestamp?: number       // Epoch milliseconds for exact date filtering
  pair: string
  side: OrderSide
  executionPrice: string   // e.g. "67,150.00"
  filledAmount: string     // e.g. "0.0500 BTC"
  totalCostUSDT: string    // e.g. "3,360.00"
  feeUSDT: string          // e.g. "3.36"
}

export interface OrdersKPIs {
  activeOrders: number
  todayExecutions: number
  todayExecutionsChange: string // e.g. "+20%"
  tradedVolume24h: string       // e.g. "18,450.00"
  tradedVolume24hChange: string // e.g. "+12%"
  fundsLocked: string           // e.g. "4,200.00"
  fundsLockedOrderCount: number // e.g. 3
}

export interface OrderFilterState {
  market: string  // "All Markets" | "BTC/USDT" | "ETH/USDT" | "SOL/USDT"
  side: string    // "All Sides" | "BUY" | "SELL"
  timeRange?: string // "Last 7 Days" | "Last 30 Days" | "All Time"
  searchQuery: string
}
