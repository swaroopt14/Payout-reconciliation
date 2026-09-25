// Run: npm run test:settlement-source-gate
//   (= node --import ./scripts/test-support/register-ts-loader.mjs --test scripts/test-settlement-source-gate.mjs)
// Proves the cash routes never serve smoke-simulator fixture data unless
// CLEARLINE_DEMO_SETTLEMENT_SIMULATOR is on AND no live ZORD_SETTLEMENT_URL is configured,
// and that simulator responses are always labelled demo.
import { test, beforeEach, afterEach } from 'node:test'
import assert from 'node:assert/strict'
import { readdirSync, readFileSync, statSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { NextRequest } from 'next/server.js'

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const TENANT = '22222222-2222-4222-8222-222222222222'
const LIVE = 'http://live-recon.test:8081'
const SIM = 'http://smoke-sim.test:8099'
const INTEL_LIVE = 'http://live-intel.test:8089'
const NOT_CONFIGURED = {
  error: 'settlement_source_not_configured',
  message: 'Settlement source not configured; refusing to serve simulator fixture data on a live route.',
}
const ENV_KEYS = [
  'ZORD_SETTLEMENT_URL', 'SMOKE_SIMULATOR_URL', 'CLEARLINE_DEMO_SETTLEMENT_SIMULATOR',
  'ZORD_INTELLIGENCE_URL', 'CLEARLINE_DEMO_ANALYTICS', 'ZORD_EDGE_URL',
]

// ── Every cash route, how to call it, and the upstream path it must hit ───────────────
const COOKIE = { cookie: 'zord_access_token=test-token' }
const ROUTES = [
  { name: 'finance/[...path] GET', file: 'app/api/prod/finance/[...path]/route.ts', upstream: '/v1/reconciliation/exceptions',
    call: (m) => m.GET(new NextRequest('http://localhost/api/prod/finance/exceptions', { headers: COOKIE }), { params: { path: ['exceptions'] } }) },
  { name: 'finance/[...path] POST', file: 'app/api/prod/finance/[...path]/route.ts', upstream: '/v1/reconciliation/rerun',
    call: (m) => m.POST(new NextRequest('http://localhost/api/prod/finance/rerun', { method: 'POST', headers: { ...COOKIE, 'content-type': 'application/json' }, body: '{}' }), { params: { path: ['rerun'] } }) },
  { name: 'settlements', file: 'app/api/prod/settlements/route.ts', upstream: '/v1/settlements',
    call: (m) => m.GET(new NextRequest('http://localhost/api/prod/settlements', { headers: COOKIE })) },
  { name: 'settlements/[...path]', file: 'app/api/prod/settlements/[...path]/route.ts', upstream: '/v1/settlements/batch_1',
    call: (m) => m.GET(new NextRequest('http://localhost/api/prod/settlements/batch_1', { headers: COOKIE }), { params: { path: ['batch_1'] } }) },
  { name: 'settlement/errors', file: 'app/api/prod/settlement/errors/route.ts', upstream: '/v1/settlement/errors',
    call: (m) => m.GET(new NextRequest('http://localhost/api/prod/settlement/errors', { headers: COOKIE })) },
  { name: 'settlement/observations/batches', file: 'app/api/prod/settlement/observations/batches/route.ts', upstream: '/v1/settlement/observations/batches',
    call: (m) => m.GET(new NextRequest('http://localhost/api/prod/settlement/observations/batches', { headers: COOKIE })) },
  { name: 'cash/instruments', file: 'app/api/prod/cash/instruments/route.ts', upstream: '/v1/cash/instruments',
    call: (m) => m.GET(new NextRequest('http://localhost/api/prod/cash/instruments', { headers: COOKIE })) },
  { name: 'ingest-status', file: 'app/api/prod/ingest-status/route.ts', upstream: '/v1/settlement/observations/batches',
    call: (m) => m.GET(new NextRequest('http://localhost/api/prod/ingest-status', { headers: COOKIE })) },
  { name: 'settlement/upload (non-prod, cash write)', file: 'app/api/settlement/upload/route.ts', upstream: '/v1/settlement/upload',
    call: (m) => {
      const fd = new FormData()
      fd.append('file', new Blob(['id,amount\n1,100\n'], { type: 'text/csv' }), 'settle.csv')
      return m.POST(new NextRequest('http://localhost/api/settlement/upload?psp=razorpay', { method: 'POST', headers: COOKIE, body: fd }))
    } },
]

// ── fetch stub: auth answered locally, every other call recorded ───────────────────────
let calls = []
let upstreamBody = () => ({ status: 200, body: JSON.stringify({ items: [{ id: 'x1' }], observations: [] }), type: 'application/json' })
const realFetch = globalThis.fetch

function installFetch() {
  calls = []
  globalThis.fetch = async (input, init) => {
    const url = typeof input === 'string' ? input : input.url
    if (url.includes('/v1/auth/me')) return Response.json({ session: { tenant_id: TENANT } })
    if (url.includes('/v1/auth/principal')) return Response.json({ tenant_id: TENANT })
    calls.push({ url, method: init?.method ?? 'GET' })
    const r = upstreamBody(url)
    return new Response(r.body, { status: r.status, headers: { 'content-type': r.type } })
  }
}

function setEnv(vars) {
  for (const k of ENV_KEYS) delete process.env[k]
  process.env.ZORD_INTELLIGENCE_URL = INTEL_LIVE
  for (const [k, v] of Object.entries(vars)) {
    if (v === undefined) delete process.env[k]
    else process.env[k] = v
  }
}

const mods = new Map()
async function load(file) {
  if (!mods.has(file)) mods.set(file, await import(pathToFileURL(path.join(ROOT, file)).href))
  return mods.get(file)
}

beforeEach(() => installFetch())
afterEach(() => { globalThis.fetch = realFetch; for (const k of ENV_KEYS) delete process.env[k] })

const simCalls = () => calls.filter((c) => c.url.startsWith(SIM))

// ── Static guard ────────────────────────────────────────────────────────────────────
function walk(dir) {
  const out = []
  for (const n of readdirSync(dir)) {
    const p = path.join(dir, n)
    if (statSync(p).isDirectory()) out.push(...walk(p))
    else if (/\.tsx?$/.test(n)) out.push(p)
  }
  return out
}

test('static: every cash route uses resolveSettlementSource; no /api/prod file reads SMOKE_SIMULATOR_URL directly', () => {
  for (const r of ROUTES) {
    const src = readFileSync(path.join(ROOT, r.file), 'utf8')
    assert.match(src, /resolveSettlementSource\(\)/, `${r.file} does not use the shared resolver`)
    assert.doesNotMatch(src, /SMOKE_SIMULATOR_URL|ZORD_SETTLEMENT_URL|localhost:80(81|99)/, `${r.file} still resolves its own upstream`)
  }
  const offenders = walk(path.join(ROOT, 'app', 'api', 'prod'))
    .filter((f) => /SMOKE_SIMULATOR_URL/.test(readFileSync(f, 'utf8')))
    .map((f) => path.relative(ROOT, f))
  assert.deepEqual(offenders, [], 'a /api/prod file reads SMOKE_SIMULATOR_URL directly')
  const cfg = readFileSync(path.join(ROOT, 'config', 'api.endpoints.ts'), 'utf8')
  assert.doesNotMatch(cfg, /SMOKE_SIMULATOR_URL/, 'config/api.endpoints.ts still falls back to the simulator')
})

// ── Case 1: live configured → live wins even with the flag on ────────────────────────
test('case 1 (live set): every route fetches ZORD_SETTLEMENT_URL, never the simulator, no demo label - even with flag on', async () => {
  for (const flag of [undefined, '0', '1', 'true']) {
    for (const r of ROUTES) {
      setEnv({ ZORD_SETTLEMENT_URL: LIVE, SMOKE_SIMULATOR_URL: SIM, CLEARLINE_DEMO_SETTLEMENT_SIMULATOR: flag })
      calls = []
      const res = await r.call(await load(r.file))
      const tag = `${r.name} [flag=${flag}]`
      assert.equal(simCalls().length, 0, `${tag} fetched the simulator: ${JSON.stringify(simCalls())}`)
      assert.ok(calls.some((c) => c.url.startsWith(LIVE + r.upstream)), `${tag} did not hit live ${r.upstream}: ${JSON.stringify(calls)}`)
      assert.equal(res.headers.get('x-clearline-data'), null, `${tag} live response labelled demo`)
      const body = await res.json()
      assert.notEqual(body.demo, true, `${tag} live body has demo:true`)
      assert.notEqual(body.source, 'smoke_simulator', `${tag} live body claims simulator`)
    }
  }
})

// ── Case 2: no live URL + flag on → simulator, labelled ──────────────────────────────
test('case 2 (unset + flag "1"/"true"): every route uses the simulator and is labelled demo', async () => {
  for (const flag of ['1', 'true']) {
    for (const r of ROUTES) {
      setEnv({ SMOKE_SIMULATOR_URL: SIM, CLEARLINE_DEMO_SETTLEMENT_SIMULATOR: flag })
      calls = []
      const res = await r.call(await load(r.file))
      const tag = `${r.name} [flag=${flag}]`
      assert.ok(calls.some((c) => c.url.startsWith(SIM + r.upstream)), `${tag} did not hit simulator ${r.upstream}`)
      assert.equal(res.headers.get('x-clearline-data'), 'demo', `${tag} missing X-Clearline-Data: demo`)
      const body = await res.json()
      assert.equal(body.demo, true, `${tag} body.demo`)
      assert.equal(body.source, 'smoke_simulator', `${tag} body.source`)
    }
  }
})

// ── Case 3: no live URL + flag off → 503, simulator never fetched ───────────────────
test('case 3 (unset + flag off/invalid): every route → 503 not_configured, no-store, zero upstream fetches', async () => {
  for (const flag of [undefined, '0', 'false', 'TRUE', 'yes', ' 1']) {
    for (const r of ROUTES) {
      setEnv({ SMOKE_SIMULATOR_URL: SIM, CLEARLINE_DEMO_SETTLEMENT_SIMULATOR: flag, CLEARLINE_DEMO_ANALYTICS: '1' })
      calls = []
      const res = await r.call(await load(r.file))
      const tag = `${r.name} [flag=${JSON.stringify(flag)}]`
      assert.equal(simCalls().length, 0, `${tag} fetched the simulator`)
      assert.equal(calls.length, 0, `${tag} made upstream calls: ${JSON.stringify(calls)}`)
      assert.equal(res.status, 503, `${tag} status ${res.status}`)
      assert.equal(res.headers.get('cache-control'), 'no-store', `${tag} cache-control`)
      assert.deepEqual(await res.json(), NOT_CONFIGURED, `${tag} body`)
    }
  }
})

test('live URL pointed at the simulator itself is treated as the simulator (503 when off, demo when on)', async () => {
  const r = ROUTES.find((x) => x.name === 'settlements')
  setEnv({ ZORD_SETTLEMENT_URL: SIM + '/', SMOKE_SIMULATOR_URL: SIM })
  calls = []
  let res = await r.call(await load(r.file))
  assert.equal(res.status, 503)
  assert.equal(calls.length, 0)
  setEnv({ ZORD_SETTLEMENT_URL: SIM, SMOKE_SIMULATOR_URL: SIM, CLEARLINE_DEMO_SETTLEMENT_SIMULATOR: '1' })
  res = await r.call(await load(r.file))
  assert.equal(res.headers.get('x-clearline-data'), 'demo')
})

test('demo labelling edge cases: source clash → data_source, arrays/non-JSON header-only, errors labelled', async () => {
  const r = ROUTES.find((x) => x.name === 'settlements')
  const m = await load(r.file)
  setEnv({ SMOKE_SIMULATOR_URL: SIM, CLEARLINE_DEMO_SETTLEMENT_SIMULATOR: '1' })
  try {
    upstreamBody = () => ({ status: 200, body: JSON.stringify({ source: 'razorpay', items: [] }), type: 'application/json' })
    let res = await r.call(m)
    let body = await res.json()
    assert.equal(body.source, 'razorpay', 'existing source must be preserved')
    assert.equal(body.data_source, 'smoke_simulator')
    assert.equal(body.demo, true)

    upstreamBody = () => ({ status: 200, body: '[{"id":1}]', type: 'application/json' })
    res = await r.call(m)
    assert.equal(res.headers.get('x-clearline-data'), 'demo')
    assert.equal(await res.text(), '[{"id":1}]', 'array body must pass through verbatim')

    upstreamBody = () => ({ status: 200, body: 'id,amount\n1,100\n', type: 'text/csv' })
    res = await r.call(m)
    assert.equal(res.headers.get('x-clearline-data'), 'demo')
    assert.equal(res.headers.get('content-type'), 'text/csv')
    assert.equal(await res.text(), 'id,amount\n1,100\n', 'non-JSON body must pass through verbatim')

    upstreamBody = () => ({ status: 500, body: JSON.stringify({ error: 'boom' }), type: 'application/json' })
    res = await r.call(m)
    assert.equal(res.status, 500)
    assert.equal(res.headers.get('x-clearline-data'), 'demo', 'upstream error must be labelled')
    assert.equal((await res.json()).demo, true)

    // 401 from the auth gate and 502 when the simulator is unreachable are labelled too.
    globalThis.fetch = async (input) => {
      const url = typeof input === 'string' ? input : input.url
      if (url.includes('/v1/auth/')) return new Response('{}', { status: 401 })
      throw new Error('ECONNREFUSED')
    }
    res = await r.call(m)
    assert.equal(res.status, 401)
    assert.equal(res.headers.get('x-clearline-data'), 'demo', 'auth error must be labelled')
    globalThis.fetch = async (input) => {
      const url = typeof input === 'string' ? input : input.url
      if (url.includes('/v1/auth/me')) return Response.json({ session: { tenant_id: TENANT } })
      throw new Error('ECONNREFUSED')
    }
    res = await r.call(m)
    assert.equal(res.status, 502)
    assert.equal(res.headers.get('x-clearline-data'), 'demo', '502 must be labelled')
  } finally {
    upstreamBody = () => ({ status: 200, body: JSON.stringify({ items: [{ id: 'x1' }], observations: [] }), type: 'application/json' })
  }
})

// ── Shared intelligence resolver (config/api.endpoints.ts → forwardIntelligence) ─────
test('intelligence resolver: simulator fallback only with the flag; labelled demo when used', async () => {
  const m = await load('app/api/prod/intelligence/leakage/route.ts')
  const req = () => new NextRequest('http://localhost/api/prod/intelligence/leakage', { headers: COOKIE })

  for (const flag of [undefined, '0', 'TRUE']) {
    setEnv({ SMOKE_SIMULATOR_URL: SIM, CLEARLINE_DEMO_SETTLEMENT_SIMULATOR: flag, CLEARLINE_DEMO_ANALYTICS: '1' })
    delete process.env.ZORD_INTELLIGENCE_URL
    calls = []
    const res = await m.GET(req())
    assert.equal(simCalls().length, 0, `intelligence fetched simulator with flag=${flag}`)
    assert.ok(calls.some((c) => c.url.startsWith('http://localhost:8089/')), 'expected live default :8089')
    assert.equal(res.headers.get('x-clearline-data'), null)
  }

  setEnv({ SMOKE_SIMULATOR_URL: SIM, CLEARLINE_DEMO_SETTLEMENT_SIMULATOR: '1' })
  process.env.ZORD_INTELLIGENCE_URL = INTEL_LIVE
  calls = []
  let res = await m.GET(req())
  assert.equal(simCalls().length, 0, 'live intelligence URL must win over the simulator')
  assert.equal(res.headers.get('x-clearline-data'), null)

  delete process.env.ZORD_INTELLIGENCE_URL
  calls = []
  res = await m.GET(req())
  assert.ok(simCalls().length > 0, 'flag on + no live intelligence URL → simulator')
  assert.equal(res.headers.get('x-clearline-data'), 'demo')
  const body = await res.json()
  assert.equal(body.demo, true)
})
