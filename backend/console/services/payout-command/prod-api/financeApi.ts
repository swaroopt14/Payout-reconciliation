import { fetchProdJsonGetWithMeta } from './fetchProdJsonGet'
import type {
  FinanceCashPosition,
  FinanceCashSchedule,
  FinanceEvaluation,
  FinanceException,
  FinanceInvestigation,
  FinancePayment,
  FinancePayout,
  FinanceReconRow,
  FinanceRefund,
  FinanceSettlementLine,
  FinanceSummary,
  FinanceEntityTimeline,
  RazorpaySettlementListResponse,
  RazorpaySettlementReconResponse,
} from './financeTypes'

const BASE = '/api/prod/finance'

export async function getFinanceSummary() {
  return fetchProdJsonGetWithMeta<FinanceSummary>(`${BASE}/summary`)
}

export async function getFinanceCashPosition() {
  return fetchProdJsonGetWithMeta<FinanceCashPosition>(`${BASE}/cash-position`)
}

export async function getFinanceResults(result?: string) {
  const suffix = result && result !== 'ALL' ? `?result=${encodeURIComponent(result)}` : ''
  return fetchProdJsonGetWithMeta<{
    records: number
    matched: number
    exceptions: number
    results: FinanceReconRow[]
  }>(`${BASE}/results${suffix}`)
}

export async function getFinanceInvestigations() {
  return fetchProdJsonGetWithMeta<{ investigations: FinanceInvestigation[] }>(
    `${BASE}/investigations`,
  )
}

export async function getFinanceEvaluation() {
  return fetchProdJsonGetWithMeta<FinanceEvaluation>(`${BASE}/evaluation`)
}

export async function runFinanceReconciliation(opts?: {
  batch_id?: string
  payout_ids?: string[]
}) {
  const response = await fetch(`${BASE}/run`, {
    method: 'POST',
    credentials: 'include',
    cache: 'no-store',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({
      batch_id: opts?.batch_id || undefined,
      payout_ids: opts?.payout_ids?.length ? opts.payout_ids : undefined,
    }),
  })
  if (!response.ok) {
    const errorText = await response.text()
    return { ok: false as const, status: response.status, errorText }
  }
  return { ok: true as const, status: response.status, data: await response.json() }
}

export async function getFinanceExceptions(opts?: { entityType?: string; reason?: string }) {
  const q = new URLSearchParams()
  if (opts?.entityType) q.set('entity_type', opts.entityType)
  if (opts?.reason) q.set('reason', opts.reason)
  const suffix = q.toString() ? `?${q.toString()}` : ''
  return fetchProdJsonGetWithMeta<{ exceptions: FinanceException[] }>(`${BASE}/exceptions${suffix}`)
}

export async function getFinancePayment(paymentId: string) {
  return fetchProdJsonGetWithMeta<FinancePayment>(
    `${BASE}/payments/${encodeURIComponent(paymentId)}`,
  )
}

export async function getFinancePayout(payoutId: string) {
  return fetchProdJsonGetWithMeta<FinancePayout>(
    `${BASE}/payouts/${encodeURIComponent(payoutId)}`,
  )
}

export async function getFinanceInvestigation(id: string) {
  return fetchProdJsonGetWithMeta<{ data: FinanceInvestigation }>(
    `${BASE}/investigations/${encodeURIComponent(id)}`,
  )
}

export async function getFinanceCashSchedule() {
  return fetchProdJsonGetWithMeta<FinanceCashSchedule>(`${BASE}/cash-schedule`)
}

export async function getFinanceTimeline(
  entity: 'payouts' | 'payments',
  entityId: string,
) {
  return fetchProdJsonGetWithMeta<FinanceEntityTimeline>(
    `${BASE}/${entity}/${encodeURIComponent(entityId)}/timeline`,
  )
}

export async function getFinanceRefunds(paymentId: string) {
  return fetchProdJsonGetWithMeta<{ payment_id: string; refunds: FinanceRefund[]; error?: string }>(
    `${BASE}/refunds?payment_id=${encodeURIComponent(paymentId)}`,
  )
}

export async function getFinanceSettlements(paymentId: string) {
  return fetchProdJsonGetWithMeta<{ settlements: FinanceSettlementLine[] }>(
    `${BASE}/settlements?payment_id=${encodeURIComponent(paymentId)}`,
  )
}

export async function getRazorpaySettlements(status?: string) {
  const q = status && status !== 'all' ? `?status=${encodeURIComponent(status)}` : ''
  return fetchProdJsonGetWithMeta<RazorpaySettlementListResponse>(`/api/prod/settlements${q}`)
}

export async function getSettlementReconCombined(settlementId: string) {
  const q = new URLSearchParams({ settlement_id: settlementId.trim() })
  return fetchProdJsonGetWithMeta<RazorpaySettlementReconResponse>(
    `/api/prod/settlements/recon/combined?${q.toString()}`,
  )
}

export async function createFinanceInvestigation(body: {
  exception_id?: string
  entity_id?: string
  payment_id?: string
}) {
  const response = await fetch(`${BASE}/investigations`, {
    method: 'POST',
    credentials: 'include',
    cache: 'no-store',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!response.ok) {
    const errorText = await response.text()
    return { ok: false as const, status: response.status, data: null, errorText }
  }
  const json = (await response.json()) as { data: FinanceInvestigation }
  return { ok: true as const, status: response.status, data: json.data }
}
