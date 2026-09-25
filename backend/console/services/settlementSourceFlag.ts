/**
 * Pure env helpers for the settlement-simulator demo gate. No `next/*` imports so that
 * `config/api.endpoints.ts` (shared resolver) can use them too.
 *
 * WHY A DEDICATED FLAG (not CLEARLINE_DEMO_ANALYTICS):
 * The smoke simulator (payout-smoke-simulator, :8099) returns fixture settlements, recon
 * exceptions, cash instruments and payout KPIs. Those are *cash* numbers. Turning on the
 * seeded demo analytics pages (CLEARLINE_DEMO_ANALYTICS) must never, as a side effect, let
 * a live cash route quietly serve fixture money. So fixture cash has its own explicit,
 * server-only, default-off switch: CLEARLINE_DEMO_SETTLEMENT_SIMULATOR ("1" or "true" only).
 * Never expose this as NEXT_PUBLIC_*.
 */

export const SETTLEMENT_SIMULATOR_FLAG = 'CLEARLINE_DEMO_SETTLEMENT_SIMULATOR'

/** payout-smoke-simulator listens on :8099 by default (backend/payout-smoke-simulator/src/server.js). */
export const SMOKE_SIMULATOR_DEFAULT_BASE_URL = 'http://localhost:8099'

type Env = Record<string, string | undefined>

/** Exactly "1" or "true" (case-sensitive, no whitespace) turns it on; anything else is off. */
export function isSettlementSimulatorDemoEnabled(env: Env = process.env): boolean {
  const raw = env[SETTLEMENT_SIMULATOR_FLAG]
  return raw === '1' || raw === 'true'
}

/** Trim + drop trailing slashes; empty → ''. */
export function normalizeBaseUrl(value: string | undefined | null): string {
  return (value ?? '').trim().replace(/\/+$/, '')
}

/**
 * zord-intelligence base URL (shared resolver used by every /api/prod/intelligence/* route,
 * ingest-status and intent-journal). The smoke-simulator fallback is only allowed when the
 * settlement-simulator demo flag is on; otherwise the live default (:8089) is used.
 */
export function resolveIntelligenceSource(env: Env = process.env): {
  kind: 'live' | 'simulator'
  baseUrl: string
} {
  const live = normalizeBaseUrl(env.ZORD_INTELLIGENCE_URL)
  const sim = normalizeBaseUrl(env.SMOKE_SIMULATOR_URL)
  const demo = isSettlementSimulatorDemoEnabled(env)
  if (live && live !== sim) return { kind: 'live', baseUrl: live }
  if (demo && (live || sim)) return { kind: 'simulator', baseUrl: live || sim }
  // Flag off: never fall back to the simulator (even if ZORD_INTELLIGENCE_URL was pointed at it).
  return { kind: 'live', baseUrl: 'http://localhost:8089' }
}
