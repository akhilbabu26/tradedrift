import client from './client'

export interface CreateOrderRequest {
  market_id: string
  side: 'BUY' | 'SELL'
  order_type: 'LIMIT' | 'MARKET'
  price?: string
  quantity: string
}

export interface Order {
  id: string
  user_id: string
  market_id: string
  side: string
  order_type: string
  status: string
  price: string
  quantity: string
  filled_quantity: string
  created_at: string
  updated_at: string
}

export interface ListOrdersParams {
  market_id?: string
  status?: string
  limit?: number
}

export const orderApi = {
  // POST /api/v1/orders — Create a new limit/market order
  createOrder: async (data: CreateOrderRequest): Promise<Order> => {
    const res = await client.post<Order>('/api/v1/orders', data)
    return res.data
  },

  // GET /api/v1/orders — List user orders
  listOrders: async (params?: ListOrdersParams): Promise<Order[]> => {
    const res = await client.get<{ orders: Order[] }>('/api/v1/orders', { params })
    return res.data?.orders || []
  },

  // GET /api/v1/orders/{id} — Get single order
  getOrder: async (id: string): Promise<Order> => {
    const res = await client.get<Order>(`/api/v1/orders/${id}`)
    return res.data
  },

  // POST /api/v1/orders/{id}/cancel — Cancel an open order
  cancelOrder: async (id: string): Promise<Order> => {
    const res = await client.post<Order>(`/api/v1/orders/${id}/cancel`)
    return res.data
  },
}
