import { NextRequest, NextResponse } from 'next/server'
import { applyAuthCookies } from '@/services/auth/server'
import {
  applyRefreshedSessionCookies,
  requireSessionTenantForProdProxy,
} from '@/services/auth/resolvePayoutTenant.server'

export const dynamic = 'force-dynamic'
export const runtime = 'nodejs'

function routerBase() {
  return (process.env.ZORD_ROUTER_URL?.trim() || 'http://localhost:8091').replace(/\/$/, '')
}

export async function POST(request: NextRequest) {
  const gate = await requireSessionTenantForProdProxy(request)
  if (!gate.ok) return gate.response
  const tenantId = gate.tenantId

  let body: Record<string, unknown> = {}
  try {
    body = (await request.json()) as Record<string, unknown>
  } catch {
    body = {}
  }
  body.tenant_id = tenantId
  if (!body.merchant_id) body.merchant_id = tenantId
  if (!body.direction) body.direction = 'OUTBOUND'
  if (!body.currency) body.currency = 'INR'

  const url = `${routerBase()}/v1/routing/route`
  try {
    const upstream = await fetch(url, {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        'x-tenant-id': tenantId,
      },
      body: JSON.stringify(body),
      cache: 'no-store',
    })
    const text = await upstream.text()
    const res = new NextResponse(text, {
      status: upstream.status,
      headers: {
        'content-type': upstream.headers.get('content-type') || 'application/json; charset=utf-8',
        'cache-control': 'no-store',
      },
    })
    if (gate.refreshedPayload) applyAuthCookies(res, gate.refreshedPayload)
    applyRefreshedSessionCookies(res, gate.refreshedPayload)
    return res
  } catch (error) {
    const res = NextResponse.json(
      {
        error: 'routing service unavailable',
        upstream: url,
        details: error instanceof Error ? error.message : 'unknown',
      },
      { status: 502 },
    )
    applyRefreshedSessionCookies(res, gate.refreshedPayload)
    return res
  }
}
