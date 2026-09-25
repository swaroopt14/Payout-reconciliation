import { NextRequest } from 'next/server'
import { BACKEND_SERVICES } from '@/config/api.endpoints'
import { resolveIntelligenceSource } from '@/services/settlementSourceFlag'
import {
  labelSettlementResponse,
  resolveSettlementSource,
  settlementJson,
  type SettlementSource,
} from '@/services/settlementSource.server'
import {
  applyRefreshedSessionCookies,
  requireSessionTenantForProdProxy,
} from '@/services/auth/resolvePayoutTenant.server'

export const dynamic = 'force-dynamic'

type SourceStatus = 'received' | 'missing' | 'partial' | 'processing'

type IngestSource = {
  id: 'intent_file' | 'settlement_file' | 'bank_statement' | 'evidence'
  label: string
  status: SourceStatus
  detail?: string
}

async function probeJson<T>(url: string, tenantId: string): Promise<T | null> {
  try {
    const res = await fetch(url, {
      headers: { 'content-type': 'application/json', 'x-tenant-id': tenantId },
      cache: 'no-store',
    })
    if (!res.ok) return null
    return (await res.json()) as T
  } catch {
    return null
  }
}

export async function GET(request: NextRequest) {
  const resolved = resolveSettlementSource()
  if (!resolved.ok) return resolved.response
  const settlementSource = resolved.source
  // Label the whole composite response demo if ANY probed source is the simulator.
  const intel = resolveIntelligenceSource()
  const labelSource: SettlementSource =
    settlementSource.kind === 'simulator' || intel.kind === 'simulator'
      ? { kind: 'simulator', baseUrl: settlementSource.baseUrl }
      : settlementSource

  const gate = await requireSessionTenantForProdProxy(request)
  if (!gate.ok) return labelSettlementResponse(gate.response, labelSource)
  const tenantId = gate.tenantId

  const intentBase = BACKEND_SERVICES.INTENT_ENGINE.BASE_URL
  const intelBase = intel.baseUrl
  const evidenceBase = BACKEND_SERVICES.EVIDENCE.BASE_URL

  const [intentProbe, settlement, evidencePacks, defensibility, patterns] = await Promise.all([
    probeJson<{ pagination?: { total?: number }; items?: unknown[] }>(
      `${intentBase}/v1/intents?page=1&page_size=1&tenant_id=${encodeURIComponent(tenantId)}`,
      tenantId,
    ),
    probeJson<{ items?: unknown[]; observations?: unknown[] }>(
      `${settlementSource.baseUrl}/v1/settlement/observations/batches?tenant_id=${encodeURIComponent(tenantId)}`,
      tenantId,
    ),
    probeJson<{ packs?: unknown[] }>(
      `${evidenceBase}${BACKEND_SERVICES.EVIDENCE.ENDPOINTS.PACKS}?tenant_id=${encodeURIComponent(tenantId)}&limit=1`,
      tenantId,
    ),
    probeJson<{ data_available?: boolean; bank_confirmed_rate?: number }>(
      `${intelBase}${BACKEND_SERVICES.INTELLIGENCE.ENDPOINTS.DEFENSIBILITY}?tenant_id=${encodeURIComponent(tenantId)}`,
      tenantId,
    ),
    probeJson<{ data_available?: boolean; total_count?: number }>(
      `${intelBase}${BACKEND_SERVICES.INTELLIGENCE.ENDPOINTS.PATTERNS}?tenant_id=${encodeURIComponent(tenantId)}`,
      tenantId,
    ),
  ])

  const intentCount =
    intentProbe?.pagination?.total ??
    intentProbe?.items?.length ??
    (patterns?.data_available === true ? (patterns.total_count ?? 0) : 0)
  const settlementCount = settlement?.items?.length ?? settlement?.observations?.length ?? 0
  const packCount = evidencePacks?.packs?.length ?? 0
  const bankHint =
    defensibility?.data_available === true && (defensibility.bank_confirmed_rate ?? 0) > 0

  const sources: IngestSource[] = [
    {
      id: 'intent_file',
      label: 'Payment instructions',
      status: intentCount > 0 ? 'received' : 'missing',
      detail: intentCount > 0 ? `${intentCount} instruction(s)` : undefined,
    },
    {
      id: 'settlement_file',
      label: 'Settlement file',
      status: settlementCount > 0 ? 'received' : 'missing',
      detail: settlementCount > 0 ? `${settlementCount} batch(es)` : undefined,
    },
    {
      id: 'bank_statement',
      label: 'Bank statement',
      status: bankHint ? 'partial' : 'missing',
      detail: bankHint ? 'Inferred from defensibility' : 'Awaiting bank-confirmed signal',
    },
    {
      id: 'evidence',
      label: 'Evidence',
      status: packCount > 0 ? 'received' : 'missing',
      detail: packCount > 0 ? `${packCount}+ pack(s)` : undefined,
    },
  ]

  const res = settlementJson({ tenant_id: tenantId, sources }, labelSource, {
    headers: { 'cache-control': 'no-store' },
  })
  applyRefreshedSessionCookies(res, gate.refreshedPayload)
  return res
}
