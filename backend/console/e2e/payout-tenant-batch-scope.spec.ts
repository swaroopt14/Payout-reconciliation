import { test, expect, type BrowserContext, type Page } from '@playwright/test'

const BASE_URL = process.env.PLAYWRIGHT_BASE_URL || 'http://localhost:3000'

/** Session tenant returned by mocked /api/auth/me (BFF injects this server-side). */
const SESSION_TENANT = 'e2e-session-tenant-111'
/** Batch scope for intent journal + evidence; settlement uses client_batch_id. */
const BATCH_ID = 'e2e-batch-222'

type ProdCapture = { pathname: string; searchParams: URLSearchParams }

function captureProdGet(url: string): ProdCapture | null {
  try {
    const u = new URL(url)
    if (!u.pathname.startsWith('/api/prod/')) return null
    return { pathname: u.pathname, searchParams: u.searchParams }
  } catch {
    return null
  }
}

function assertNoClientTenantId(params: URLSearchParams, label: string) {
  expect(params.get('tenant_id'), `${label}: client must not send tenant_id`).toBeNull()
}

async function installPayoutSessionCookies(context: BrowserContext) {
  await context.addCookies([
    { name: 'zord_access_token', value: 'e2e-playwright-access', url: BASE_URL },
    { name: 'zord_role', value: 'CUSTOMER_USER', url: BASE_URL },
  ])
}

function installAuthAndProdMocks(page: Page) {
  return Promise.all([
    page.route('**/api/auth/me', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          session: { tenant_id: SESSION_TENANT },
          user: { tenant_id: SESSION_TENANT },
        }),
      })
    }),
    page.route('**/api/sandbox/workspace-api-keys', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ tenant_id: SESSION_TENANT }),
      })
    }),
    page.route('**/api/prod/**', async (route) => {
      if (route.request().method() !== 'GET') {
        await route.continue()
        return
      }
      const url = new URL(route.request().url())
      const path = url.pathname

      let body: unknown = {}
      if (path.endsWith('/intents/batch-ids')) {
        body = { items: [{ batch_id: BATCH_ID, intent_count: 0 }] }
      } else if (path.endsWith('/intelligence/batches')) {
        body = { tenant_id: SESSION_TENANT, batches: [{ batch_id: BATCH_ID, finality_status: 'OPEN' }] }
      } else if (path.includes('/intelligence/batches/')) {
        body = { batch_id: BATCH_ID, tenant_id: SESSION_TENANT, data_available: false }
      } else if (path.endsWith('/intents/payment-intents') || path.endsWith('/intents/dlq-items')) {
        body = { items: [] }
      } else if (path.endsWith('/evidence/packs')) {
        body = { packs: [] }
      } else if (path.endsWith('/settlement/observations/batches')) {
        body = { items: [], client_batch_ids: [BATCH_ID] }
      } else if (path.includes('/intelligence/')) {
        body = { data_available: false, tenant_id: SESSION_TENANT }
      } else if (path.endsWith('/ingest-status')) {
        body = { status: 'unknown', sources: [] }
      }

      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(body),
      })
    }),
  ])
}

function trackProdGets(page: Page): { captures: ProdCapture[]; stop: () => void } {
  const captures: ProdCapture[] = []
  const onRequest = (req: { method: () => string; url: () => string }) => {
    if (req.method() !== 'GET') return
    const cap = captureProdGet(req.url())
    if (cap) captures.push(cap)
  }
  page.on('request', onRequest)
  return {
    captures,
    stop: () => page.off('request', onRequest),
  }
}

async function waitForProdGet(page: Page, pathPart: string, timeout = 20_000) {
  return page.waitForResponse(
    (res) => res.request().method() === 'GET' && res.url().includes(pathPart),
    { timeout },
  )
}

function pathsMatching(captures: ProdCapture[], suffix: string): ProdCapture[] {
  return captures.filter((c) => c.pathname === suffix || c.pathname.endsWith(suffix))
}

test.describe('batch-scoped surfaces (session tenant via BFF + batch in query)', () => {
  test.beforeEach(async ({ page, context }) => {
    await installPayoutSessionCookies(context)
    await installAuthAndProdMocks(page)
  })

  test('intent journal requests batch_id and never client tenant_id', async ({ page }) => {
    const { captures: gets, stop } = trackProdGets(page)
    const paymentWait = waitForProdGet(page, '/api/prod/intents/payment-intents')
    await page.goto(`/transactions?demo=sandbox&batch_id=${encodeURIComponent(BATCH_ID)}`)
    await paymentWait
    await page.waitForTimeout(1500)
    stop()

    for (const cap of gets) {
      assertNoClientTenantId(cap.searchParams, cap.pathname)
    }

    const paymentIntents = pathsMatching(gets, '/api/prod/intents/payment-intents')
    expect(paymentIntents.length, 'payment-intents should be fetched').toBeGreaterThan(0)
    expect(paymentIntents.some((c) => c.searchParams.get('batch_id') === BATCH_ID)).toBe(true)

    const dlqItems = pathsMatching(gets, '/api/prod/intents/dlq-items')
    expect(dlqItems.some((c) => c.searchParams.get('batch_id') === BATCH_ID)).toBe(true)

    const batchIds = pathsMatching(gets, '/api/prod/intents/batch-ids')
    expect(batchIds.length, 'sidebar batch list is tenant-scoped (no batch_id param)').toBeGreaterThan(0)
    for (const cap of batchIds) {
      expect(cap.searchParams.get('batch_id')).toBeNull()
    }
  })

  test('settlement journal requests client_batch_id when deep-linked', async ({ page }) => {
    const { captures: gets, stop } = trackProdGets(page)
    const obsWait = page.waitForResponse(
      (res) =>
        res.request().method() === 'GET' &&
        res.url().includes('/api/prod/settlement/observations/batches') &&
        res.url().includes(`client_batch_id=${encodeURIComponent(BATCH_ID)}`),
      { timeout: 20_000 },
    )
    await page.goto(
      `/settlement/journal?demo=sandbox&client_batch_id=${encodeURIComponent(BATCH_ID)}`,
    )
    await obsWait
    await page.waitForTimeout(1500)
    stop()

    for (const cap of gets) {
      assertNoClientTenantId(cap.searchParams, cap.pathname)
    }

    const obs = pathsMatching(gets, '/api/prod/settlement/observations/batches')
    expect(obs.length, 'settlement observations BFF should be called').toBeGreaterThan(0)
    expect(
      obs.some((c) => c.searchParams.get('client_batch_id') === BATCH_ID),
      'scoped fetch must include client_batch_id',
    ).toBe(true)
  })

  test('evidence (proof dock) requests client_batch_id on packs; batches list is tenant-wide', async ({
    page,
  }) => {
    const { captures: gets, stop } = trackProdGets(page)
    const packsWait = page.waitForResponse(
      (res) =>
        res.request().method() === 'GET' &&
        res.url().includes('/api/prod/evidence/packs') &&
        res.url().includes(`client_batch_id=${encodeURIComponent(BATCH_ID)}`),
      { timeout: 20_000 },
    )
    await page.goto(`/proof?demo=sandbox&batch_id=${encodeURIComponent(BATCH_ID)}`)
    await packsWait
    await page.waitForTimeout(1500)
    stop()

    for (const cap of gets) {
      assertNoClientTenantId(cap.searchParams, cap.pathname)
    }

    const packs = pathsMatching(gets, '/api/prod/evidence/packs')
    expect(packs.some((c) => c.searchParams.get('client_batch_id') === BATCH_ID)).toBe(true)

    const intelBatches = pathsMatching(gets, '/api/prod/intelligence/batches')
    expect(intelBatches.length).toBeGreaterThan(0)
    for (const cap of intelBatches) {
      expect(cap.searchParams.get('batch_id')).toBeNull()
    }

  })
})

test.describe.skip('batch-scoped intelligence KPIs (legacy docks removed)', () => {
  test.beforeEach(async ({ page, context }) => {
    await installPayoutSessionCookies(context)
    await installAuthAndProdMocks(page)
  })

  const INTELLIGENCE_BATCH_ROUTES = [
    '/api/prod/intelligence/leakage',
    '/api/prod/intelligence/ambiguity',
    '/api/prod/intelligence/defensibility',
    '/api/prod/intelligence/patterns',
    '/api/prod/intelligence/recommendations',
    '/api/prod/intelligence/rca',
  ] as const

  test('leakage and ambiguity send batch_id when batch is selected in URL', async ({ page }) => {
    const { captures: gets, stop } = trackProdGets(page)
    const leakageWait = waitForProdGet(page, '/api/prod/intelligence/leakage')
    await page.goto(`/payout-command-view/today?dock=leakage&batch_id=${encodeURIComponent(BATCH_ID)}`)
    await leakageWait
    await page.waitForTimeout(1500)
    stop()

    for (const cap of gets) {
      assertNoClientTenantId(cap.searchParams, cap.pathname)
    }

    const leakage = pathsMatching(gets, '/api/prod/intelligence/leakage')
    expect(leakage.some((c) => c.searchParams.get('batch_id') === BATCH_ID)).toBe(true)

    const { captures: ambiguityGets, stop: stopAmbiguity } = trackProdGets(page)
    const ambiguityWait = waitForProdGet(page, '/api/prod/intelligence/ambiguity')
    await page.goto(`/payout-command-view/today?dock=ambiguity&batch_id=${encodeURIComponent(BATCH_ID)}`)
    await ambiguityWait
    await page.waitForTimeout(1000)
    stopAmbiguity()

    const ambiguity = pathsMatching(ambiguityGets, '/api/prod/intelligence/ambiguity')
    expect(ambiguity.some((c) => c.searchParams.get('batch_id') === BATCH_ID)).toBe(true)
  })

  test('evidence defensibility and patterns send batch_id when batch is selected', async ({ page }) => {
    const { captures: gets, stop } = trackProdGets(page)
    const defWait = waitForProdGet(page, '/api/prod/intelligence/defensibility')
    await page.goto(`/proof?demo=sandbox&batch_id=${encodeURIComponent(BATCH_ID)}`)
    await defWait
    await page.waitForTimeout(1500)
    stop()

    for (const cap of gets) {
      assertNoClientTenantId(cap.searchParams, cap.pathname)
    }

    const defensibility = pathsMatching(gets, '/api/prod/intelligence/defensibility')
    expect(defensibility.some((c) => c.searchParams.get('batch_id') === BATCH_ID)).toBe(true)

    const patterns = pathsMatching(gets, '/api/prod/intelligence/patterns')
    if (patterns.length > 0) {
      expect(patterns.some((c) => c.searchParams.get('batch_id') === BATCH_ID)).toBe(true)
    }
  })

  test('workspace sends batch-scoped intelligence KPIs when batch_id is in URL', async ({ page }) => {
    const { captures: gets, stop } = trackProdGets(page)
    const leakageWait = waitForProdGet(page, '/api/prod/intelligence/leakage')
    await page.goto(`/payout-command-view/today?dock=workspace&batch_id=${encodeURIComponent(BATCH_ID)}`)
    await leakageWait
    await page.waitForTimeout(2000)
    stop()

    for (const cap of gets) {
      assertNoClientTenantId(cap.searchParams, cap.pathname)
    }

    for (const route of INTELLIGENCE_BATCH_ROUTES) {
      const matches = pathsMatching(gets, route)
      if (matches.length === 0) continue
      expect(
        matches.some((c) => c.searchParams.get('batch_id') === BATCH_ID),
        `${route} should include batch_id when batch is selected`,
      ).toBe(true)
    }
  })
})

test.describe('tenant-scoped surfaces (no batch_id on client prod GETs)', () => {
  test.beforeEach(async ({ page, context }) => {
    await installPayoutSessionCookies(context)
    await installAuthAndProdMocks(page)
  })

  test('exceptions page does not send client tenant_id', async ({ page }) => {
    const { captures: gets, stop } = trackProdGets(page)
    await page.goto('/exceptions?demo=sandbox')
    await page.waitForTimeout(2000)
    stop()
    for (const cap of gets) {
      assertNoClientTenantId(cap.searchParams, cap.pathname)
    }
  })

  test('overview uses tenant-wide prod routes only', async ({ page }) => {
    const { captures: gets, stop } = trackProdGets(page)
    await page.goto('/overview?demo=sandbox')
    await expect(page.getByText('Financial control overview')).toBeVisible({ timeout: 20_000 })
    stop()

    const batchScoped = gets.filter((c) => {
      const bid = c.searchParams.get('batch_id') || c.searchParams.get('client_batch_id')
      return Boolean(bid)
    })
    expect(
      batchScoped,
      `overview should not pass batch scope on prod GETs: ${batchScoped
        .map((c) => `${c.pathname}?${c.searchParams.toString()}`)
        .join(', ')}`,
    ).toHaveLength(0)

    for (const cap of gets) {
      assertNoClientTenantId(cap.searchParams, cap.pathname)
    }
  })
})

test.describe('BFF rejects client tenant_id override', () => {
  test('payment-intents returns 403 when query tenant_id disagrees with session', async ({ request }) => {
    const res = await request.get(
      `/api/prod/intents/payment-intents?batch_id=${BATCH_ID}&tenant_id=wrong-tenant-id`,
    )
    expect([401, 403]).toContain(res.status())
  })

  test('evidence packs returns 401 without session (tenant injected server-side only)', async ({ request }) => {
    const res = await request.get(`/api/prod/evidence/packs?batch_id=${BATCH_ID}`)
    expect(res.status()).toBe(401)
    const body = await res.json()
    expect(body.code || body.message).toBeTruthy()
  })
})
