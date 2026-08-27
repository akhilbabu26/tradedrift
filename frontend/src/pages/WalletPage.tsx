import { useEffect, useState, useCallback, useMemo } from 'react'
import toast from 'react-hot-toast'
import AppLayout from '../components/layout/AppLayout'
import PortfolioSummaryCards from '../components/wallet/PortfolioSummaryCards'
import AssetBalancesTable, { type AssetRowData } from '../components/wallet/AssetBalancesTable'
import TestnetFaucetCard from '../components/wallet/TestnetFaucetCard'
import FinancialLedger, { type LedgerItem } from '../components/wallet/FinancialLedger'
import { DepositModal, WithdrawModal } from '../components/wallet/DepositWithdrawModals'
import { walletApi, type Balance } from '../api/wallet'
import { orderApi } from '../api/order'

const BASE_ASSETS: AssetRowData[] = [
  {
    symbol: 'USDT',
    name: 'Tether',
    iconChar: '₮',
    iconBg: 'bg-[#00e676]/15 border-[#00e676]/30',
    iconText: 'text-[#00e676]',
    total: 0,
    available: 0,
    inOrders: 0,
    priceUsd: 1.0,
  },
  {
    symbol: 'BTC',
    name: 'Bitcoin',
    iconChar: '₿',
    iconBg: 'bg-[#f7931a]/15 border-[#f7931a]/30',
    iconText: 'text-[#f7931a]',
    total: 0,
    available: 0,
    inOrders: 0,
    priceUsd: 96450.0,
  },
  {
    symbol: 'ETH',
    name: 'Ethereum',
    iconChar: 'Ξ',
    iconBg: 'bg-[#627eea]/15 border-[#627eea]/30',
    iconText: 'text-[#627eea]',
    total: 0,
    available: 0,
    inOrders: 0,
    priceUsd: 2780.5,
  },
  {
    symbol: 'SOL',
    name: 'Solana',
    iconChar: 'S',
    iconBg: 'bg-[#00e5ff]/15 border-[#00e5ff]/30',
    iconText: 'text-[#00e5ff]',
    total: 0,
    available: 0,
    inOrders: 0,
    priceUsd: 188.2,
  },
]

export default function WalletPage() {
  const [assetRows, setAssetRows] = useState<AssetRowData[]>(BASE_ASSETS)
  const [activeDepositAsset, setActiveDepositAsset] = useState<string | null>(null)
  const [activeWithdrawAsset, setActiveWithdrawAsset] = useState<string | null>(null)
  const [ledgerTxns, setLedgerTxns] = useState<LedgerItem[]>([])

  // Fetch live balances and orders from backend
  const fetchLiveBalances = useCallback(async () => {
    try {
      const [apiBalances, orders] = await Promise.all([
        walletApi.getAllBalances().catch(() => []),
        orderApi.listOrders({ limit: 20 }).catch(() => []),
      ])

      if (apiBalances && apiBalances.length > 0) {
        setAssetRows((prevRows) =>
          prevRows.map((row) => {
            const found = apiBalances.find((b: Balance) => b.asset === row.symbol)
            if (found) {
              const avail = parseFloat(String(found.availableBalance).replace(/,/g, '')) || 0
              const resv = parseFloat(String(found.reservedBalance).replace(/,/g, '')) || 0
              return {
                ...row,
                available: avail,
                inOrders: resv,
                total: avail + resv,
              }
            }
            return row
          })
        )
      }

      // Populate ledger from real orders if available
      if (orders && orders.length > 0) {
        const mappedTxns: LedgerItem[] = orders.map((o) => {
          const isBuy = o.side === 'BUY'
          const numPrice = parseFloat(o.price || '0')
          const numQty = parseFloat(o.quantity || '0')
          const totalVal = numPrice * numQty

          return {
            id: o.id,
            txHash: `0x${o.id.substring(0, 4)}...${o.id.substring(o.id.length - 4)}`,
            type: o.status === 'FILLED' ? 'Trade Fill' : 'Order Lock',
            asset: o.market_id.split('-')[0] || 'USDT',
            amount: `${isBuy ? '+' : '-'}${numQty.toFixed(4)} ${o.market_id.split('-')[0]}`,
            amountNum: isBuy ? numQty : -numQty,
            usdValue: `${isBuy ? '+' : '-'}$${totalVal.toLocaleString('en-US', { minimumFractionDigits: 2 })}`,
            dateTimeUtc: new Date(o.created_at || Date.now()).toLocaleString('en-US', {
              month: 'short',
              day: 'numeric',
              year: 'numeric',
              hour: '2-digit',
              minute: '2-digit',
              second: '2-digit',
              hour12: false,
            }),
            status: o.status === 'FILLED' ? 'Completed' : 'Pending',
          }
        })
        setLedgerTxns(mappedTxns)
      }
    } catch {
      // keep state
    }
  }, [])

  useEffect(() => {
    fetchLiveBalances()
  }, [fetchLiveBalances])

  // Aggregate Portfolio Totals
  const { totalEquityUsd, availableUsd, reservedUsd, btcPrice } = useMemo(() => {
    const btcAsset = assetRows.find((a) => a.symbol === 'BTC')
    const btcP = btcAsset ? btcAsset.priceUsd : 96450.0

    let totalEq = 0
    let availUsd = 0
    let resvUsd = 0

    for (const a of assetRows) {
      totalEq += a.total * a.priceUsd
      availUsd += a.available * a.priceUsd
      resvUsd += a.inOrders * a.priceUsd
    }

    return {
      totalEquityUsd: totalEq,
      availableUsd: availUsd,
      reservedUsd: resvUsd,
      btcPrice: btcP,
    }
  }, [assetRows])

  const totalBtcEquiv = btcPrice > 0 ? totalEquityUsd / btcPrice : 0
  const availableBtcEquiv = btcPrice > 0 ? availableUsd / btcPrice : 0
  const reservedBtcEquiv = btcPrice > 0 ? reservedUsd / btcPrice : 0

  // Handle 1-Click Testnet Faucet Claim
  const handleFaucetClaim = async (asset: string, amount: number) => {
    await new Promise((resolve) => setTimeout(resolve, 500))

    setAssetRows((prev) =>
      prev.map((row) => {
        if (row.symbol === asset) {
          const newAvail = row.available + amount
          const newTotal = row.total + amount
          return {
            ...row,
            available: newAvail,
            total: newTotal,
          }
        }
        return row
      })
    )

    // Add entry to Financial Ledger
    const randomHex = Array.from({ length: 8 }, () => Math.floor(Math.random() * 16).toString(16)).join('')
    const targetAsset = assetRows.find((a) => a.symbol === asset)
    const price = targetAsset ? targetAsset.priceUsd : 1
    const usdVal = amount * price

    const newTx: LedgerItem = {
      id: Date.now().toString(),
      txHash: `0x${randomHex.substring(0, 4)}...${randomHex.substring(4)}`,
      type: 'Faucet Deposit',
      asset,
      amount: `+${amount.toLocaleString('en-US', { minimumFractionDigits: asset === 'USDT' ? 2 : 4 })} ${asset}`,
      amountNum: amount,
      usdValue: `+$${usdVal.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`,
      dateTimeUtc: new Date().toLocaleString('en-US', {
        month: 'short',
        day: 'numeric',
        year: 'numeric',
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        hour12: false,
      }),
      status: 'Completed',
    }

    setLedgerTxns((prev) => [newTx, ...prev])
    toast.success(`Claimed +${amount} ${asset} testnet tokens!`)
  }

  // Handle Withdrawal Action
  const handleWithdrawSuccess = (asset: string, amount: number) => {
    setAssetRows((prev) =>
      prev.map((row) => {
        if (row.symbol === asset) {
          const newAvail = Math.max(0, row.available - amount)
          const newTotal = Math.max(0, row.total - amount)
          return {
            ...row,
            available: newAvail,
            total: newTotal,
          }
        }
        return row
      })
    )

    const randomHex = Array.from({ length: 8 }, () => Math.floor(Math.random() * 16).toString(16)).join('')
    const targetAsset = assetRows.find((a) => a.symbol === asset)
    const price = targetAsset ? targetAsset.priceUsd : 1
    const usdVal = amount * price

    const newTx: LedgerItem = {
      id: Date.now().toString(),
      txHash: `0x${randomHex.substring(0, 4)}...${randomHex.substring(4)}`,
      type: 'Withdrawal',
      asset,
      amount: `-${amount.toLocaleString('en-US', { minimumFractionDigits: asset === 'USDT' ? 2 : 4 })} ${asset}`,
      amountNum: -amount,
      usdValue: `-$${usdVal.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`,
      dateTimeUtc: new Date().toLocaleString('en-US', {
        month: 'short',
        day: 'numeric',
        year: 'numeric',
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        hour12: false,
      }),
      status: 'Completed',
    }

    setLedgerTxns((prev) => [newTx, ...prev])
  }

  const selectedWithdrawBalance = useMemo(() => {
    if (!activeWithdrawAsset) return 0
    const found = assetRows.find((a) => a.symbol === activeWithdrawAsset)
    return found ? found.available : 0
  }, [activeWithdrawAsset, assetRows])

  return (
    <AppLayout>
      <div className="flex flex-col space-y-6 max-w-[1920px] mx-auto select-none pb-12">
        {/* ── 1. Page Header ── */}
        <div className="flex flex-col">
          <h1 className="text-xl lg:text-2xl font-black text-white tracking-tight font-sans">
            Wallet
          </h1>
          <p className="text-xs text-slate-400 font-sans mt-0.5">
            Manage your assets and funds securely
          </p>
        </div>

        {/* ── 2. Portfolio Summary Cards ── */}
        <PortfolioSummaryCards
          totalEquityUsd={totalEquityUsd}
          totalBtcEquiv={totalBtcEquiv}
          availableUsd={availableUsd}
          availableBtcEquiv={availableBtcEquiv}
          reservedUsd={reservedUsd}
          reservedBtcEquiv={reservedBtcEquiv}
          pnlPercent={0.0}
          pnlUsd={0.0}
        />

        {/* ── 3 & 4. Asset Balances (8 Cols) + 1-Click Testnet Faucet (4 Cols) ── */}
        <div className="grid grid-cols-1 lg:grid-cols-12 gap-6 items-start">
          {/* Asset Balances Table */}
          <div className="lg:col-span-8">
            <AssetBalancesTable
              assets={assetRows}
              onDeposit={(asset) => setActiveDepositAsset(asset)}
              onWithdraw={(asset) => setActiveWithdrawAsset(asset)}
            />
          </div>

          {/* 1-Click Testnet Faucet Card */}
          <div className="lg:col-span-4 h-full">
            <TestnetFaucetCard onClaim={handleFaucetClaim} />
          </div>
        </div>

        {/* ── 5. Financial Ledger (Full Width) ── */}
        <div>
          <FinancialLedger transactions={ledgerTxns} />
        </div>
      </div>

      {/* ── Deposit & Withdraw Interactive Modals ── */}
      {activeDepositAsset && (
        <DepositModal
          isOpen={true}
          asset={activeDepositAsset}
          onClose={() => setActiveDepositAsset(null)}
        />
      )}

      {activeWithdrawAsset && (
        <WithdrawModal
          isOpen={true}
          asset={activeWithdrawAsset}
          availableBalance={selectedWithdrawBalance}
          onClose={() => setActiveWithdrawAsset(null)}
          onWithdrawSuccess={handleWithdrawSuccess}
        />
      )}
    </AppLayout>
  )
}
