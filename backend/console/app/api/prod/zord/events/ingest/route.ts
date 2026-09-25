import type { NextRequest } from 'next/server'
import { demoJson, withDemoAnalytics } from '../../demoGate'

export const dynamic = 'force-dynamic'

export async function POST(request: NextRequest) {
  return withDemoAnalytics(async (analytics) => {
    try {
      const body = await request.json()

      const required = ['tenant_id', 'intent_id', 'event_type', 'event_version', 'source_topic', 'payload']
      const missing = required.filter((field) => body[field] === undefined || body[field] === null)

      if (missing.length > 0) {
        return demoJson({ error: `Missing required fields: ${missing.join(', ')}` }, { status: 400 })
      }

      const result = analytics.ingestEvent({
        tenant_id: String(body.tenant_id),
        intent_id: String(body.intent_id),
        event_type: String(body.event_type),
        event_version: Number(body.event_version),
        occurred_at: body.occurred_at ? String(body.occurred_at) : new Date().toISOString(),
        source_topic: body.source_topic,
        payload: body.payload,
        correlation_id: body.correlation_id ? String(body.correlation_id) : undefined,
      })

      if (!result.accepted && result.reason === 'DUPLICATE') {
        return demoJson({ accepted: false, reason: 'DUPLICATE' }, { status: 202 })
      }
      if (!result.accepted) {
        return demoJson({ accepted: false, reason: result.reason || 'FAILED' }, { status: 422 })
      }
      return demoJson({ accepted: true })
    } catch (error) {
      return demoJson(
        { error: 'Unable to ingest event', detail: error instanceof Error ? error.message : 'unknown error' },
        { status: 500 },
      )
    }
  })
}
