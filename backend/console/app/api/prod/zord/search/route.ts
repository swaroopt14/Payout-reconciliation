import type { NextRequest } from 'next/server'
import { demoJson, resolveRequestContext, withDemoAnalytics, withNoStore } from '../demoGate'

export const dynamic = 'force-dynamic'

export async function GET(request: NextRequest) {
  return withDemoAnalytics((analytics) => {
    const ctx = resolveRequestContext(analytics, request)
    if (ctx.response) return ctx.response

    const query = request.nextUrl.searchParams.get('q') || ''
    const limit = Math.min(50, Math.max(1, Number(request.nextUrl.searchParams.get('limit') || 20)))

    if (!query.trim()) {
      return withNoStore(demoJson({ items: [] }))
    }

    try {
      const started = Date.now()
      const items = analytics.searchDataset(ctx.tenantId, query, limit)
      const durationMs = Date.now() - started
      return withNoStore(
        demoJson({
          items,
          meta: { query, limit, response_ms: durationMs, target_ms: 200 },
        }),
      )
    } catch (error) {
      return demoJson(
        { error: 'Search failed', detail: error instanceof Error ? error.message : 'unknown error' },
        { status: 500 },
      )
    }
  })
}
