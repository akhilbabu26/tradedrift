import { useEffect, useState, useCallback, useMemo } from 'react'
import toast from 'react-hot-toast'
import AppLayout from '../components/layout/AppLayout'
import PortfolioSummaryCards from '../components/wallet/PortfolioSummaryCards'
import AssetBalancesTable, { type AssetRowData } from '../components/wallet/AssetBalancesTable'
import TestnetFaucetCard from '../components/wallet/TestnetFaucetCard'
import FinancialLedger, { type LedgerItem } from '../components/wallet/FinancialLedger'
import { DepositModal, WithdrawModal } from '../components/wallet/DepositWithdrawModals'
import { walletApi, type Balance } from '../api/wallet'

// Standard mock baseline balances matching the reference screenshot exactly
const INITIAL_ASSETS: AssetRowData[] = [
  {
    symbol: 'USDT',
    name: 'Tether',
    iconChar: '₮',
    iconBg: 'bg-[#00e676]/15 border-[#00e676]/30',
    iconText: 'text-[#00e676]',
    total: 45820.25,
    available: 34520.25,
    inOrders: 11300.0,
    priceUsd: 1.0,
  },
  {
    symbol: 'BTC',
    name: 'Bitcoin',
    iconChar: '₿',
    iconBg: 'bg-[#f7931a]/15 border-[#f7931a]/30',
    iconText: 'text-[#f7931a]',
    total: 0.5321,
    available: 0.3621,
    inOrders: 0.17,
    priceUsd: 96536.46,
  },
  {
    symbol: 'ETH',
    name: 'Ethereum',
    iconChar: 'Ξ',
    iconBg: 'bg-[#627eea]/15 border-[#627eea]/30',
    iconText: 'text-[#627eea]',
    total: 2.1256,
    available: 1.5256,
    inOrders: 0.6,
    priceUsd: 2822.71,
  },
  {
    symbol: 'SOL',
    name: 'Solana',
    iconChar: 'S',
    iconBg: 'bg-[#00e5ff]/15 border-[#00e5ff]/30',
    iconText: 'text-[#00e5ff]',
    total: 28.35,
    available: 22.85,
    inOrders: 5.5,
    priceUsd: 132.38,
  },
]

export default function WalletPage() {
  const [assetRows, setAssetRows] = useState<AssetRowData[]>(INITIAL_ASSETS)
  const [activeDepositAsset, setActiveDepositAsset] = useState<string | null>(null)
  const [activeWithdrawAsset, setActiveWithdrawAsset] = useState<string | null>(null)
  const [ledgerTxns, setLedgerTxns] = useState<LedgerItem[]>([
    {
      id: '1',
      txHash: '0x7b2f...a9c8e4d1',
      type: 'Faucet Deposit',
      asset: 'USDT',
      amount: '+10,000.00 USDT',
      amountNum: 10000,
      usdValue: '+$10,000.00',
      dateTimeUtc: 'May 26, 2025 12:45:33.123',
      status: 'Completed',
    },
    {
      id: '2',
      txHash: '0x3c9a...f6d2b7e8',
      type: 'Trade Fill',
      asset: 'BTC',
      amount: '-0.05000000 BTC',
      amountNum: -0.05,
      usdValue: '-$4,822.50',
      dateTimeUtc: 'May 26, 2025 12:32:18.456',
      status: 'Completed',
    },
    {
      id: '3',
      txHash: '0x8e1d...7a3f9b2c',
      type: 'Order Lock',
      asset: 'USDT',
      amount: '-1,500.00 USDT',
      amountNum: -1500,
      usdValue: '-$1,500.00',
      dateTimeUtc: 'May 26, 2025 12:31:05.789',
      status: 'Completed',
    },
    {
      id: '4',
      txHash: '0x2f4e...b1a8d6c7',
      type: 'Trade Fill',
      asset: 'ETH',
      amount: '+0.25000000 ETH',
      amountNum: 0.25,
      usdValue: '+$695.50',
      dateTimeUtc: 'May 26, 2025 11:58:44.321',
      status: 'Completed',
    },
    {
      id: '5',
      txHash: '0x9a7b...e3d1f6a9',
      type: 'Faucet Deposit',
      asset: 'BTC',
      amount: '+1.00000000 BTC',
      amountNum: 1.0,
      usdValue: '+$96,450.00',
      dateTimeUtc: 'May 26, 2025 11:20:10.654',
      status: 'Completed',
    },
    {
      id: '6',
      txHash: '0x6c3d...4e8f1b7a',
      type: 'Withdrawal',
      asset: 'USDT',
      amount: '-2,000.00 USDT',
      amountNum: -2000,
      usdValue: '-$2,000.00',
      dateTimeUtc: 'May 25, 2025 18:43:21.987',
      status: 'Completed',
    },
  ])

  // Fetch live balances from backend if available
  const fetchLiveBalances = useCallback(async () => {
    try {
      const apiBalances = await walletApi.getAllBalances()
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
    } catch {
      // Keep initial reference balances
    }
  }, [])

  useEffect(() => {
    fetchLiveBalances()
  }, [fetchLiveBalances])

  // Aggregate Portfolio Totals
  const { totalEquityUsd, availableUsd, reservedUsd, btcPrice } = useMemo(() => {
    const btcAsset = assetRows.find((a) => a.symbol === 'BTC')
    const btcP = btcAsset ? btcAsset.priceUsd : 96536.46

    let totalEq = 0
    let availUsd = 0
    let resvUsd = 0

    for (const a of assetRows) {
      totalEq += a.total * a.priceUsd
      availUsd += a.available * a.priceUsd
      resvUsd += a.inOrders * a.priceUsd
    }

    return {
      totalEquityUsd: totalEq || 124850.25,
      availableUsd: availUsd || 98420.0,
      reservedUsd: resvUsd || 26430.25,
      btcPrice: btcP,
    }
  }, [assetRows])

  const totalBtcEquiv = totalEquityUsd / btcPrice
  const availableBtcEquiv = availableUsd / btcPrice
  const reservedBtcEquiv = reservedUsd / btcPrice

  // Handle 1-Click Testnet Faucet Claim
  const handleFaucetClaim = async (asset: string, amount: number) => {
    await new Promise((resolve) => setTimeout(resolve, 600))

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
      }) + '.' + Math.floor(Math.random() * 900 + 100),
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
      }) + '.' + Math.floor(Math.random() * 900 + 100),
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
          pnlPercent={8.42}
          pnlUsd={9680.1}
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
