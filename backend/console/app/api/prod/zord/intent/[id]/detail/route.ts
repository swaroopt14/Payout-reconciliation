import type { NextRequest } from 'next/server'
import { demoJson, resolveRequestContext, withDemoAnalytics, withNoStore } from '../../../demoGate'

export const dynamic = 'force-dynamic'

export async function GET(request: NextRequest, { params }: { params: { id: string } }) {
  return withDemoAnalytics((analytics) => {
    const ctx = resolveRequestContext(analytics, request)
    if (ctx.response) return ctx.response

    try {
      const detail = analytics.getIntentDetail(ctx.tenantId, params.id)
      if (!detail) {
        return demoJson({ error: 'Intent not found' }, { status: 404 })
      }
      return withNoStore(demoJson(detail))
    } catch (error) {
      return demoJson(
        { error: 'Failed to load intent detail', detail: error instanceof Error ? error.message : 'unknown error' },
        { status: 500 },
      )
    }
  })
}
