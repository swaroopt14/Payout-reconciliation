import type { NextRequest } from 'next/server'
import { demoJson, resolveRequestContext, withDemoAnalytics, withNoStore } from '../../demoGate'

export const dynamic = 'force-dynamic'

export async function GET(request: NextRequest) {
  return withDemoAnalytics((analytics) => {
    const ctx = resolveRequestContext(analytics, request)
    if (ctx.response) return ctx.response
    try {
      const data = analytics.getOverviewMetrics(ctx.tenantId, ctx.timeRange)
      return withNoStore(demoJson(data))
    } catch (error) {
      return demoJson(
        { error: 'Failed to load overview metrics', detail: error instanceof Error ? error.message : 'unknown error' },
        { status: 500 },
      )
    }
  })
}
