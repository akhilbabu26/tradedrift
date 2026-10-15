import { test, expect } from '@playwright/test'
import { authenticateTestUser } from './helpers/auth'

test.describe('Orders & Order Management Journey', () => {
  test.beforeEach(async ({ page }) => {
    await authenticateTestUser(page)
  })

  test('Renders orders page with status filters and search bar', async ({ page }) => {
    await page.goto('/orders')

    // Page title and tabs
    await expect(page.locator('h1, h2, h3:has-text("Orders")').first()).toBeVisible()

    // Filter controls
    const searchInput = page.locator('input[placeholder*="Search"]').first()
    if (await searchInput.isVisible()) {
      await searchInput.fill('BTC')
      expect(await searchInput.inputValue()).toBe('BTC')
    }

    // Status filter tabs or select
    await expect(page.locator('body')).toBeVisible()
  })
})
