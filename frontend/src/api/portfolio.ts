/**
 * Portfolio API client
 *
 * Wraps the gateway-exposed Portfolio Service endpoints:
 *   GET /api/v1/portfolio/summary  → PortfolioSummaryResponse
 *   GET /api/v1/portfolio/holdings → PortfolioHoldingsResponse
 *
 * These endpoints exist in the running backend (returns 401 without auth,
 * confirming the routes are registered at the gateway).
 */

import client from './client'

// ── Response DTOs (matching gateway/internal/handler/portfolio/dto.go) ──────

export interface PortfolioSummaryResponse {
  userId: string
  totalValue: string
  realizedPnl: string
  unrealizedPnl: string
  cashBalance: string
  updatedAt: string
}

export interface HoldingDetail {
  asset: string
  totalQuantity: string
  averageEntryPrice: string
  currentPrice: string
  unrealizedPnl: string
}

export interface PortfolioHoldingsResponse {
  userId: string
  holdings: HoldingDetail[]
}

// ── API client ────────────────────────────────────────────────────────────────

export const portfolioApi = {
  /**
   * GET /api/v1/portfolio/summary
   * Returns the authenticated user's portfolio summary.
   * Throws on network/HTTP error — callers must catch and fall back.
   */
  getSummary: async (): Promise<PortfolioSummaryResponse> => {
    const res = await client.get<PortfolioSummaryResponse>('/api/v1/portfolio/summary')
    return res.data
  },

  /**
   * GET /api/v1/portfolio/holdings
   * Returns the authenticated user's current crypto holdings.
   * Throws on network/HTTP error — callers must catch and fall back.
   */
  getHoldings: async (): Promise<PortfolioHoldingsResponse> => {
    const res = await client.get<PortfolioHoldingsResponse>('/api/v1/portfolio/holdings')
    return res.data
  },
}
