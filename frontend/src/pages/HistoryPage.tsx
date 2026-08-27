import { useState, useEffect, useCallback, useMemo } from 'react'
import { Loader2 } from 'lucide-react'
import toast from 'react-hot-toast'
import AppLayout from '../components/layout/AppLayout'
import HistoryKpiCards from '../components/history/HistoryKpiCards'
import HistoryFilterToolbar from '../components/history/HistoryFilterToolbar'
import ExecutionFillsTable, { type ExecutionFillItem } from '../components/history/ExecutionFillsTable'
import { orderApi, type Order } from '../api/order'

export default function HistoryPage() {
  const [executions, setExecutions] = useState<ExecutionFillItem[]>([])
  const [loading, setLoading] = useState(true)
  const [selectedMarket, setSelectedMarket] = useState('ALL')
  const [selectedSide, setSelectedSide] = useState('ALL')

  const fetchExecutions = useCallback(async () => {
    try {
      setLoading(true)
      const filledOrders = await orderApi.listOrders({ status: 'FILLED' })
      if (filledOrders && filledOrders.length > 0) {
        const mapped: ExecutionFillItem[] = filledOrders.map((o: Order) => {
          const price = parseFloat(o.price || '0')
          const qty = parseFloat(o.filled_quantity || o.quantity || '0')
          const totalVal = price * qty
          const fee = totalVal * 0.0002 // 0.02% VIP fee tier
          const base = o.market_id.split('-')[0] || 'BTC'
          const isBtc = base === 'BTC'
          const isEth = base === 'ETH'

          return {
            id: o.id,
            executionId: `exe_${o.id.substring(0, 16)}`,
            marketId: o.market_id,
            marketSymbol: `${base}/USDT`,
            iconChar: isBtc ? '₿' : isEth ? 'Ξ' : 'S',
            iconBg: isBtc
              ? 'bg-[#f7931a]/15 border-[#f7931a]/30'
              : isEth
              ? 'bg-[#627eea]/15 border-[#627eea]/30'
              : 'bg-[#00e5ff]/15 border-[#00e5ff]/30',
            iconText: isBtc ? 'text-[#f7931a]' : isEth ? 'text-[#627eea]' : 'text-[#00e5ff]',
            side: o.side === 'BUY' ? 'BUY' : 'SELL',
            role: 'MAKER',
            executedPrice: price.toLocaleString('en-US', { minimumFractionDigits: 2 }),
            filledQuantity: `${qty.toFixed(4)} ${base}`,
            totalValueUsd: `$${totalVal.toLocaleString('en-US', { minimumFractionDigits: 2 })}`,
            feeUsdt: fee.toFixed(2),
            realizedPnl: '+$0.00',
            isPnlPositive: true,
            timestampUtc: new Date(o.updated_at || o.created_at || Date.now()).toLocaleString('en-US', {
              month: 'short',
              day: 'numeric',
              year: 'numeric',
              hour: '2-digit',
              minute: '2-digit',
              second: '2-digit',
              hour12: false,
            }),
          }
        })
        setExecutions(mapped)
      } else {
        setExecutions([])
      }
    } catch {
      setExecutions([])
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchExecutions()
  }, [fetchExecutions])

  const filteredExecutions = useMemo(() => {
    return executions.filter((e) => {
      const matchMarket = selectedMarket === 'ALL' || e.marketId === selectedMarket
      const matchSide = selectedSide === 'ALL' || e.side === selectedSide
      return matchMarket && matchSide
    })
  }, [executions, selectedMarket, selectedSide])

  const totalVolume = useMemo(() => {
    return executions.reduce((sum, e) => {
      const val = parseFloat(e.totalValueUsd.replace(/[$,]/g, '')) || 0
      return sum + val
    }, 0)
  }, [executions])

  const totalFees = useMemo(() => {
    return executions.reduce((sum, e) => sum + (parseFloat(e.feeUsdt) || 0), 0)
  }, [executions])

  const handleExportCsv = () => {
    if (filteredExecutions.length === 0) {
      toast.error('No execution records to export')
      return
    }
    const headers = [
      'Execution ID',
      'Market',
      'Side',
      'Role',
      'Executed Price',
      'Filled Quantity',
      'Total Value (USD)',
      'Fee (USDT)',
      'Realized PnL (USD)',
      'Timestamp (UTC)',
    ]
    const rows = filteredExecutions.map((e) => [
      e.executionId,
      e.marketSymbol,
      e.side,
      e.role,
      e.executedPrice,
      e.filledQuantity,
      e.totalValueUsd,
      e.feeUsdt,
      e.realizedPnl,
      e.timestampUtc,
    ])
    const csvContent =
      'data:text/csv;charset=utf-8,' + [headers.join(','), ...rows.map((e) => e.join(','))].join('\n')
    const encodedUri = encodeURI(csvContent)
    const link = document.createElement('a')
    link.setAttribute('href', encodedUri)
    link.setAttribute('download', `tradedrift_executions_${Date.now()}.csv`)
    document.body.appendChild(link)
    link.click()
    document.body.removeChild(link)
    toast.success('Trade executions exported to CSV / Tax report')
  }

  return (
    <AppLayout>
      <div className="flex flex-col space-y-6 max-w-[1920px] mx-auto select-none pb-12">
        {/* ── 1. Page Header ── */}
        <div className="flex flex-col">
          <h1 className="text-xl lg:text-2xl font-black text-white tracking-tight font-sans">
            Trade History &amp; Executions
          </h1>
          <p className="text-xs text-slate-400 font-sans mt-0.5">
            Complete history of your trades, executions, fees, and realized PnL
          </p>
        </div>

        {/* ── 2. Top Summary KPI Cards ── */}
        <HistoryKpiCards
          totalVolumeUsd={totalVolume}
          btcEquiv={totalVolume > 0 ? totalVolume / 96450 : 0}
          realizedPnlUsd={0}
          pnlChangePercent={0}
          feesPaidUsdt={totalFees}
          feeTier="VIP 1"
          feeRate="0.02%"
        />

        {/* ── 3. Filter Toolbar ── */}
        <HistoryFilterToolbar
          selectedMarket={selectedMarket}
          onMarketChange={setSelectedMarket}
          selectedSide={selectedSide}
          onSideChange={setSelectedSide}
          onExportCsv={handleExportCsv}
        />

        {/* ── 4. Execution Fills Table ── */}
        {loading ? (
          <div className="py-16 bg-[#0e121b] border border-[#1b2230] rounded-xl flex items-center justify-center text-slate-400">
            <Loader2 className="animate-spin mr-2" size={20} />
            <span>Loading trade executions...</span>
          </div>
        ) : (
          <ExecutionFillsTable executions={filteredExecutions} />
        )}
      </div>
    </AppLayout>
  )
}
