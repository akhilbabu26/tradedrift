import { describe, it, expect, vi } from 'vitest'
import { renderToString } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import WalletSummary from './WalletSummary'
import TopUpVirtualUSDTCard from './TopUpVirtualUSDTCard'
import TopUpPanel from './TopUpPanel'
import WalletAssets from './WalletAssets'
import type { EnrichedAssetRow, WalletSummaryMetrics } from '../../hooks/useWalletData'
import { getAssetMetadata } from '../../utils/marketMetadata'

describe('Wallet Data Integrity & Fallback Removal Audit', () => {
  it('displays loading state correctly without fabricating zero financial values', () => {
    const emptySummary: WalletSummaryMetrics = {
      totalWalletValue: '0.00',
      availableValue: '0.00',
      lockedValue: '0.00',
      availablePct: 100,
      lockedPct: 0,
    }
    const html = renderToString(<WalletSummary summary={emptySummary} loading={true} />)
    expect(html).toContain('animate-pulse')
    expect(html).not.toContain('$0.00')
  })

  it('handles unavailable daily usage explicitly and disables top-up submissions', () => {
    const cardHtml = renderToString(
      <TopUpVirtualUSDTCard dailyUsage={null} onTopUpClick={vi.fn()} />
    )
    expect(cardHtml).toContain('Today&#x27;s Top-Up Usage')
    expect(cardHtml).toContain('—')
    expect(cardHtml).toContain('Daily Limit: Unavailable')

    const panelHtml = renderToString(
      <TopUpPanel dailyUsage={null} onPay={vi.fn()} />
    )
    expect(panelHtml).toContain('Daily Limit Unavailable')
    expect(panelHtml).toContain('disabled=""')
  })

  it('renders authentic values when daily usage is valid', () => {
    const validUsage = {
      userId: 'user-1',
      limitInr: 10,
      dailyLimitInr: 10,
      reservedInr: 0,
      consumedInr: 4,
      remainingInr: 6,
      availableInr: 6,
      timezone: 'Asia/Kolkata (IST)',
      usageDate: '2026-10-15',
      resetsAt: '2026-10-16T00:00:00Z',
    }
    const cardHtml = renderToString(
      <TopUpVirtualUSDTCard dailyUsage={validUsage} onTopUpClick={vi.fn()} />
    )
    expect(cardHtml).toContain('4,000.00 / 10,000.00 USDT')
    expect(cardHtml).toContain('40%')
  })

  it('represents unavailable crypto prices explicitly as dash rather than fabricating fake zero or hardcoded prices', () => {
    const assetsWithUnavailablePrice: EnrichedAssetRow[] = [
      {
        asset: 'BTC',
        name: 'Bitcoin',
        meta: getAssetMetadata('BTC'),
        availableBalance: '0.1500',
        reservedBalance: '0.0000',
        totalBalance: '0.1500',
        price: '—',
        valueUSDT: '—',
        isZeroBalance: false,
      },
      {
        asset: 'USDT',
        name: 'Tether USD',
        meta: getAssetMetadata('USDT'),
        availableBalance: '1000.00',
        reservedBalance: '0.00',
        totalBalance: '1000.00',
        price: '1.00',
        valueUSDT: '1000.00',
        isZeroBalance: false,
      },
    ]

    const html = renderToString(
      <MemoryRouter>
        <WalletAssets assets={assetsWithUnavailablePrice} onTopUpClick={vi.fn()} />
      </MemoryRouter>
    )

    expect(html).toContain('BTC')
    expect(html).toContain('0.15')
    expect(html).toContain('—')
    expect(html).toContain('$1,000.00')
  })

  it('displays dash for wallet valuation when holdings have unavailable prices', () => {
    const summaryWithUnavailablePrices: WalletSummaryMetrics = {
      totalWalletValue: '—',
      availableValue: '—',
      lockedValue: '—',
      availablePct: 100,
      lockedPct: 0,
    }

    const html = renderToString(<WalletSummary summary={summaryWithUnavailablePrices} />)
    expect(html).toContain('—')
    expect(html).not.toContain('$—')
  })
})
