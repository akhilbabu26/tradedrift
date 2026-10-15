import { describe, it, expect, vi } from 'vitest'
import { renderToString } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import WalletSummary from './WalletSummary'
import TopUpVirtualUSDTCard from './TopUpVirtualUSDTCard'
import WhyTopUp from './WhyTopUp'
import WalletAssets from './WalletAssets'
import TopUpPanel from './TopUpPanel'
import TopUpHistory from './TopUpHistory'
import WalletHeader from './WalletHeader'
import { surfaceTokens } from '../common/Surface'

describe('Wallet Page — Consistent Monochrome 3D Card Styling Audit', () => {
  const mockSummary = {
    totalWalletValue: '19705.80',
    availableValue: '19705.80',
    lockedValue: '0.00',
    availablePct: 100,
    lockedPct: 0,
  }

  const mockDailyUsage = {
    userId: 'test-user-123',
    limitInr: 10,
    dailyLimitInr: 10,
    reservedInr: 0,
    consumedInr: 0,
    remainingInr: 10,
    availableInr: 10,
    usageDate: '2026-10-15',
    resetsAt: '2026-10-16T00:00:00Z',
    timezone: 'Asia/Kolkata (IST)',
  }

  // ── 1. Reference Style Consistency ──────────────────────────────────────────
  it('1. verifies Total Wallet Value uses the 3D raised-subtle charcoal surface', () => {
    const html = renderToString(<WalletSummary summary={mockSummary} />)

    // Gradient background matching approved 3D surface token
    expect(html).toContain('linear-gradient(180deg, #1B1D22 0%, #131518 100%)')
    // Upper-edge highlight
    expect(html).toContain('border-top:1px solid rgba(255, 255, 255, 0.11)')
    // Ambient box-shadow depth
    expect(html).toContain('box-shadow:0 8px 20px -4px rgba(0, 0, 0, 0.55)')
    // Rounded-xl container
    expect(html).toContain('rounded-xl')
  })

  // ── 2. Top Up Virtual USDT Card Styling ────────────────────────────────────
  it('2. verifies Top Up Virtual USDT Card applies the same 3D surface and borders', () => {
    const html = renderToString(
      <TopUpVirtualUSDTCard
        dailyUsage={mockDailyUsage}
        onTopUpClick={vi.fn()}
      />
    )

    // Uses the exact same gradient and upper edge highlight as reference
    expect(html).toContain('linear-gradient(180deg, #1B1D22 0%, #131518 100%)')
    expect(html).toContain('border-top:1px solid rgba(255, 255, 255, 0.11)')
    expect(html).toContain('box-shadow:0 8px 20px -4px rgba(0, 0, 0, 0.55)')
    expect(html).toContain('rounded-xl')
    // Retains rate information and top up action
    expect(html).toContain('1 INR = 1,000 Virtual USDT')
    expect(html).toContain('Top Up Virtual USDT')
  })

  // ── 3. Why Top Up Card Styling ─────────────────────────────────────────────
  it('3. verifies Why Top Up Card applies the dimensional surface without row clutter', () => {
    const html = renderToString(<WhyTopUp />)

    expect(html).toContain('linear-gradient(180deg, #1B1D22 0%, #131518 100%)')
    expect(html).toContain('border-top:1px solid rgba(255, 255, 255, 0.11)')
    expect(html).toContain('box-shadow:0 8px 20px -4px rgba(0, 0, 0, 0.55)')
    expect(html).toContain('Why Top Up?')
    expect(html).toContain('Get more simulated funds')
    expect(html).toContain('Secure Payments')
  })

  // ── 4. Assets in Wallet Table Container ────────────────────────────────────
  it('4. verifies Assets in Wallet table container uses the shared raised surface', () => {
    const html = renderToString(
      <MemoryRouter>
        <WalletAssets
          assets={[]}
          loading={false}
          onTopUpClick={vi.fn()}
        />
      </MemoryRouter>
    )

    expect(html).toContain('linear-gradient(180deg, #1B1D22 0%, #131518 100%)')
    expect(html).toContain('border-top:1px solid rgba(255, 255, 255, 0.11)')
    expect(html).toContain('Assets in Wallet')
    expect(html).toContain('Search assets...')
    expect(html).toContain('Hide zero balances')
  })

  // ── 5. Top Up Panel Form Card ──────────────────────────────────────────────
  it('5. verifies Top Up Panel uses the 3D surface and dark inset preview controls', () => {
    const html = renderToString(
      <TopUpPanel
        dailyUsage={mockDailyUsage}
        onPay={vi.fn()}
      />
    )

    expect(html).toContain('linear-gradient(180deg, #1B1D22 0%, #131518 100%)')
    expect(html).toContain('border-top:1px solid rgba(255, 255, 255, 0.11)')
    expect(html).toContain('Selected Amount')
    expect(html).toContain('You will receive')
    expect(html).toContain('Secure payment via Razorpay')
  })

  // ── 6. Top-Up History Table ────────────────────────────────────────────────
  it('6. verifies Top-Up History applies the shared raised surface', () => {
    const html = renderToString(<TopUpHistory history={[]} />)

    expect(html).toContain('linear-gradient(180deg, #1B1D22 0%, #131518 100%)')
    expect(html).toContain('border-top:1px solid rgba(255, 255, 255, 0.11)')
    expect(html).toContain('Top-Up History')
    expect(html).toContain('No top-up transactions yet')
  })

  // ── 7. Header Informational Disclaimer ─────────────────────────────────────
  it('7. verifies WalletHeader disclaimer pill adopts the consistent 3D surface', () => {
    const html = renderToString(<WalletHeader />)

    expect(html).toContain('linear-gradient(180deg, #1B1D22 0%, #131518 100%)')
    expect(html).toContain('Top-ups purchase virtual trading credits for the simulator')
  })

  // ── 8. Semantic Color Discipline ───────────────────────────────────────────
  it('8. enforces that green is reserved for positive/available and blue for locked orders', () => {
    const html = renderToString(<WalletSummary summary={mockSummary} />)

    // Green indicator for available
    expect(html).toContain('bg-[#10b981]')
    expect(html).toContain('Available to Trade')

    // Blue indicator for locked
    expect(html).toContain('bg-[#3b82f6]')
    expect(html).toContain('Locked in Open Orders')

    // Surface tokens remain monochrome charcoal/white
    expect(surfaceTokens.colors.canvas).toBe('#08090B')
    expect(surfaceTokens.colors.card).toBe('#151719')
  })
})
