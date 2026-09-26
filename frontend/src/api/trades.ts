/**
 * src/api/trades.ts
 *
 * Trades REST API service.
 * SOURCE OF TRUTH: frontend/docs/all backend apis.md
 *
 * Documented endpoints (Gateway → Trade Service gRPC):
 *   GET /api/v1/trades        — user's fill/trade history (cursor-paginated)
 *   GET /api/v1/trades/{id}   — single trade by ID (caller must be buyer or seller)
 *
 * Pagination: keyset cursor
 *   Query params: cursor, limit (default 20, max 100), market_id
 *   Response includes next_cursor when more pages exist.
 *
 * All endpoints require Bearer JWT authentication.
 */

import client from './client'

// ── Backend response DTOs ────────────────────────────────────────────────────
// Field names verified against backend docs / Trade Service gRPC proto.

export interface BackendTrade {
  id: string
  market_id: string      // e.g. "BTC-USDT"
  price: string          // decimal string, e.g. "67500.00"
  quantity: string       // decimal string, e.g. "0.0500"
  taker_side: string     // "BUY" | "SELL" — the aggressor side
  created_at: string     // RFC3339 timestamp
}

export interface TradesListResponse {
  trades: BackendTrade[]
  next_cursor?: string   // opaque cursor string; present when more pages exist
}

export interface GetTradesParams {
  cursor?: string    // keyset cursor from previous next_cursor
  limit?: number     // default: 20, max: 100
  market_id?: string // optional filter by market
}

// ── API service ──────────────────────────────────────────────────────────────

export const tradesApi = {
  /**
   * GET /api/v1/trades
   * Returns cursor-paginated fill history for the authenticated user.
   * Requires: Bearer JWT
   */
  listTrades: async (params?: GetTradesParams): Promise<TradesListResponse> => {
    const res = await client.get<TradesListResponse>('/api/v1/trades', { params })
    const body = (res.data as unknown as { data?: TradesListResponse })
    return body?.data ?? res.data
  },

  /**
   * GET /api/v1/trades/{id}
   * Returns a single trade. Caller must be the buyer or seller.
   * Requires: Bearer JWT
   */
  getTrade: async (id: string): Promise<BackendTrade> => {
    const res = await client.get<BackendTrade>(`/api/v1/trades/${id}`)
    const body = (res.data as unknown as { data?: BackendTrade })
    return body?.data ?? res.data
  },
}
