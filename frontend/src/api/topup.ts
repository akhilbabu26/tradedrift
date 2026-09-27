import axios from 'axios'

const TOPUP_BASE_URL = import.meta.env.VITE_TOPUP_API_BASE_URL || 'http://localhost:8084'

const topupClient = axios.create({
  baseURL: TOPUP_BASE_URL,
  headers: { 'Content-Type': 'application/json' },
})

topupClient.interceptors.request.use((config) => {
  const token = localStorage.getItem('access_token')
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

export interface CreateTopUpRequest {
  inrAmount: number // INR amount
}

export interface TopUpOrder {
  topupId: string
  userId: string
  inrAmount: number
  usdtAmount: string
  exchangeRate?: number | string
  status: 'INITIATED' | 'PAYMENT_PENDING' | 'PAYMENT_CONFIRMED' | 'CREDIT_PENDING' | 'COMPLETED' | 'FAILED' | 'REFUND_REQUIRED'
  provider: string
  providerOrderId?: string
  expiresAt?: string
  createdAt: string
}

export interface DailyUsage {
  userId: string
  limitInr: number
  dailyLimitInr: number
  reservedInr: number
  consumedInr: number
  remainingInr: number
  availableInr: number
  usageDate: string
  resetsAt: string
  timezone?: string
}

export interface TopUpHistoryItem {
  id: string
  orderId: string
  amountInr: number
  usdtCredited: string
  paymentMode: string
  status: 'Completed' | 'Credited' | 'Pending' | 'Failed'
  date: string
}

export const topupApi = {
  // POST /api/v1/topups — create a top-up order
  createTopUp: async (data: CreateTopUpRequest, idempotencyKey: string): Promise<TopUpOrder> => {
    const res = await topupClient.post<TopUpOrder>('/api/v1/topups', data, {
      headers: {
        'X-Idempotency-Key': idempotencyKey,
      },
    })
    return res.data
  },

  // GET /api/v1/topups/daily-usage — get current user daily limit & usage
  getDailyUsage: async (): Promise<DailyUsage> => {
    const res = await topupClient.get<any>('/api/v1/topups/daily-usage')
    const d = res.data || {}
    const limit = Number(d.limitInr ?? d.dailyLimitInr ?? 10)
    const consumed = Number(d.consumedInr ?? 0)
    const reserved = Number(d.reservedInr ?? 0)
    const remaining = Number(d.remainingInr ?? d.availableInr ?? Math.max(0, limit - consumed - reserved))
    return {
      userId: d.userId ?? '',
      limitInr: limit,
      dailyLimitInr: limit,
      reservedInr: reserved,
      consumedInr: consumed,
      remainingInr: remaining,
      availableInr: remaining,
      usageDate: d.usageDate ?? '',
      resetsAt: d.resetsAt ?? '',
      timezone: d.timezone ?? 'Asia/Kolkata (IST)',
    }
  },

  // GET /api/v1/topups/:id — get topup status by ID
  getTopUpById: async (id: string): Promise<TopUpOrder> => {
    const res = await topupClient.get<TopUpOrder>(`/api/v1/topups/${id}`)
    return res.data
  },
}
