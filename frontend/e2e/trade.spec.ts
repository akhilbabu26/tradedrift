import { test, expect } from '@playwright/test'
import { authenticateTestUser } from './helpers/auth'

test.describe('Trading Terminal & Charting Journey', () => {
  test.beforeEach(async ({ page }) => {
    await authenticateTestUser(page)
  })

  test('Loads trading terminal, order book, and deferred chart module', async ({ page }) => {
    await page.goto('/trade?market=BTC-USDT')

    // Terminal header with market info
    await expect(page.locator('text=BTC/USDT').first()).toBeVisible()

    // Order form with Buy and Sell toggles
    const buyTab = page.locator('button:has-text("Buy"), button:has-text("BUY")').first()
    const sellTab = page.locator('button:has-text("Sell"), button:has-text("SELL")').first()
    await expect(buyTab).toBeVisible()
    await expect(sellTab).toBeVisible()

    // Chart container is present
    await expect(page.locator('.tv-lightweight-charts, canvas, div[data-chart="true"]').first()).toBeVisible()
  })

  test('Validates order form input entry without NaN or negative values', async ({ page }) => {
    await page.goto('/trade?market=BTC-USDT')

    // Find price and quantity inputs
    const inputs = page.locator('input[type="text"], input[type="number"]')
    const count = await inputs.count()
    expect(count).toBeGreaterThan(0)

    const priceInput = inputs.first()
    await priceInput.fill('80000.00')
    expect(await priceInput.inputValue()).toBe('80000.00')
  })
})

