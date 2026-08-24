import client from './client'

export interface RawBalance {
  asset: string
  available_balance?: string
  availableBalance?: string
  reserved_balance?: string
  reservedBalance?: string
}

export interface Balance {
  asset: string
  availableBalance: string
  reservedBalance: string
}

export interface AssetInfo {
  symbol: string
  name: string
  decimals: number
  isEnabled: boolean
  seedAmount: string
  displayOrder: number
}

function normalizeBalance(b: RawBalance): Balance {
  return {
    asset: b.asset,
    availableBalance: b.available_balance ?? b.availableBalance ?? '0.00',
    reservedBalance: b.reserved_balance ?? b.reservedBalance ?? '0.00',
  }
}

export const walletApi = {
  // GET /api/v1/wallet/balances — all balances for the logged-in user
  getAllBalances: async (): Promise<Balance[]> => {
    const res = await client.get<{ balances: RawBalance[] }>('/api/v1/wallet/balances')
    const rawList = res.data?.balances || []
    return rawList.map(normalizeBalance)
  },

  // GET /api/v1/wallet/balances/{asset} — single asset
  getBalance: async (asset: string): Promise<Balance> => {
    const res = await client.get<RawBalance | { balance: RawBalance }>(`/api/v1/wallet/balances/${asset}`)
    const raw = 'balance' in res.data ? res.data.balance : res.data
    return normalizeBalance(raw)
  },

  // GET /api/v1/wallet/assets — supported asset list (public)
  getSupportedAssets: async (): Promise<AssetInfo[]> => {
    const res = await client.get<{ assets: AssetInfo[] }>('/api/v1/wallet/assets')
    return res.data?.assets || []
  },
}
