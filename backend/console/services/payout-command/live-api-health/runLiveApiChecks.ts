import { fetchProdJsonGetWithMeta } from '../prod-api/fetchProdJsonGet'
import type {
  AmbiguityKpiResponse,
  BatchesListResponse,
  DefensibilityKpiResponse,
  LeakageKpiResponse,
  PatternsKpiResponse,
  RecommendationsKpiResponse,
} from '../prod-api/intelligenceTypes'
import type { PatternDetailResponse, PatternHistoryResponse } from '../prod-api/intelligencePatternTypes'
import type { DisbursementTrendResponse } from '../prod-api/disbursementTrendTypes'
import type { ListPacksResponse } from '../prod-api/evidenceTypes'

export type LiveApiCheckStatus = 'ok' | 'empty' | 'error' | 'skipped'

export type LiveApiCheckResult = {
  id: string
  label: string
  status: LiveApiCheckStatus
  httpStatus: number
  detail: string
  url: string
}

export type RunLiveApiChecksOptions = {
  batchId?: string
  /** When false, checks are not run (e.g. tenant not ready). */
  enabled?: boolean
}

function summarizeDataAvailable(
  payload: { data_available?: boolean; reason?: string } | null,
  ok: boolean,
): { status: LiveApiCheckStatus; detail: string } {
  if (!ok || !payload) return { status: 'error', detail: 'Request failed or unreachable' }
  if (payload.data_available === true) return { status: 'ok', detail: 'data_available: true' }
  const reason = payload.reason?.trim() || 'data_available: false'
  return { status: 'empty', detail: reason }
}

function checkFromMeta(
  id: string,
  label: string,
  meta: Awaited<ReturnType<typeof fetchProdJsonGetWithMeta<unknown>>>,
  summarize: (data: unknown) => { status: LiveApiCheckStatus; detail: string },
): LiveApiCheckResult {
  const { status, detail } = summarize(meta.data)
  return {
    id,
    label,
    status: meta.ok ? status : 'error',
    httpStatus: meta.status,
    detail: meta.ok ? detail : meta.errorText?.slice(0, 120) || 'HTTP error',
    url: meta.url,
  }
}

/**
 * Probes live BFF routes for the signed-in session (credentials: include).
 * Tenant is injected server-side; client does not send tenant_id.
 */
export async function runLiveApiChecks(options: RunLiveApiChecksOptions = {}): Promise<LiveApiCheckResult[]> {
  const { batchId, enabled = true } = options
  if (!enabled) {
    return [{ id: 'skipped', label: 'Checks', status: 'skipped', httpStatus: 0, detail: 'Tenant not ready', url: '' }]
  }

  const bid = batchId?.trim() ?? ''
  const patternsQs = bid ? `?batch_id=${encodeURIComponent(bid)}` : ''
  const evidenceQs = bid ? `?batch_id=${encodeURIComponent(bid)}` : ''

  const probes: Array<{
    id: string
    label: string
    url: string
    summarize: (data: unknown) => { status: LiveApiCheckStatus; detail: string }
  }> = [
    {
      id: 'leakage',
      label: 'Intelligence · leakage',
      url: '/api/prod/intelligence/leakage',
      summarize: (d) => summarizeDataAvailable(d as LeakageKpiResponse | null, true),
    },
    {
      id: 'ambiguity',
      label: 'Intelligence · ambiguity',
      url: '/api/prod/intelligence/ambiguity',
      summarize: (d) => summarizeDataAvailable(d as AmbiguityKpiResponse | null, true),
    },
    {
      id: 'patterns',
      label: `Intelligence · patterns${bid ? ` (${bid})` : ''}`,
      url: `/api/prod/intelligence/patterns${patternsQs}`,
      summarize: (d) => summarizeDataAvailable(d as PatternsKpiResponse | null, true),
    },
    {
      id: 'pattern-detail',
      label: 'Intelligence · pattern detail',
      url: '/api/prod/intelligence/pattern',
      summarize: (d) => {
        const payload = d as PatternDetailResponse | null
        if (!payload) return { status: 'error', detail: 'No response' }
        if (payload.data_available === true) return { status: 'ok', detail: 'pattern snapshot available' }
        return { status: 'empty', detail: payload.reason?.trim() || 'data_available: false' }
      },
    },
    {
      id: 'pattern-history',
      label: 'Intelligence · pattern history',
      url: '/api/prod/intelligence/pattern/history?limit=5',
      summarize: (d) => {
        const payload = d as PatternHistoryResponse | null
        if (!payload) return { status: 'error', detail: 'No response' }
        const n = payload.snapshots?.length ?? payload.count ?? 0
        return { status: n > 0 ? 'ok' : 'empty', detail: n > 0 ? `${n} snapshot(s)` : 'empty history' }
      },
    },
    {
      id: 'defensibility',
      label: 'Intelligence · defensibility',
      url: '/api/prod/intelligence/defensibility',
      summarize: (d) => summarizeDataAvailable(d as DefensibilityKpiResponse | null, true),
    },
    {
      id: 'recommendations',
      label: 'Intelligence · recommendations',
      url: '/api/prod/intelligence/recommendations',
      summarize: (d) => summarizeDataAvailable(d as RecommendationsKpiResponse | null, true),
    },
    {
      id: 'intel-batches',
      label: 'Intelligence · batches',
      url: '/api/prod/intelligence/batches?limit=5',
      summarize: (d) => {
        const list = d as BatchesListResponse | null
        if (!list) return { status: 'error', detail: 'No response' }
        const n = list.batches?.length ?? 0
        if (list.intelligence_mode === 'offline') return { status: 'empty', detail: 'intelligence offline' }
        return { status: n > 0 ? 'ok' : 'empty', detail: n > 0 ? `${n} batch(es)` : 'empty batch list' }
      },
    },
    {
      id: 'evidence-packs',
      label: `Evidence · packs${bid ? ` (${bid})` : ''}`,
      url: `/api/prod/evidence/packs${evidenceQs}`,
      summarize: (d) => {
        const list = d as ListPacksResponse | null
        if (!list) return { status: 'error', detail: 'No response' }
        const n = list.packs?.length ?? 0
        return { status: n > 0 ? 'ok' : 'empty', detail: n > 0 ? `${n} pack(s)` : 'no packs' }
      },
    },
    {
      id: 'intent-batches',
      label: 'Intent engine · batches',
      url: '/api/prod/intents/batches?page=1&page_size=5',
      summarize: (d) => {
        const body = d as { batches?: unknown[] } | null
        if (!body) return { status: 'error', detail: 'No response' }
        const n = body.batches?.length ?? 0
        return { status: n > 0 ? 'ok' : 'empty', detail: n > 0 ? `${n} batch(es)` : 'empty batch list' }
      },
    },
    {
      id: 'settlement',
      label: 'Settlement · observations',
      url: '/api/prod/settlement/observations/batches',
      summarize: (d) => {
        const body = d as { batches?: unknown[]; observations?: unknown[] } | null
        if (!body) return { status: 'error', detail: 'No response' }
        const n = (body.batches?.length ?? 0) + (body.observations?.length ?? 0)
        return { status: 'ok', detail: n > 0 ? `${n} row(s)` : 'reachable (empty)' }
      },
    },
    {
      id: 'disbursement-trend',
      label: 'Home · payment trend (leakage windows)',
      url: '/api/prod/home/disbursement-trend?range=month',
      summarize: (d) => {
        const trend = d as DisbursementTrendResponse | null
        if (!trend) return { status: 'error', detail: 'No response' }
        if (trend.data_available === true) {
          const buckets = trend.buckets?.length ?? 0
          return { status: buckets > 0 ? 'ok' : 'empty', detail: `${buckets} bucket(s)` }
        }
        return { status: 'empty', detail: 'data_available: false' }
      },
    },
    {
      id: 'ingest-status',
      label: 'Home · ingest status',
      url: '/api/prod/ingest-status',
      summarize: (d) => {
        const body = d as { sources?: Array<{ id: string; status: string }> } | null
        if (!body?.sources) return { status: 'error', detail: 'No response' }
        const received = body.sources.filter((s) => s.status === 'received').length
        return { status: 'ok', detail: `${received}/${body.sources.length} source(s) received` }
      },
    },
    {
      id: 'intents-list',
      label: 'Customer/console · intents',
      url: '/api/prod/intents?page=1&page_size=5',
      summarize: (d) => {
        const body = d as { intents?: unknown[]; items?: unknown[] } | null
        if (!body) return { status: 'error', detail: 'No response' }
        const n = body.intents?.length ?? body.items?.length ?? 0
        return { status: 'ok', detail: n > 0 ? `${n} intent(s)` : 'reachable (empty)' }
      },
    },
    {
      id: 'dlq-list',
      label: 'Customer/console · dlq',
      url: '/api/prod/dlq?page=1&page_size=5',
      summarize: (d) => {
        const body = d as { items?: unknown[]; recent_failures?: unknown[] } | null
        if (!body) return { status: 'error', detail: 'No response' }
        const n = body.items?.length ?? body.recent_failures?.length ?? 0
        return { status: 'ok', detail: n > 0 ? `${n} item(s)` : 'reachable (empty)' }
      },
    },
    {
      id: 'console-overview',
      label: 'Console · overview',
      url: '/api/prod/overview',
      summarize: (d) => ({
        status: d ? 'ok' : 'error',
        detail: d ? 'overview payload' : 'No response',
      }),
    },
    {
      id: 'router-health',
      label: 'Router · health',
      url: '/api/prod/routing/health',
      summarize: (d) => {
        const body = d as { status?: string; service?: string } | null
        if (!body) return { status: 'error', detail: 'No response' }
        if (body.status === 'ok') return { status: 'ok', detail: body.service || 'zord-router' }
        return { status: 'empty', detail: 'router reachable but not ok' }
      },
    },
    {
      id: 'router-processors',
      label: 'Router · processors',
      url: '/api/prod/routing/processors',
      summarize: (d) => {
        const body = d as { processors?: unknown[] } | null
        if (!body) return { status: 'error', detail: 'No response' }
        const n = body.processors?.length ?? 0
        return { status: n > 0 ? 'ok' : 'empty', detail: n > 0 ? `${n} processor(s)` : 'no processors' }
      },
    },
    {
      id: 'zord-overview',
      label: 'Zord metrics · overview',
      url: '/api/prod/zord/metrics/overview?time_range=24h',
      summarize: (d) => ({
        status: d ? 'ok' : 'error',
        detail: d ? 'metrics reachable (synthetic BFF)' : 'No response',
      }),
    },
  ]

  if (bid) {
    probes.push(
      {
        id: 'intent-batch-ids',
        label: 'Intent journal · batch-ids',
        url: '/api/prod/intents/batch-ids',
        summarize: (d) => {
          const body = d as { items?: unknown[] } | null
          if (!body) return { status: 'error', detail: 'No response' }
          const n = body.items?.length ?? 0
          return { status: n > 0 ? 'ok' : 'empty', detail: n > 0 ? `${n} batch id(s)` : 'empty list' }
        },
      },
      {
        id: 'intent-payment-intents',
        label: `Intent journal · payment-intents (${bid})`,
        url: `/api/prod/intents/payment-intents?batch_id=${encodeURIComponent(bid)}`,
        summarize: (d) => {
          const body = d as { items?: unknown[] } | null
          if (!body) return { status: 'error', detail: 'No response' }
          const n = body.items?.length ?? 0
          return { status: 'ok', detail: n > 0 ? `${n} intent(s)` : 'reachable (empty)' }
        },
      },
      {
        id: 'intent-dlq-items',
        label: `Intent journal · dlq-items (${bid})`,
        url: `/api/prod/intents/dlq-items?batch_id=${encodeURIComponent(bid)}`,
        summarize: (d) => {
          const body = d as { items?: unknown[] } | null
          if (!body) return { status: 'error', detail: 'No response' }
          const n = body.items?.length ?? 0
          return { status: 'ok', detail: n > 0 ? `${n} review item(s)` : 'reachable (empty)' }
        },
      },
      {
        id: 'intel-batch-detail',
        label: `Intelligence · batch detail (${bid})`,
        url: `/api/prod/intelligence/batches/${encodeURIComponent(bid)}`,
        summarize: (d) => {
          const body = d as { batch?: unknown; data_available?: boolean } | null
          if (!body) return { status: 'error', detail: 'No response' }
          if (body.batch) return { status: 'ok', detail: 'batch projection loaded' }
          return { status: 'empty', detail: 'no batch detail' }
        },
      },
      {
        id: 'settlement-batch',
        label: `Settlement · observations (${bid})`,
        url: `/api/prod/settlement/observations/batches?client_batch_id=${encodeURIComponent(bid)}`,
        summarize: (d) => {
          const body = d as { observations?: unknown[] } | null
          if (!body) return { status: 'error', detail: 'No response' }
          const n = body.observations?.length ?? 0
          return { status: 'ok', detail: n > 0 ? `${n} observation(s)` : 'reachable (empty)' }
        },
      },
    )
  }

  const results: LiveApiCheckResult[] = []
  for (const probe of probes) {
    const meta = await fetchProdJsonGetWithMeta(probe.url)
    results.push(checkFromMeta(probe.id, probe.label, meta, probe.summarize))
  }
  return results
}
