import type { Market, Ticker24h, Candle } from '../api/market'
import type { Order, CreateOrderRequest } from '../api/order'
import type { Balance } from '../api/wallet'
import type { AssetMetadata, MarketMetadata } from '../utils/marketMetadata'

export type { Market, Ticker24h, Candle, Order, CreateOrderRequest, Balance, AssetMetadata, MarketMetadata }

export interface SelectedMarket extends Market {
  pair: string
  base: string
  quote: string
  name: string
  symbol: string
  baseMeta: AssetMetadata
  quoteMeta: AssetMetadata
}

export type Timeframe = '1m' | '5m' | '15m' | '1h' | '4h' | '1d'

export type OrderSide = 'BUY' | 'SELL'
export type OrderType = 'LIMIT' | 'MARKET'

export interface OrderBookLevel {
  price: string
  quantity: string
  total: string
  depthPct: number
}

export interface OrderBookSnapshot {
  asks: OrderBookLevel[]
  bids: OrderBookLevel[]
  spread: string
  spreadPercent: string
  lastPrice: string
}

export interface TradeMarketSummary {
  id: string
  symbol: string
  baseAsset: string
  quoteAsset: string
  price: string
  change24h: string
  high24h: string
  low24h: string
  volume24h: string
  quoteVolume24h: string
}
