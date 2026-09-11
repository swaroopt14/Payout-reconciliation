import { NextRequest, NextResponse } from 'next/server'
import { applyAuthCookies } from '@/services/auth/server'
import {
  applyRefreshedSessionCookies,
  requireSessionTenantForProdProxy,
} from '@/services/auth/resolvePayoutTenant.server'

export const dynamic = 'force-dynamic'
export const runtime = 'nodejs'

const ALLOWED = new Set(['GET', 'POST'])

function reconBase() {
  const explicit =
    process.env.ZORD_SETTLEMENT_URL?.trim() || process.env.SMOKE_SIMULATOR_URL?.trim()
  if (explicit) return explicit.replace(/\/$/, '')
  return 'http://localhost:8081'
}

function connectorId() {
  return (
    process.env.ZORD_CONNECTOR_ID?.trim() ||
    process.env.NEXT_PUBLIC_ZORD_CONNECTOR_ID?.trim() ||
    'conn_smoke_razorpay'
  )
}

function safePath(segments: string[]): string | null {
  if (segments.length === 0) return null
  if (segments.some((s) => !s || s.includes('..') || s.includes('/') || s.includes('\\'))) {
    return null
  }
  return segments.join('/')
}

async function proxy(request: NextRequest, segments: string[]): Promise<NextResponse> {
  const method = request.method.toUpperCase()
  if (!ALLOWED.has(method)) {
    return NextResponse.json({ error: 'method_not_allowed' }, { status: 405 })
  }

  const rest = safePath(segments)
  if (!rest) {
    return NextResponse.json({ error: 'invalid_path' }, { status: 400 })
  }

  const gate = await requireSessionTenantForProdProxy(request)
  if (!gate.ok) return gate.response
  const tenantId = gate.tenantId

  const params = new URLSearchParams(request.nextUrl.searchParams)
  params.delete('tenant_id')
  params.set('tenant_id', tenantId)
  if (!params.get('connector_id')) params.set('connector_id', connectorId())

  const url = `${reconBase()}/v1/reconciliation/${rest}?${params.toString()}`
  const accessCookie = request.cookies.get('zord_access_token')?.value
  const authHeader = accessCookie?.trim() ? `Bearer ${accessCookie.trim()}` : ''

  let body: string | undefined
  if (method === 'POST') {
    try {
      body = await request.text()
    } catch {
      body = '{}'
    }
    try {
      const parsed = JSON.parse(body || '{}') as Record<string, unknown>
      delete parsed.tenant_id
      parsed.tenant_id = tenantId
      if (!parsed.connector_id) parsed.connector_id = connectorId()
      body = JSON.stringify(parsed)
    } catch {
      /* keep raw body */
    }
  }

  try {
    const upstream = await fetch(url, {
      method,
      headers: {
        'content-type': 'application/json',
        'x-tenant-id': tenantId,
        'tenant-id': tenantId,
        ...(authHeader ? { Authorization: authHeader } : {}),
      },
      body: method === 'POST' ? body || '{}' : undefined,
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
        error: 'finance recon upstream unavailable',
        upstream: url,
        details: error instanceof Error ? error.message : 'unknown',
      },
      { status: 502 },
    )
    applyRefreshedSessionCookies(res, gate.refreshedPayload)
    return res
  }
}

export async function GET(
  request: NextRequest,
  context: { params: { path?: string[] } },
) {
  return proxy(request, context.params.path ?? [])
}

export async function POST(
  request: NextRequest,
  context: { params: { path?: string[] } },
) {
  return proxy(request, context.params.path ?? [])
}
