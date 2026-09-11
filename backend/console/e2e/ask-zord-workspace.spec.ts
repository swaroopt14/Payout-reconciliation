import { test, expect, type BrowserContext, type Page } from '@playwright/test'

const BASE_URL = process.env.PLAYWRIGHT_BASE_URL || 'http://localhost:3000'
const SESSION_TENANT = 'e2e-session-tenant-111'

async function installPayoutSessionCookies(context: BrowserContext) {
  const parsed = new URL(BASE_URL)
  const port = parsed.port ? `:${parsed.port}` : ''
  const origins = new Set<string>([
    `${parsed.protocol}//${parsed.hostname}${port}`,
    `${parsed.protocol}//localhost${port}`,
    `${parsed.protocol}//127.0.0.1${port}`,
  ])
  const cookies = [...origins].flatMap((url) => ([
    { name: 'zord_access_token', value: 'e2e-playwright-access', url },
    { name: 'zord_refresh_token', value: 'e2e-playwright-refresh', url },
    { name: 'zord_role', value: 'CUSTOMER_USER', url },
    { name: 'zord_session_present', value: '1', url },
  ]))
  await context.addCookies(cookies)
}

async function installAuthIntelligenceAndPromptMocks(page: Page) {
  await page.route('**/api/auth/**', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
        body: JSON.stringify({
          user: { id: 'e2e-user', name: 'E2E User', email: 'e2e@test.com', tenant_id: SESSION_TENANT },
          session: { tenant_id: SESSION_TENANT },
        }),
    })
  })

  await page.route('**/api/prompt-layer/query', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        answer: '18 payments need review in this workspace snapshot.',
        citations: [],
      }),
    })
  })
}

test.describe('Ask Zord workspace', () => {
  test.beforeEach(async ({ context, page }) => {
    await installPayoutSessionCookies(context)
    await installAuthIntelligenceAndPromptMocks(page)
  })

  test('loads Ask Zord', async ({ page }) => {
    await page.goto('/ask?demo=sandbox')

    await expect(page.getByRole('heading', { name: 'Ask Zord' })).toBeVisible()
    await expect(page.locator('#ask-zord-input')).toBeVisible()
  })

  test('typed prompt shows an answer', async ({ page }) => {
    await page.goto('/ask?demo=sandbox')
    await page.locator('#ask-zord-input').fill('Which batches are blocked from close?')
    await page.keyboard.press('Enter')
    await expect(page.getByText(/Ask Zord|cited|Answered/i).first()).toBeVisible({ timeout: 15_000 })
  })
})
