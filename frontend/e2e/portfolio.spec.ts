import { test, expect } from '@playwright/test'
import { authenticateTestUser } from './helpers/auth'

test.describe('Portfolio & Asset Valuation Journey', () => {
  test.beforeEach(async ({ page }) => {
    await authenticateTestUser(page)
  })

  test('Renders portfolio metrics, asset allocation, and holdings list', async ({ page }) => {
    await page.goto('/portfolio')

    // Page loads without error
    await expect(page.locator('h1, h2, h3:has-text("Portfolio")').first()).toBeVisible()

    // Metric titles are visible
    await expect((page.locator('text=Total Portfolio Value').or(page.locator('text=Portfolio Value'))).first()).toBeVisible()

    // Assets table with supported symbols (BTC, ETH, SOL, USDT)
    await expect(page.locator('text=USDT').first()).toBeVisible()
  })
})
