import type { NextRequest } from 'next/server'
import { demoJson, resolveRequestContext, withDemoAnalytics, withNoStore } from '../demoGate'

export const dynamic = 'force-dynamic'

export async function GET(request: NextRequest) {
  return withDemoAnalytics((analytics) => {
    const ctx = resolveRequestContext(analytics, request)
    if (ctx.response) return ctx.response

    const alerts = analytics.getAlerts(ctx.tenantId)
    return withNoStore(
      demoJson({
        items: alerts,
        active_count: alerts.filter((item) => item.status === 'ACTIVE').length,
      }),
    )
  })
}
