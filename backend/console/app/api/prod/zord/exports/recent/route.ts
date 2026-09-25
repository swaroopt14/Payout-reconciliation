import type { NextRequest } from 'next/server'
import { demoJson, resolveRequestContext, withDemoAnalytics, withNoStore } from '../../demoGate'

export const dynamic = 'force-dynamic'

export async function GET(request: NextRequest) {
  return withDemoAnalytics((analytics) => {
    const ctx = resolveRequestContext(analytics, request)
    if (ctx.response) return ctx.response

    const queue = analytics.getExportQueue(ctx.tenantId)
    return withNoStore(
      demoJson({
        items: queue,
        queued_count: queue.filter((item) => item.status === 'QUEUED' || item.status === 'PROCESSING').length,
      }),
    )
  })
}
