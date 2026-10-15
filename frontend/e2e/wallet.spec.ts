import { test, expect } from '@playwright/test'
import { authenticateTestUser } from './helpers/auth'

test.describe('Wallet & Deposit Journey', () => {
  test.beforeEach(async ({ page }) => {
    await authenticateTestUser(page)
  })

  test('Renders wallet balances and top-up trigger controls', async ({ page }) => {
    await page.goto('/wallet')

    // Page title and balance card
    await expect(page.locator('h1, h2, h3:has-text("Wallet")').first()).toBeVisible()
    await expect((page.locator('text=Virtual USDT').or(page.locator('text=USDT'))).first()).toBeVisible()

    // Top-up action button is clickable
    const topUpBtn = page.locator('button:has-text("Top Up"), button:has-text("Deposit")').first()
    await expect(topUpBtn).toBeVisible()
  })
})
