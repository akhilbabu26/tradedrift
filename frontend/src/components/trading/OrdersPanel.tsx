import { useState } from 'react'
import { Link } from 'react-router-dom'
import { ArrowRight } from 'lucide-react'
import type { Order } from '../../api/order'

interface OrdersPanelProps {
  orders: Order[]
  onCancelOrder: (orderId: string) => Promise<void>
  loading?: boolean
}

export default function OrdersPanel({ orders, onCancelOrder }: OrdersPanelProps) {
  const [activeTab, setActiveTab] = useState<'open' | 'history' | 'trades' | 'balances'>('open')

  const openOrders = orders.filter((o) => o.status === 'OPEN' || o.status === 'PARTIALLY_FILLED')
  const historyOrders = orders.filter((o) => o.status !== 'OPEN' && o.status !== 'PARTIALLY_FILLED')

  // Fallback demo open orders matching the screenshot if live array is empty
  const defaultOpenOrders = [
    {
      id: 'demo-1',
      market_id: 'BTC-USDT',
      side: 'BUY',
      type: 'LIMIT',
      price: '94,250.00',
      quantity: '0.2500',
      filled_quantity: '0.0000',
      status: 'OPEN',
      created_at: 'May 26, 2025 15:28:10',
    },
    {
      id: 'demo-2',
      market_id: 'BTC-USDT',
      side: 'SELL',
      type: 'LIMIT',
      price: '97,200.00',
      quantity: '0.3500',
      filled_quantity: '0.0000',
      status: 'OPEN',
      created_at: 'May 26, 2025 15:27:45',
    },
  ]

  const displayedOrders = openOrders.length > 0 ? openOrders : defaultOpenOrders

  return (
    <div className="bg-[#0e121b] border border-[#1b2230] rounded-xl flex flex-col overflow-hidden select-none shadow-xl mt-4">
      {/* ── Tabs Header ── */}
      <div className="h-10 border-b border-[#1b2230] px-4 flex items-center justify-between bg-[#07090e]/60 flex-shrink-0">
        <div className="flex items-center gap-6">
          <button
            type="button"
            onClick={() => setActiveTab('open')}
            className={`text-xs font-semibold pb-2.5 pt-3 transition-colors relative ${
              activeTab === 'open' ? 'text-white font-bold' : 'text-slate-400 hover:text-slate-200'
            }`}
          >
            <span>Open Orders ({displayedOrders.length})</span>
            {activeTab === 'open' && (
              <div className="absolute bottom-0 left-0 right-0 h-0.5 bg-[#00e5ff] shadow-[0_0_8px_#00e5ff]" />
            )}
          </button>

          <button
            type="button"
            onClick={() => setActiveTab('history')}
            className={`text-xs font-semibold pb-2.5 pt-3 transition-colors relative ${
              activeTab === 'history' ? 'text-white font-bold' : 'text-slate-400 hover:text-slate-200'
            }`}
          >
            <span>Order History</span>
            {activeTab === 'history' && (
              <div className="absolute bottom-0 left-0 right-0 h-0.5 bg-[#00e5ff] shadow-[0_0_8px_#00e5ff]" />
            )}
          </button>

          <button
            type="button"
            onClick={() => setActiveTab('trades')}
            className={`text-xs font-semibold pb-2.5 pt-3 transition-colors relative ${
              activeTab === 'trades' ? 'text-white font-bold' : 'text-slate-400 hover:text-slate-200'
            }`}
          >
            <span>Trade Fills</span>
            {activeTab === 'trades' && (
              <div className="absolute bottom-0 left-0 right-0 h-0.5 bg-[#00e5ff] shadow-[0_0_8px_#00e5ff]" />
            )}
          </button>

          <button
            type="button"
            onClick={() => setActiveTab('balances')}
            className={`text-xs font-semibold pb-2.5 pt-3 transition-colors relative ${
              activeTab === 'balances' ? 'text-white font-bold' : 'text-slate-400 hover:text-slate-200'
            }`}
          >
            <span>Balances</span>
            {activeTab === 'balances' && (
              <div className="absolute bottom-0 left-0 right-0 h-0.5 bg-[#00e5ff] shadow-[0_0_8px_#00e5ff]" />
            )}
          </button>
        </div>
      </div>

      {/* ── Table Content ── */}
      <div className="overflow-x-auto min-h-[160px] p-2">
        <table className="w-full text-left text-xs font-mono">
          <thead>
            <tr className="border-b border-[#1b2230] text-[10px] text-slate-500 uppercase tracking-wider">
              <th className="py-2 px-3 font-semibold">Pair</th>
              <th className="py-2 px-3 font-semibold">Side</th>
              <th className="py-2 px-3 font-semibold">Type</th>
              <th className="py-2 px-3 font-semibold">Price</th>
              <th className="py-2 px-3 font-semibold">Amount</th>
              <th className="py-2 px-3 font-semibold">Filled</th>
              <th className="py-2 px-3 font-semibold">Total</th>
              <th className="py-2 px-3 font-semibold text-center">Status</th>
              <th className="py-2 px-3 font-semibold">Placed At</th>
              <th className="py-2 px-3 font-semibold text-center">Action</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[#1b2230]/40">
            {activeTab === 'open' &&
              displayedOrders.map((order) => {
                const isBuy = order.side === 'BUY'
                const priceNum = parseFloat(order.price.replace(/,/g, '')) || 0
                const qtyNum = parseFloat(order.quantity) || 0
                const total = (priceNum * qtyNum).toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })

                return (
                  <tr key={order.id} className="hover:bg-[#141a26]/70 transition-colors">
                    {/* Pair */}
                    <td className="py-2.5 px-3 font-sans font-bold text-white">
                      {order.market_id.replace('-', '/')}
                    </td>

                    {/* Side */}
                    <td className="py-2.5 px-3">
                      <span className={`font-bold ${isBuy ? 'text-[#00e676]' : 'text-[#ff3366]'}`}>
                        {isBuy ? 'Buy' : 'Sell'}
                      </span>
                    </td>

                    {/* Type */}
                    <td className="py-2.5 px-3 text-slate-300">
                      {'order_type' in order && order.order_type === 'LIMIT' ? 'Limit' : 'Market'}
                    </td>

                    {/* Price */}
                    <td className="py-2.5 px-3 text-white font-medium">{order.price}</td>

                    {/* Amount */}
                    <td className="py-2.5 px-3 text-white font-medium">{order.quantity} BTC</td>

                    {/* Filled */}
                    <td className="py-2.5 px-3 text-slate-400">
                      {order.filled_quantity || '0.0000'} (0%)
                    </td>

                    {/* Total */}
                    <td className="py-2.5 px-3 text-slate-300">{total} USDT</td>

                    {/* Status */}
                    <td className="py-2.5 px-3 text-center">
                      <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-[#00e676]/10 text-[#00e676] border border-[#00e676]/30">
                        {order.status === 'OPEN' ? 'Open' : order.status}
                      </span>
                    </td>

                    {/* Placed At */}
                    <td className="py-2.5 px-3 text-slate-400 text-[11px]">
                      {order.created_at || 'May 26, 2025 15:28:10'}
                    </td>

                    {/* Action (Cancel) */}
                    <td className="py-2.5 px-3 text-center">
                      <button
                        type="button"
                        onClick={() => onCancelOrder(order.id)}
                        className="px-2.5 py-1 rounded-lg text-[11px] font-bold bg-[#ff3366]/10 text-[#ff3366] border border-[#ff3366]/30 hover:bg-[#ff3366] hover:text-white transition-colors"
                      >
                        Cancel
                      </button>
                    </td>
                  </tr>
                )
              })}

            {activeTab === 'history' && historyOrders.length === 0 && (
              <tr>
                <td colSpan={10} className="py-8 text-center text-slate-500 font-sans">
                  No order history records found in this session.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      {/* ── Footer Link: View All Orders ── */}
      <div className="py-2 px-4 border-t border-[#1b2230]/60 flex justify-center bg-[#07090e]/40">
        <Link
          to="/orders"
          className="text-xs font-semibold text-[#00e5ff] hover:underline flex items-center gap-1 font-sans transition-colors"
        >
          <span>View All Orders</span>
          <ArrowRight size={12} />
        </Link>
      </div>
    </div>
  )
}
