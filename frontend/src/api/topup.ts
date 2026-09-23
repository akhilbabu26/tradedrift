import client from './client'

export interface CreateTopUpRequest {
  inrAmount: number // Integer between 1 and 10
}

export interface TopUpOrder {
  topupId: string
  userId: string
  inrAmount: number
  usdtAmount: string
  exchangeRate?: string
  status: 'INITIATED' | 'PAYMENT_PENDING' | 'PAYMENT_CONFIRMED' | 'CREDIT_PENDING' | 'COMPLETED' | 'FAILED' | 'REFUND_REQUIRED'
  provider: string
  providerOrderId?: string
  reservationDate?: string
  paymentId?: string
  expiresAt?: string
  createdAt: string
  paidAt?: string
  completedAt?: string
}

export interface DailyUsage {
  userId: string
  dailyLimitInr: number
  reservedInr: number
  consumedInr: number
  availableInr: number
  timezone: string
  usageDate: string
  resetsAt: string
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
    const res = await client.post<TopUpOrder>('/api/v1/topups', data, {
      headers: {
        'Idempotency-Key': idempotencyKey,
      },
    })
    return res.data
  },

  // GET /api/v1/topups/daily-usage — get current user daily limit & usage
  getDailyUsage: async (): Promise<DailyUsage> => {
    const res = await client.get<DailyUsage>('/api/v1/topups/daily-usage')
    return res.data
  },

  // GET /api/v1/topups/:id — get topup status by ID
  getTopUpById: async (id: string): Promise<TopUpOrder> => {
    const res = await client.get<TopUpOrder>(`/api/v1/topups/${id}`)
    return res.data
  },
}
