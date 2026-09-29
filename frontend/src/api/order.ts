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
  side: 'BUY' | 'SELL' | string
  order_type: 'LIMIT' | 'MARKET' | string
  status: 'OPEN' | 'PARTIALLY_FILLED' | 'FILLED' | 'CANCELLING' | 'CANCELLED' | 'REJECTED' | string
  price: string
  quantity: string
  filled_quantity: string
  created_at: string
  updated_at: string
}

export interface ListOrdersParams {
  market_id?: string
  // NOTE: `status` query param is NOT supported by the backend — passing it returns 422.
  // Filter by status client-side after fetching all orders.
  limit?: number
}

export function normalizeOrder(o: any): Order {
  if (!o) return o

  // Normalize side: ORDER_SIDE_BUY -> BUY, ORDER_SIDE_SELL -> SELL
  let side = (o.side || 'BUY').toUpperCase()
  if (side.includes('BUY')) side = 'BUY'
  else if (side.includes('SELL')) side = 'SELL'

  // Normalize order_type: ORDER_TYPE_LIMIT -> LIMIT, ORDER_TYPE_MARKET -> MARKET
  let order_type = (o.order_type || o.type || 'LIMIT').toUpperCase()
  if (order_type.includes('LIMIT')) order_type = 'LIMIT'
  else if (order_type.includes('MARKET')) order_type = 'MARKET'

  // Normalize status: ORDER_STATUS_OPEN -> OPEN, ORDER_STATUS_FILLED -> FILLED, etc.
  let status = (o.status || 'OPEN').toUpperCase()
  if (status.includes('PARTIALLY')) status = 'PARTIALLY_FILLED'
  else if (status.includes('FILLED')) status = 'FILLED'
  else if (status.includes('CANCELL')) status = status.includes('ING') ? 'CANCELLING' : 'CANCELLED'
  else if (status.includes('REJECT')) status = 'REJECTED'
  else if (status.includes('OPEN')) status = 'OPEN'

  return {
    id: o.id,
    user_id: o.user_id,
    market_id: o.market_id,
    side,
    order_type,
    status,
    price: o.price ? String(o.price) : '',
    quantity: o.quantity ? String(o.quantity) : '0',
    filled_quantity: o.filled_quantity ? String(o.filled_quantity) : '0',
    created_at: o.created_at || new Date().toISOString(),
    updated_at: o.updated_at || new Date().toISOString(),
  }
}

export const orderApi = {
  // POST /api/v1/orders — Create a new limit/market order
  createOrder: async (data: CreateOrderRequest): Promise<Order> => {
    // Generate a per-request idempotency key. The gateway reads Idempotency-Key
    // and the order service deduplicates on it, preventing duplicate orders if
    // the user double-clicks or the network retries the same submission.
    const idempotencyKey = crypto.randomUUID()
    const res = await client.post<Order>('/api/v1/orders', data, {
      headers: { 'Idempotency-Key': idempotencyKey },
    })
    return normalizeOrder(res.data)
  },

  // GET /api/v1/orders — List user orders
  listOrders: async (params?: ListOrdersParams): Promise<Order[]> => {
    const res = await client.get<{ orders: Order[] }>('/api/v1/orders', { params })
    const rawList = res.data?.orders || []
    return rawList.map(normalizeOrder)
  },

  // GET /api/v1/orders/{id} — Get single order
  getOrder: async (id: string): Promise<Order> => {
    const res = await client.get<Order>(`/api/v1/orders/${id}`)
    return normalizeOrder(res.data)
  },

  // POST /api/v1/orders/{id}/cancel — Cancel an open order
  cancelOrder: async (id: string): Promise<Order> => {
    const res = await client.post<Order>(`/api/v1/orders/${id}/cancel`)
    return normalizeOrder(res.data)
  },
}
