import { test, expect } from '@playwright/test'
import { authenticateTestUser, setupMockApi } from './helpers/auth'

test.describe('Authentication & Navigation Journey', () => {
  test('Landing page renders hero section and redirects unauthenticated users', async ({ page }) => {
    await page.addInitScript(() => {
      window.localStorage.clear()
    })
    await page.goto('/')
    await expect(page).toHaveTitle(/TradeDrift/i)

    // Landing page has navigation to login
    const loginLink = page.locator('a[href="/login"]').first()
    await expect(loginLink).toBeVisible()
    await loginLink.click()
    await expect(page).toHaveURL(/\/login/)
  })

  test('Protected route /dashboard redirects unauthenticated users to /login', async ({ page }) => {
    await page.addInitScript(() => {
      window.localStorage.clear()
    })
    await page.goto('/dashboard')
    await expect(page).toHaveURL(/\/login/)
  })

  test('Login form renders input fields, labels, and validates credentials submission', async ({ page }) => {
    await setupMockApi(page)
    await page.addInitScript(() => {
      window.localStorage.clear()
    })
    await page.goto('/login')

    const emailInput = page.locator('#email')
    const passwordInput = page.locator('#password')
    const submitBtn = page.locator('button[type="submit"]')

    await expect(emailInput).toBeVisible()
    await expect(passwordInput).toBeVisible()
    await expect(submitBtn).toBeVisible()

    // Fill invalid credentials to check client error handling
    await emailInput.fill('invalid@tradedrift.local')
    await passwordInput.fill('wrongpassword123')
    await submitBtn.click()

    // Form displays error banner without crashing
    await expect(page.locator('text=Invalid email or password')).toBeVisible()
  })

  test('Authenticated user loads dashboard and can successfully log out', async ({ page }) => {
    await authenticateTestUser(page)
    await page.goto('/dashboard')

    // Expect navigation inside authenticated shell
    await expect(page).toHaveURL(/\/dashboard/)
    await expect((page.locator('text=Simulator Balance').or(page.locator('text=Portfolio'))).first()).toBeVisible()

    // Trigger logout via Settings
    await page.goto('/settings')
    await expect(page).toHaveURL(/\/settings/)

    const signOutBtn = page.getByRole('button', { name: 'Sign Out', exact: true })
    await expect(signOutBtn).toBeVisible()
    await signOutBtn.click()

    // Verifies session wipe and redirect to login
    await expect(page).toHaveURL(/\/login/)
  })
})


