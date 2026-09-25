import { NextRequest, NextResponse } from 'next/server'
import { BACKEND_SERVICES } from '@/config/api.endpoints'
import {
  applyRefreshedSessionCookies,
  requireSessionTenantForProdProxy,
} from '@/services/auth/resolvePayoutTenant.server'
import { resolveIntelligenceSource } from '@/services/settlementSourceFlag'
import { labelBuiltResponse } from '@/services/settlementSource.server'

const JSON_NO_STORE = { 'cache-control': 'no-store' } as const

function isKpiDashboardPath(path: string): boolean {
  return path.includes('/v1/intelligence/dashboard/')
}

function isOperationsOrExceptionsPath(path: string): boolean {
  return (
    path === BACKEND_SERVICES.INTELLIGENCE.ENDPOINTS.OPERATIONS_SUMMARY
    || path === BACKEND_SERVICES.INTELLIGENCE.ENDPOINTS.EXCEPTIONS_SUMMARY
  )
}

function isPatternDetailPath(path: string): boolean {
  return path === BACKEND_SERVICES.INTELLIGENCE.ENDPOINTS.PATTERN
    || path === BACKEND_SERVICES.INTELLIGENCE.ENDPOINTS.PATTERN_HISTORY
}

function isBatchesListPath(path: string): boolean {
  return path === BACKEND_SERVICES.INTELLIGENCE.ENDPOINTS.BATCHES
}

function emptyPatternResponse(reason: string) {
  return NextResponse.json(
    {
      data_available: false as const,
      reason,
      data: null,
    },
    { status: 200, headers: JSON_NO_STORE },
  )
}

function emptyPatternHistoryResponse(tenantId: string) {
  return NextResponse.json(
    {
      tenant_id: tenantId,
      intelligence_mode: 'offline',
      snapshot_type: 'PATTERN',
      snapshots: [] as const,
      count: 0,
    },
    { status: 200, headers: JSON_NO_STORE },
  )
}

function emptyKpiResponse(reason: string) {
  return NextResponse.json({ data_available: false as const, reason }, { status: 200, headers: JSON_NO_STORE })
}

function emptyBatchesResponse(tenantId: string, request: NextRequest) {
  const params = request.nextUrl.searchParams
  return NextResponse.json(
    {
      tenant_id: tenantId,
      intelligence_mode: 'offline',
      status_filter: params.get('status')?.trim() || '',
      batches: [] as const,
    },
    { status: 200, headers: JSON_NO_STORE },
  )
}

/**
 * Shared forwarder for `/api/prod/intelligence/*` Next routes → zord-intelligence (:8089).
 * Tenant is taken from the signed-in session; client-supplied tenant_id is ignored.
 * Upstream comes from resolveIntelligenceSource(): the smoke simulator is only used when
 * CLEARLINE_DEMO_SETTLEMENT_SIMULATOR is on, and then every response is labelled demo.
 */
export async function forwardIntelligence(request: NextRequest, path: string): Promise<NextResponse> {
  const intel = resolveIntelligenceSource()
  const res = await forwardIntelligenceTo(request, path, intel.baseUrl)
  return labelBuiltResponse(res, intel)
}

async function forwardIntelligenceTo(
  request: NextRequest,
  path: string,
  baseUrl: string,
): Promise<NextResponse> {
  const gate = await requireSessionTenantForProdProxy(request)
  if (!gate.ok) return gate.response
  const tenantId = gate.tenantId

  const params = new URLSearchParams(request.nextUrl.searchParams)
  params.delete('tenant_id')
  params.set('tenant_id', tenantId)

  const url = `${baseUrl}${path}?${params.toString()}`

  try {
    const accessCookie = request.cookies.get('zord_access_token')?.value
  const authHeader = accessCookie?.trim() ? `Bearer ${accessCookie.trim()}` : ''
  const upstream = await fetch(url, {
      method: 'GET',
      headers: {
        'content-type': 'application/json',
        'x-tenant-id': tenantId,
        ...(authHeader ? { Authorization: authHeader } : {}),
      },
      cache: 'no-store',
    })
    const text = await upstream.text()

    if (!upstream.ok) {
      if (isKpiDashboardPath(path) || isOperationsOrExceptionsPath(path)) {
        const reason =
          upstream.status === 404
            ? 'Intelligence KPIs not available (service or route missing).'
            : `Intelligence upstream returned HTTP ${upstream.status}.`
        const res = emptyKpiResponse(reason)
        applyRefreshedSessionCookies(res, gate.refreshedPayload)
        return res
      }
      if (isPatternDetailPath(path)) {
        const reason =
          upstream.status === 404
            ? 'Pattern intelligence not available (service or route missing).'
            : `Intelligence upstream returned HTTP ${upstream.status}.`
        const res = path === BACKEND_SERVICES.INTELLIGENCE.ENDPOINTS.PATTERN_HISTORY
          ? emptyPatternHistoryResponse(tenantId)
          : emptyPatternResponse(reason)
        applyRefreshedSessionCookies(res, gate.refreshedPayload)
        return res
      }
      if (isBatchesListPath(path)) {
        const res = emptyBatchesResponse(tenantId, request)
        applyRefreshedSessionCookies(res, gate.refreshedPayload)
        return res
      }
    }

    const res = new NextResponse(text, {
      status: upstream.status,
      headers: {
        'content-type': upstream.headers.get('content-type') || 'application/json; charset=utf-8',
        'cache-control': 'no-store',
      },
    })
    applyRefreshedSessionCookies(res, gate.refreshedPayload)
    return res
  } catch (error) {
    if (isKpiDashboardPath(path) || isOperationsOrExceptionsPath(path)) {
      const res = emptyKpiResponse(
        `Intelligence service unreachable (${error instanceof Error ? error.message : 'unknown'}).`,
      )
      applyRefreshedSessionCookies(res, gate.refreshedPayload)
      return res
    }
    if (isPatternDetailPath(path)) {
      const res = path === BACKEND_SERVICES.INTELLIGENCE.ENDPOINTS.PATTERN_HISTORY
        ? emptyPatternHistoryResponse(tenantId)
        : emptyPatternResponse(
            `Pattern intelligence unreachable (${error instanceof Error ? error.message : 'unknown'}).`,
          )
      applyRefreshedSessionCookies(res, gate.refreshedPayload)
      return res
    }
    if (isBatchesListPath(path)) {
      const res = emptyBatchesResponse(tenantId, request)
      applyRefreshedSessionCookies(res, gate.refreshedPayload)
      return res
    }
    const res = NextResponse.json(
      {
        error: 'intelligence service unreachable',
        details: error instanceof Error ? error.message : 'unknown',
      },
      { status: 502 },
    )
    applyRefreshedSessionCookies(res, gate.refreshedPayload)
    return res
  }
}
