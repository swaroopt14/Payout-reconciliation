import { NextResponse } from 'next/server'
import type { NextRequest } from 'next/server'

/**
 * Seeded demo analytics gate for every /api/prod/zord/* route backed by
 * services/analytics/store.ts (a seeded random dataset — not live data).
 *
 * Server-only flag, default OFF. Only the exact values "1" or "true" enable it.
 * When off: 410 and the analytics module is never imported (dynamic import below).
 * When on: every response carries `X-Clearline-Data: demo`; JSON object bodies get
 * `demo: true` and `source: 'seeded_demo'` (or `data_source` if `source` is taken).
 */
export const DEMO_ANALYTICS_ENV = 'CLEARLINE_DEMO_ANALYTICS'
export const DEMO_DATA_HEADER = 'X-Clearline-Data'

export type AnalyticsModule = typeof import('@/services/analytics')

export function isDemoAnalyticsEnabled(env: Record<string, string | undefined> = process.env): boolean {
  const raw = env[DEMO_ANALYTICS_ENV]
  return raw === '1' || raw === 'true'
}

export function demoDatasetDisabledResponse(): NextResponse {
  return NextResponse.json(
    {
      error: 'demo_dataset_disabled',
      message: `Seeded demo analytics are disabled on live routes; set ${DEMO_ANALYTICS_ENV}=1 for demo only.`,
    },
    { status: 410, headers: { 'Cache-Control': 'no-store, max-age=0' } },
  )
}

/** JSON response with demo labelling in the body where the shape allows (plain objects only). */
export function demoJson(body: unknown, init?: ResponseInit): NextResponse {
  let labelled = body
  if (body && typeof body === 'object' && !Array.isArray(body)) {
    const obj = body as Record<string, unknown>
    labelled = 'source' in obj
      ? { ...obj, demo: true, data_source: 'seeded_demo' }
      : { ...obj, demo: true, source: 'seeded_demo' }
  }
  return NextResponse.json(labelled, init)
}

export function withNoStore(response: NextResponse): NextResponse {
  response.headers.set('Cache-Control', 'no-store, max-age=0')
  return response
}

/** Tenant + time range from the request, using the (already loaded) analytics module. */
export function resolveRequestContext(
  analytics: AnalyticsModule,
  request: NextRequest,
): { tenantId: string; timeRange: string; response?: NextResponse } {
  const tenant = analytics.resolveTenantId(request)
  const timeRange = analytics.getTimeRangeParam(request)
  if (tenant.error) {
    return { tenantId: tenant.tenantId, timeRange, response: demoJson({ error: tenant.error }, { status: 400 }) }
  }
  return { tenantId: tenant.tenantId, timeRange }
}

/**
 * Single entry point for all zord analytics routes. The store is loaded lazily and only
 * when the demo flag is on; every returned response is tagged `X-Clearline-Data: demo`.
 */
export async function withDemoAnalytics(
  handler: (analytics: AnalyticsModule) => NextResponse | Promise<NextResponse>,
): Promise<NextResponse> {
  if (!isDemoAnalyticsEnabled()) return demoDatasetDisabledResponse()
  let res: NextResponse
  try {
    const analytics: AnalyticsModule = await import('@/services/analytics')
    res = await handler(analytics)
  } catch (error) {
    res = demoJson(
      { error: 'demo_analytics_failed', detail: error instanceof Error ? error.message : 'unknown error' },
      { status: 500 },
    )
  }
  res.headers.set(DEMO_DATA_HEADER, 'demo')
  return res
}
