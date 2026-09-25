// Run: npm run test:zord-demo-gate
//   (= node --import ./scripts/test-support/register-ts-loader.mjs --test scripts/test-zord-demo-gate.mjs)
// Proves the /api/prod/zord/* seeded-analytics routes are gated by CLEARLINE_DEMO_ANALYTICS.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readdirSync, readFileSync, statSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { NextRequest } from 'next/server.js'

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const ZORD_DIR = path.join(ROOT, 'app', 'api', 'prod', 'zord')
const TENANT = '11111111-1111-4111-8111-111111111111'
const SEED_MARKERS = /seller_(alpha|delta|prime|metro|north)|HDFC0000123|GATEWAY_TIMEOUT|Cashfree/

function routeFiles(dir) {
  const out = []
  for (const name of readdirSync(dir)) {
    const p = path.join(dir, name)
    if (statSync(p).isDirectory()) out.push(...routeFiles(p))
    else if (name === 'route.ts') out.push(p)
  }
  return out.sort()
}

const ROUTES = routeFiles(ZORD_DIR)

async function callRoute(file) {
  const mod = await import(pathToFileURL(file).href)
  const rel = path.relative(ZORD_DIR, path.dirname(file)).split(path.sep).join('/')
  const urlPath = `/api/prod/zord/${rel.replace('[id]', 'intent_demo_1')}`
  if (typeof mod.POST === 'function') {
    const req = new NextRequest(`http://localhost${urlPath}`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({
        tenant_id: TENANT, intent_id: 'intent_demo_1', event_type: 'INTENT_CREATED',
        event_version: 1, source_topic: 'z.intent.created', payload: {},
      }),
    })
    return { rel, res: await mod.POST(req) }
  }
  const req = new NextRequest(`http://localhost${urlPath}?tenant_id=${TENANT}&q=seller&time_range=24h`)
  return { rel, res: await mod.GET(req, { params: { id: 'intent_demo_1' } }) }
}

test('every zord route goes through the shared demo gate (no static analytics import)', () => {
  assert.ok(ROUTES.length >= 10, `expected ≥10 zord routes, found ${ROUTES.length}`)
  for (const file of [...ROUTES, path.join(ZORD_DIR, 'helpers.ts')]) {
    const src = readFileSync(file, 'utf8')
    assert.doesNotMatch(src, /^import[^\n]*['"]@\/services\/analytics/m, `${file} statically imports services/analytics`)
    if (file.endsWith('route.ts')) assert.match(src, /withDemoAnalytics\(/, `${file} does not use withDemoAnalytics`)
  }
})

test('flag OFF (unset, "0", "yes", "TRUE"): every route → 410, no seeded data, store never loaded', async () => {
  for (const value of [undefined, '0', 'yes', 'TRUE', ' 1']) {
    if (value === undefined) delete process.env.CLEARLINE_DEMO_ANALYTICS
    else process.env.CLEARLINE_DEMO_ANALYTICS = value
    for (const file of ROUTES) {
      const { rel, res } = await callRoute(file)
      const text = await res.text()
      assert.equal(res.status, 410, `${rel} [flag=${value}] status ${res.status}`)
      const body = JSON.parse(text)
      assert.equal(body.error, 'demo_dataset_disabled', `${rel} error code`)
      assert.match(body.message, /CLEARLINE_DEMO_ANALYTICS=1/)
      assert.doesNotMatch(text, SEED_MARKERS, `${rel} leaked seeded values`)
      assert.equal(res.headers.get('x-clearline-data'), null, `${rel} must not claim demo data when off`)
    }
  }
  assert.equal(globalThis.__clearlineAnalyticsStoreLoads, undefined, 'analytics store was loaded while flag off')
})

test('flag ON ("1" and "true"): responses are labelled demo (header + body)', async () => {
  for (const value of ['1', 'true']) {
    process.env.CLEARLINE_DEMO_ANALYTICS = value
    for (const file of ROUTES) {
      const { rel, res } = await callRoute(file)
      assert.notEqual(res.status, 410, `${rel} [flag=${value}] still 410`)
      assert.equal(res.headers.get('x-clearline-data'), 'demo', `${rel} missing X-Clearline-Data: demo`)
      const body = await res.json()
      if (body && typeof body === 'object' && !Array.isArray(body)) {
        assert.equal(body.demo, true, `${rel} body.demo`)
        assert.ok(body.source === 'seeded_demo' || body.data_source === 'seeded_demo', `${rel} body source label`)
      }
    }
  }
  assert.ok(globalThis.__clearlineAnalyticsStoreLoads >= 1, 'store should load only once the flag is on')
  delete process.env.CLEARLINE_DEMO_ANALYTICS
})
