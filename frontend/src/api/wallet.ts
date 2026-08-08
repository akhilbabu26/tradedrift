import client from './client'

export interface Balance {
  asset: string
  availableBalance: string
  reservedBalance: string
}

export interface AssetInfo {
  assetCode: string
  assetName: string
  decimals: number
  isEnabled: boolean
  seedAmount: string
  displayOrder: number
}

export const walletApi = {
  // GET /api/v1/wallet/balances — all balances for the logged-in user
  getAllBalances: () =>
    client
      .get<{ balances: Balance[] }>('/api/v1/wallet/balances')
      .then((r) => r.data.balances),

  // GET /api/v1/wallet/balances/{asset} — single asset
  getBalance: (asset: string) =>
    client
      .get<{ balance: Balance }>(`/api/v1/wallet/balances/${asset}`)
      .then((r) => r.data.balance),

  // GET /api/v1/wallet/assets — supported asset list (public)
  getSupportedAssets: () =>
    client
      .get<{ assets: AssetInfo[] }>('/api/v1/wallet/assets')
      .then((r) => r.data.assets),
}
