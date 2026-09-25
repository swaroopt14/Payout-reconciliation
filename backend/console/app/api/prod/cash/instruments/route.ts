import { NextRequest } from 'next/server'
import { applyAuthCookies } from '@/services/auth/server'
import {
  applyRefreshedSessionCookies,
  requireSessionTenantForProdProxy,
} from '@/services/auth/resolvePayoutTenant.server'
import {
  labelSettlementResponse,
  relaySettlementUpstream,
  resolveSettlementSource,
  settlementJson,
} from '@/services/settlementSource.server'

export const dynamic = 'force-dynamic'
export const runtime = 'nodejs'

export async function GET(request: NextRequest) {
  const resolved = resolveSettlementSource()
  if (!resolved.ok) return resolved.response
  const source = resolved.source

  const gate = await requireSessionTenantForProdProxy(request)
  if (!gate.ok) return labelSettlementResponse(gate.response, source)
  const url = `${source.baseUrl}/v1/cash/instruments`
  try {
    const upstream = await fetch(url, {
      method: 'GET',
      headers: { 'x-tenant-id': gate.tenantId },
      cache: 'no-store',
    })
    const res = await relaySettlementUpstream(upstream, source)
    if (gate.refreshedPayload) applyAuthCookies(res, gate.refreshedPayload)
    applyRefreshedSessionCookies(res, gate.refreshedPayload)
    return res
  } catch (error) {
    const res = settlementJson(
      {
        error: 'recon service unavailable',
        upstream: url,
        details: error instanceof Error ? error.message : 'unknown',
      },
      source,
      { status: 502 },
    )
    applyRefreshedSessionCookies(res, gate.refreshedPayload)
    return res
  }
}
