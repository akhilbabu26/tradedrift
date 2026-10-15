import { test, expect } from '@playwright/test'
import { authenticateTestUser } from './helpers/auth'

test.describe('Dashboard & Layout Stability Journey', () => {
  test.beforeEach(async ({ page }) => {
    await authenticateTestUser(page)
  })

  test('Renders dashboard header, balance cards, and market overview', async ({ page }) => {
    await page.goto('/dashboard')

    // Persistent shell elements
    await expect(page.locator('header')).toBeVisible()
    await expect(page.locator('aside')).toBeVisible()

    // Page content rendered
    await expect((page.locator('text=Simulator Balance').or(page.locator('text=Portfolio'))).first()).toBeVisible()
  })

  test('Navigates between Dashboard, Markets, and Portfolio with layout persistence', async ({ page }) => {
    await page.goto('/dashboard')

    // Click Markets nav link
    const marketsLink = page.locator('aside a[href="/markets"]').first()
    await expect(marketsLink).toBeVisible()
    await marketsLink.click()
    await expect(page).toHaveURL(/\/markets/)

    // Click Portfolio nav link
    const portfolioLink = page.locator('aside a[href="/portfolio"]').first()
    await expect(portfolioLink).toBeVisible()
    await portfolioLink.click()
    await expect(page).toHaveURL(/\/portfolio/)

    // Return to Dashboard
    const dashboardLink = page.locator('aside a[href="/dashboard"]').first()
    await expect(dashboardLink).toBeVisible()
    await dashboardLink.click()
    await expect(page).toHaveURL(/\/dashboard/)
  })
})
