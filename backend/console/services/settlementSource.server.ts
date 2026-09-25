import { NextResponse } from 'next/server'
import {
  isSettlementSimulatorDemoEnabled,
  normalizeBaseUrl,
  SMOKE_SIMULATOR_DEFAULT_BASE_URL,
} from './settlementSourceFlag'

/**
 * ONE resolver for every console route that reads or writes settlement / recon / cash data
 * from the outcome engine (zord-recon, :8081) and historically fell back to SMOKE_SIMULATOR_URL.
 *
 * Resolution rule:
 *  1. ZORD_SETTLEMENT_URL set (and not pointed at the simulator itself) → LIVE, always.
 *     The demo flag is ignored; the simulator is never called.
 *  2. Unset (or equal to SMOKE_SIMULATOR_URL) and CLEARLINE_DEMO_SETTLEMENT_SIMULATOR is
 *     exactly "1"/"true" → SIMULATOR (SMOKE_SIMULATOR_URL, default :8099), and every response
 *     is labelled demo (X-Clearline-Data: demo + demo/source fields on JSON object bodies).
 *  3. Otherwise → 503 settlement_source_not_configured (no-store). No upstream call at all.
 *
 * There is deliberately NO implicit localhost default any more (the old per-route code fell
 * back to :8081 or even :8099). A live route must be configured explicitly.
 * See services/settlementSourceFlag.ts for why this flag is separate from CLEARLINE_DEMO_ANALYTICS.
 */

export const DEMO_DATA_HEADER = 'X-Clearline-Data'
export const SIMULATOR_SOURCE = 'smoke_simulator'

export const SETTLEMENT_SOURCE_NOT_CONFIGURED_BODY = {
  error: 'settlement_source_not_configured',
  message: 'Settlement source not configured; refusing to serve simulator fixture data on a live route.',
} as const

export type SettlementSource = { kind: 'live' | 'simulator'; baseUrl: string }

export type SettlementSourceResolution =
  | { ok: true; source: SettlementSource }
  | { ok: false; response: NextResponse }

type Env = Record<string, string | undefined>

export function settlementSourceNotConfiguredResponse(): NextResponse {
  return NextResponse.json(
    { ...SETTLEMENT_SOURCE_NOT_CONFIGURED_BODY },
    { status: 503, headers: { 'cache-control': 'no-store' } },
  )
}

export function resolveSettlementSource(env: Env = process.env): SettlementSourceResolution {
  const live = normalizeBaseUrl(env.ZORD_SETTLEMENT_URL)
  const sim = normalizeBaseUrl(env.SMOKE_SIMULATOR_URL)
  if (live && live !== sim) return { ok: true, source: { kind: 'live', baseUrl: live } }
  if (isSettlementSimulatorDemoEnabled(env)) {
    return {
      ok: true,
      source: { kind: 'simulator', baseUrl: sim || live || SMOKE_SIMULATOR_DEFAULT_BASE_URL },
    }
  }
  return { ok: false, response: settlementSourceNotConfiguredResponse() }
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

/** Adds demo:true + source (or data_source when `source` is taken) to plain JSON objects only. */
export function withDemoFields(body: unknown): unknown {
  if (!isPlainObject(body)) return body
  const key = Object.prototype.hasOwnProperty.call(body, 'source') ? 'data_source' : 'source'
  return { ...body, demo: true, [key]: SIMULATOR_SOURCE }
}

/** Header-only label; no-op for live. Use on every early return (auth/validation errors too). */
export function labelSettlementResponse<T extends Response>(res: T, source: SettlementSource): T {
  if (source.kind === 'simulator') res.headers.set(DEMO_DATA_HEADER, 'demo')
  return res
}

/** NextResponse.json with demo labelling applied when the source is the simulator. */
export function settlementJson(
  body: unknown,
  source: SettlementSource,
  init?: ResponseInit,
): NextResponse {
  const payload = source.kind === 'simulator' ? withDemoFields(body) : body
  return labelSettlementResponse(NextResponse.json(payload, init), source)
}

/**
 * Relays an upstream response.
 *  - live: unchanged behaviour (buffered text, upstream status + content-type).
 *  - simulator + JSON content-type: object bodies get demo fields; arrays/scalars/unparseable
 *    bodies are passed through verbatim (header only).
 *  - simulator + non-JSON: body stream passed through untouched (header only).
 */
export async function relaySettlementUpstream(
  upstream: Response,
  source: SettlementSource,
  cacheControl = 'no-store',
): Promise<NextResponse> {
  const contentType = upstream.headers.get('content-type') || 'application/json; charset=utf-8'
  const headers = { 'content-type': contentType, 'cache-control': cacheControl }

  if (source.kind === 'live') {
    const text = await upstream.text()
    return new NextResponse(text, { status: upstream.status, headers })
  }

  if (!/json/i.test(contentType)) {
    return labelSettlementResponse(
      new NextResponse(upstream.body, { status: upstream.status, headers }),
      source,
    )
  }

  const text = await upstream.text()
  let out = text
  try {
    const parsed: unknown = JSON.parse(text)
    if (isPlainObject(parsed)) out = JSON.stringify(withDemoFields(parsed))
  } catch {
    /* not valid JSON: pass through verbatim */
  }
  return labelSettlementResponse(new NextResponse(out, { status: upstream.status, headers }), source)
}

/**
 * Post-hoc demo labelling for an already-built response (used by forwardIntelligence, whose
 * many graceful-empty branches all return early). Header always; JSON object bodies get the
 * demo fields; arrays / non-JSON / streams are left untouched. Status + headers (incl. cookies)
 * are preserved. No-op for live.
 */
export async function labelBuiltResponse(
  res: NextResponse,
  source: SettlementSource,
): Promise<NextResponse> {
  if (source.kind !== 'simulator') return res
  const contentType = res.headers.get('content-type') || ''
  if (!/json/i.test(contentType) || res.body === null) return labelSettlementResponse(res, source)
  const text = await res.text()
  let out = text
  try {
    const parsed: unknown = JSON.parse(text)
    if (isPlainObject(parsed)) out = JSON.stringify(withDemoFields(parsed))
  } catch {
    /* verbatim */
  }
  const relabelled = new NextResponse(out, { status: res.status, headers: res.headers })
  return labelSettlementResponse(relabelled, source)
}
