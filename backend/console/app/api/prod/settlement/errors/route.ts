import { NextRequest } from 'next/server'
import {
  applyRefreshedSessionCookies,
  resolveSettlementUploadContext,
  TENANT_MISMATCH_BODY,
} from '@/services/auth/resolvePayoutTenant.server'
import {
  labelSettlementResponse,
  relaySettlementUpstream,
  resolveSettlementSource,
  settlementJson,
} from '@/services/settlementSource.server'

export const dynamic = 'force-dynamic'
export const runtime = 'nodejs'

/** Proxy: GET /api/prod/settlement/errors → outcome-engine settlement parse errors. */
export async function GET(request: NextRequest) {
  const resolved = resolveSettlementSource()
  if (!resolved.ok) return resolved.response
  const source = resolved.source

  const ctx = await resolveSettlementUploadContext(
    request,
    process.env.ZORD_SETTLEMENT_API_KEY ?? process.env.ZORD_BULK_INGEST_API_KEY,
  )
  if (!ctx.ok) return labelSettlementResponse(ctx.response, source)
  const tenantId = ctx.tenantId

  const queryTenant = request.nextUrl.searchParams.get('tenant_id')?.trim()
  if (queryTenant && queryTenant !== tenantId) {
    const res = settlementJson(TENANT_MISMATCH_BODY, source, { status: 403 })
    applyRefreshedSessionCookies(res, ctx.refreshedPayload)
    return res
  }

  const batchId =
    request.nextUrl.searchParams.get('batch_id')?.trim() ||
    request.nextUrl.searchParams.get('client_batch_id')?.trim()

  const upstreamParams = new URLSearchParams({ tenant_id: tenantId })
  if (batchId) upstreamParams.set('batch_id', batchId)

  const url = `${source.baseUrl}/v1/settlement/errors?${upstreamParams.toString()}`

  try {
    const upstream = await fetch(url, {
      method: 'GET',
      headers: {
        'content-type': 'application/json',
        authorization: ctx.authorization,
        'x-tenant-id': tenantId,
        'tenant-id': tenantId,
        tenant_id: tenantId,
      },
      cache: 'no-store',
    })
    const res = await relaySettlementUpstream(upstream, source)
    applyRefreshedSessionCookies(res, ctx.refreshedPayload)
    return res
  } catch (error) {
    const res = settlementJson(
      {
        error: 'settlement parse errors upstream unavailable',
        upstream: url,
        details: error instanceof Error ? error.message : 'unknown',
      },
      source,
      { status: 502 },
    )
    applyRefreshedSessionCookies(res, ctx.refreshedPayload)
    return res
  }
}
