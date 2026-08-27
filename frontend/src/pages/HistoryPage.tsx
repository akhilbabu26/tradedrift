import { useState, useMemo } from 'react'
import toast from 'react-hot-toast'
import AppLayout from '../components/layout/AppLayout'
import HistoryKpiCards from '../components/history/HistoryKpiCards'
import HistoryFilterToolbar from '../components/history/HistoryFilterToolbar'
import ExecutionFillsTable, { type ExecutionFillItem } from '../components/history/ExecutionFillsTable'

const INITIAL_EXECUTIONS: ExecutionFillItem[] = [
  {
    id: '1',
    executionId: 'exe_8f7c2d1a9b3e4f7a',
    marketId: 'BTC-USDT',
    marketSymbol: 'BTC/USDT',
    iconChar: '₿',
    iconBg: 'bg-[#f7931a]/15 border-[#f7931a]/30',
    iconText: 'text-[#f7931a]',
    side: 'BUY',
    role: 'MAKER',
    executedPrice: '96,450.00',
    filledQuantity: '0.1500 BTC',
    totalValueUsd: '$14,467.50',
    feeUsdt: '2.89',
    realizedPnl: '+$125.40',
    isPnlPositive: true,
    timestampUtc: 'Aug 27, 2026 14:32:05.123',
  },
  {
    id: '2',
    executionId: 'exe_3a9b1e7d5c2f4a8b',
    marketId: 'ETH-USDT',
    marketSymbol: 'ETH/USDT',
    iconChar: 'Ξ',
    iconBg: 'bg-[#627eea]/15 border-[#627eea]/30',
    iconText: 'text-[#627eea]',
    side: 'SELL',
    role: 'TAKER',
    executedPrice: '2,780.50',
    filledQuantity: '1.0000 ETH',
    totalValueUsd: '$2,780.50',
    feeUsdt: '0.56',
    realizedPnl: '+$45.20',
    isPnlPositive: true,
    timestampUtc: 'Aug 27, 2026 13:58:44.512',
  },
  {
    id: '3',
    executionId: 'exe_1b2c3d4e5f6a7b8c',
    marketId: 'BTC-USDT',
    marketSymbol: 'BTC/USDT',
    iconChar: '₿',
    iconBg: 'bg-[#f7931a]/15 border-[#f7931a]/30',
    iconText: 'text-[#f7931a]',
    side: 'SELL',
    role: 'MAKER',
    executedPrice: '97,200.00',
    filledQuantity: '0.2000 BTC',
    totalValueUsd: '$19,440.00',
    feeUsdt: '3.89',
    realizedPnl: '+$310.80',
    isPnlPositive: true,
    timestampUtc: 'Aug 27, 2026 12:21:10.845',
  },
  {
    id: '4',
    executionId: 'exe_7c8d9e0f1a2b3c4d',
    marketId: 'SOL-USDT',
    marketSymbol: 'SOL/USDT',
    iconChar: 'S',
    iconBg: 'bg-[#00e5ff]/15 border-[#00e5ff]/30',
    iconText: 'text-[#00e5ff]',
    side: 'BUY',
    role: 'TAKER',
    executedPrice: '188.20',
    filledQuantity: '10.0000 SOL',
    totalValueUsd: '$1,882.00',
    feeUsdt: '0.37',
    realizedPnl: '-$12.30',
    isPnlPositive: false,
    timestampUtc: 'Aug 27, 2026 11:45:33.221',
  },
  {
    id: '5',
    executionId: 'exe_9d8c7b6a5e4f3d2c',
    marketId: 'ETH-USDT',
    marketSymbol: 'ETH/USDT',
    iconChar: 'Ξ',
    iconBg: 'bg-[#627eea]/15 border-[#627eea]/30',
    iconText: 'text-[#627eea]',
    side: 'BUY',
    role: 'MAKER',
    executedPrice: '2,745.00',
    filledQuantity: '0.5000 ETH',
    totalValueUsd: '$1,372.50',
    feeUsdt: '0.27',
    realizedPnl: '+$18.90',
    isPnlPositive: true,
    timestampUtc: 'Aug 27, 2026 10:33:18.654',
  },
  {
    id: '6',
    executionId: 'exe_0a1b2c3d4e5f6a7b',
    marketId: 'BTC-USDT',
    marketSymbol: 'BTC/USDT',
    iconChar: '₿',
    iconBg: 'bg-[#f7931a]/15 border-[#f7931a]/30',
    iconText: 'text-[#f7931a]',
    side: 'BUY',
    role: 'TAKER',
    executedPrice: '95,800.25',
    filledQuantity: '0.1000 BTC',
    totalValueUsd: '$9,580.03',
    feeUsdt: '1.92',
    realizedPnl: '-$22.10',
    isPnlPositive: false,
    timestampUtc: 'Aug 27, 2026 09:17:04.877',
  },
  {
    id: '7',
    executionId: 'exe_6e5d4c3b2a1f0e9d',
    marketId: 'SOL-USDT',
    marketSymbol: 'SOL/USDT',
    iconChar: 'S',
    iconBg: 'bg-[#00e5ff]/15 border-[#00e5ff]/30',
    iconText: 'text-[#00e5ff]',
    side: 'SELL',
    role: 'MAKER',
    executedPrice: '190.00',
    filledQuantity: '5.0000 SOL',
    totalValueUsd: '$950.00',
    feeUsdt: '0.19',
    realizedPnl: '+$6.50',
    isPnlPositive: true,
    timestampUtc: 'Aug 27, 2026 08:54:22.331',
  },
  {
    id: '8',
    executionId: 'exe_f1e2d3c4b5a69788',
    marketId: 'BTC-USDT',
    marketSymbol: 'BTC/USDT',
    iconChar: '₿',
    iconBg: 'bg-[#f7931a]/15 border-[#f7931a]/30',
    iconText: 'text-[#f7931a]',
    side: 'SELL',
    role: 'TAKER',
    executedPrice: '96,980.00',
    filledQuantity: '0.0500 BTC',
    totalValueUsd: '$4,849.00',
    feeUsdt: '0.97',
    realizedPnl: '+$8.75',
    isPnlPositive: true,
    timestampUtc: 'Aug 27, 2026 07:23:45.998',
  },
  {
    id: '9',
    executionId: 'exe_a1b2c3d4e5f6a7b9c',
    marketId: 'ETH-USDT',
    marketSymbol: 'ETH/USDT',
    iconChar: 'Ξ',
    iconBg: 'bg-[#627eea]/15 border-[#627eea]/30',
    iconText: 'text-[#627eea]',
    side: 'SELL',
    role: 'MAKER',
    executedPrice: '2,752.10',
    filledQuantity: '0.7000 ETH',
    totalValueUsd: '$1,926.47',
    feeUsdt: '0.38',
    realizedPnl: '-$15.40',
    isPnlPositive: false,
    timestampUtc: 'Aug 27, 2026 06:42:13.456',
  },
  {
    id: '10',
    executionId: 'exe_b1c2d3e4f5a6b7c8d',
    marketId: 'SOL-USDT',
    marketSymbol: 'SOL/USDT',
    iconChar: 'S',
    iconBg: 'bg-[#00e5ff]/15 border-[#00e5ff]/30',
    iconText: 'text-[#00e5ff]',
    side: 'BUY',
    role: 'MAKER',
    executedPrice: '187.50',
    filledQuantity: '8.0000 SOL',
    totalValueUsd: '$1,500.00',
    feeUsdt: '0.15',
    realizedPnl: '+$4.20',
    isPnlPositive: true,
    timestampUtc: 'Aug 27, 2026 05:31:55.109',
  },
]

export default function HistoryPage() {
  const [selectedMarket, setSelectedMarket] = useState('ALL')
  const [selectedSide, setSelectedSide] = useState('ALL')

  const filteredExecutions = useMemo(() => {
    return INITIAL_EXECUTIONS.filter((e) => {
      const matchMarket = selectedMarket === 'ALL' || e.marketId === selectedMarket
      const matchSide = selectedSide === 'ALL' || e.side === selectedSide
      return matchMarket && matchSide
    })
  }, [selectedMarket, selectedSide])

  const handleExportCsv = () => {
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
        <HistoryKpiCards />

        {/* ── 3. Filter Toolbar ── */}
        <HistoryFilterToolbar
          selectedMarket={selectedMarket}
          onMarketChange={setSelectedMarket}
          selectedSide={selectedSide}
          onSideChange={setSelectedSide}
          onExportCsv={handleExportCsv}
        />

        {/* ── 4. Execution Fills Table ── */}
        <ExecutionFillsTable executions={filteredExecutions} />
      </div>
    </AppLayout>
  )
}
