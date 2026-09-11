import { fetchProdJsonGet, fetchProdJsonGetWithMeta, type ProdJsonGetResult } from './fetchProdJsonGet'
import type { ApiDlqRow } from './prodApiTypes'

/** Matches zord-intent-engine `models.BatchSidebarItem` JSON. */
export type IntentEngineBatchSidebarItem = {
  batchId: string
  type: string
  totalValue: string
  transactions: number
  confirmedCount: number
  highConfidenceCount?: number
  mismatchCount: number
  unresolvedCount: number
}

export type IntentEnginePagination = {
  page?: number
  page_size?: number
  total?: number
}

export type PaymentIntentGovernance = {
  semantic_valid?: boolean
  semantic_errors?: unknown
  duplicate_detected?: boolean
  duplicate_reason?: string
  missing_fields?: unknown
  low_confidence_fields?: unknown
  routing_consistent?: boolean
  execution_window_valid?: boolean
  policy_flags?: unknown
}

export type PaymentIntentRecord = {
  intent_id: string
  envelope_id?: string
  tenant_id?: string
  contract_id?: string
  trace_id?: string
  idempotency_key?: string
  intent_type?: string
  amount?: string | number
  currency?: string
  status?: string
  created_at?: string
  updated_at?: string
  intended_execution_at?: string
  beneficiary_type?: string
  client_payout_ref?: string
  client_batch_ref?: string
  batchid?: string
  source_row_num?: number
  governance_state?: string
  business_state?: string
  duplicate_risk_flag?: boolean
  aggregate_confidence_score?: number
  intent_quality_score?: number | null
  confidence_score?: number | null
  constraints?: { execution_window?: string }
  beneficiary?: {
    country?: string
    instrument?: { kind?: string; vpa_token?: string; ifsc_token?: string }
    name_token?: string
  }
  governance?: PaymentIntentGovernance
  canonical_snapshot_ref?: string
  nir_snapshot_ref?: string
  governance_snapshot_ref?: string
  governance_hash?: string
  request_fingerprint?: string
}

export type IntentEngineBatchesListResponse = {
  items: IntentEngineBatchSidebarItem[]
}

export type IntentEngineBatchesDetailResponse = {
  items: IntentEngineBatchSidebarItem[]
  batchDetails: {
    batchId: string
    paymentIntents: {
      items: PaymentIntentRecord[]
      pagination: IntentEnginePagination
    }
    dlqItems: {
      items: ApiDlqRow[]
      pagination: IntentEnginePagination
    }
  }
}

const BATCHES_PATH = '/api/prod/intents/batches'

function batchesUrl(extra?: Record<string, string | number | undefined>) {
  const params = new URLSearchParams()
  if (extra) {
    for (const [k, v] of Object.entries(extra)) {
      if (k === 'tenant_id') continue
      if (v !== undefined && v !== '') params.set(k, String(v))
    }
  }
  const qs = params.toString()
  return qs ? `${BATCHES_PATH}?${qs}` : BATCHES_PATH
}

export type IntentEngineBatchesFetchResult = ProdJsonGetResult<IntentEngineBatchesListResponse>

/**
 * Sidebar list. BFF injects tenant from the signed-in session; client never sends tenant_id.
 */
export async function getProdIntentEngineBatchesForSession(): Promise<IntentEngineBatchesFetchResult> {
  return fetchProdJsonGetWithMeta<IntentEngineBatchesListResponse>(batchesUrl())
}

/** @deprecated Use getProdIntentEngineBatchesForSession — tenant is session-only. */
export async function getProdIntentEngineBatches(_tenantId?: string): Promise<IntentEngineBatchesFetchResult> {
  return getProdIntentEngineBatchesForSession()
}

/** Max rows per upstream request (intent-engine caps page_size at 200). */
export const INTENT_ENGINE_BATCH_DETAIL_CHUNK = 200

/** Batch drill-down. BFF injects session tenant; client never sends tenant_id. */
export async function getProdIntentEngineBatchDetail(
  _tenantId: string | undefined,
  batchId: string,
  opts?: { page?: number; pageSize?: number },
): Promise<IntentEngineBatchesDetailResponse | null> {
  const bid = batchId.trim()
  if (!bid) return null
  return fetchProdJsonGet<IntentEngineBatchesDetailResponse>(
    batchesUrl({
      batch_id: bid,
      page: opts?.page ?? 1,
      page_size: opts?.pageSize ?? INTENT_ENGINE_BATCH_DETAIL_CHUNK,
    }),
  )
}

/**
 * Loads every intent + DLQ row for a batch (multiple upstream pages), so the journal
 * can paginate in the browser without a 20-row cap on what the user can browse.
 */
export async function getProdIntentEngineBatchDetailAll(
  tenantId: string | undefined,
  batchId: string,
): Promise<IntentEngineBatchesDetailResponse | null> {
  const bid = batchId.trim()
  if (!bid) return null

  const first = await getProdIntentEngineBatchDetail(tenantId, bid, {
    page: 1,
    pageSize: INTENT_ENGINE_BATCH_DETAIL_CHUNK,
  })
  if (!first?.batchDetails || first.batchDetails.batchId !== bid) return first

  const intentTotal =
    first.batchDetails.paymentIntents.pagination?.total ??
    first.batchDetails.paymentIntents.items.length
  const dlqTotal =
    first.batchDetails.dlqItems.pagination?.total ?? first.batchDetails.dlqItems.items.length

  const pagesNeeded = Math.max(
    1,
    Math.ceil(intentTotal / INTENT_ENGINE_BATCH_DETAIL_CHUNK),
    Math.ceil(dlqTotal / INTENT_ENGINE_BATCH_DETAIL_CHUNK),
  )

  let allIntents = [...first.batchDetails.paymentIntents.items]
  let allDlq = [...first.batchDetails.dlqItems.items]

  for (let page = 2; page <= pagesNeeded; page++) {
    const res = await getProdIntentEngineBatchDetail(tenantId, bid, {
      page,
      pageSize: INTENT_ENGINE_BATCH_DETAIL_CHUNK,
    })
    if (!res?.batchDetails || res.batchDetails.batchId !== bid) break
    allIntents = allIntents.concat(res.batchDetails.paymentIntents.items)
    allDlq = allDlq.concat(res.batchDetails.dlqItems.items)
  }

  return {
    ...first,
    batchDetails: {
      ...first.batchDetails,
      paymentIntents: {
        items: allIntents,
        pagination: {
          page: 1,
          page_size: allIntents.length,
          total: intentTotal,
        },
      },
      dlqItems: {
        items: allDlq,
        pagination: {
          page: 1,
          page_size: allDlq.length,
          total: dlqTotal,
        },
      },
    },
  }
}
