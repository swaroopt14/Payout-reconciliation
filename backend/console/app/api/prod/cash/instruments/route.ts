import { NextRequest, NextResponse } from 'next/server'
import { applyAuthCookies } from '@/services/auth/server'
import {
  applyRefreshedSessionCookies,
  requireSessionTenantForProdProxy,
} from '@/services/auth/resolvePayoutTenant.server'

export const dynamic = 'force-dynamic'
export const runtime = 'nodejs'

function reconBase() {
  const explicit =
    process.env.ZORD_SETTLEMENT_URL?.trim() || process.env.SMOKE_SIMULATOR_URL?.trim()
  if (explicit) return explicit.replace(/\/$/, '')
  return 'http://localhost:8081'
}

export async function GET(request: NextRequest) {
  const gate = await requireSessionTenantForProdProxy(request)
  if (!gate.ok) return gate.response
  const url = `${reconBase()}/v1/cash/instruments`
  try {
    const upstream = await fetch(url, {
      method: 'GET',
      headers: { 'x-tenant-id': gate.tenantId },
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
        error: 'recon service unavailable',
        upstream: url,
        details: error instanceof Error ? error.message : 'unknown',
      },
      { status: 502 },
    )
    applyRefreshedSessionCookies(res, gate.refreshedPayload)
    return res
  }
}
