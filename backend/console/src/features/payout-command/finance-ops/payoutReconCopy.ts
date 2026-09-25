import type {
  FinanceException,
  FinancePayoutKpis,
  FinancePayment,
  FinancePayout,
  FinanceReconResult,
  FinanceReconRow,
} from '@/services/payout-command/prod-api/financeTypes'
import type { RazorpayPayoutStatus } from './razorpayPayoutStatus'

export type PayoutSignalSource = 'beneficiary_bank' | 'business' | 'gateway' | 'internal'

export type PayoutStatusDetails = {
  description: string
  source: PayoutSignalSource | string
  reason: string
}

export type PayoutReconDisplayRow = {
  payoutId: string
  status: RazorpayPayoutStatus | string
  amountMinor: number
  utr: string
  errorCode: string
  errorDescription: string
  signalSource: PayoutSignalSource | string
  evidence: string
  nextSteps: string
  result: FinanceReconResult
  reason: string
  /** Raw recon reason / reason_code from the API (before Razorpay alias mapping). */
  reasonCode?: string
  contact: string
  varianceMinor: number
  settlement: boolean | null
  bank: boolean | null
  mode?: string
  purpose?: string
  fundAccountId?: string
  referenceId?: string
  paymentProvider?: string
  statusDetails?: PayoutStatusDetails
  createdAt?: number
  fees?: number
  tax?: number
  currency?: string
  exceptionType?: string | null
  direction?: string
  rail?: string
  twoWay?: FinanceReconRow['two_way']
  threeWay?: FinanceReconRow['three_way']
  /** True when bank cash movement is proven (list `bank` or overlay `bank_credit_proven`). */
  bankCreditProven?: boolean
}

/** Official Razorpay payout status_details.reason catalogue. */
export type RazorpayReasonMeta = {
  reason: string
  status: RazorpayPayoutStatus
  source: PayoutSignalSource
  description: string
  nextSteps: string
}

export const RAZORPAY_REASON_CATALOG: RazorpayReasonMeta[] = [
  // Status: reversed / failed
  {
    reason: 'bank_account_closed',
    status: 'failed',
    source: 'beneficiary_bank',
    description: 'Payout failed as the beneficiary account is closed. Please contact the beneficiary bank.',
    nextSteps: 'NA',
  },
  {
    reason: 'bank_account_frozen',
    status: 'failed',
    source: 'beneficiary_bank',
    description: 'Payout failed as beneficiary account is frozen. Please contact the beneficiary bank.',
    nextSteps: 'NA',
  },
  {
    reason: 'bank_account_invalid',
    status: 'failed',
    source: 'beneficiary_bank',
    description: 'Payout failed due to invalid beneficiary account details.',
    nextSteps: 'NA',
  },
  {
    reason: 'beneficiary_account_dormant',
    status: 'failed',
    source: 'beneficiary_bank',
    description: 'Payout failed as beneficiary account is dormant. Please contact the beneficiary bank.',
    nextSteps: 'NA',
  },
  {
    reason: 'beneficiary_bank_failure',
    status: 'failed',
    source: 'beneficiary_bank',
    description: 'Payout failed at the beneficiary bank due to a technical issue. Please retry after 30 min.',
    nextSteps: 'Retry',
  },
  {
    reason: 'beneficiary_bank_offline',
    status: 'failed',
    source: 'beneficiary_bank',
    description: 'Beneficiary bank systems are offline. Please retry after 30 min.',
    nextSteps: 'Retry',
  },
  {
    reason: 'beneficiary_bank_rejected',
    status: 'failed',
    source: 'beneficiary_bank',
    description: 'Payout rejected by the beneficiary bank. Please contact the beneficiary bank.',
    nextSteps: 'NA',
  },
  {
    reason: 'beneficiary_bank_technical_error',
    status: 'failed',
    source: 'beneficiary_bank',
    description: 'Payout failed due to a technical issue at the beneficiary bank. Please retry after 30 min.',
    nextSteps: 'Retry',
  },
  {
    reason: 'beneficiary_psp_offline',
    status: 'failed',
    source: 'beneficiary_bank',
    description: 'Beneficiary PSP systems are offline. Please retry after 30 min.',
    nextSteps: 'Retry',
  },
  {
    reason: 'imps_not_allowed',
    status: 'failed',
    source: 'beneficiary_bank',
    description: 'IMPS is not enabled on beneficiary account. Please retry with different mode.',
    nextSteps: 'Retry with a different payment mode.',
  },
  {
    reason: 'invalid_ifsc_code',
    status: 'failed',
    source: 'beneficiary_bank',
    description: 'Payout failed as the IFSC code is invalid. Please correct the IFSC code and retry.',
    nextSteps: 'Retry with correct IFSC code.',
  },
  {
    reason: 'npci_beneficiary_timeout',
    status: 'failed',
    source: 'beneficiary_bank',
    description: 'Temporary technical issue between NPCI and the beneficiary bank. Please retry after 30 min.',
    nextSteps: 'Retry',
  },
  {
    reason: 'transaction_limit_exceeded',
    status: 'failed',
    source: 'beneficiary_bank',
    description: 'Payout amount greater than the limit supported by the beneficiary account.',
    nextSteps: 'NA',
  },
  {
    reason: 'amount_limit_exhausted_neft',
    status: 'failed',
    source: 'business',
    description: 'The NEFT 24*7 limits for your account has been exhausted. Please retry after sometime.',
    nextSteps: 'Retry',
  },
  {
    reason: 'beneficiary_account_invalid',
    status: 'failed',
    source: 'business',
    description: 'Payout failed due to invalid beneficiary account number.',
    nextSteps: 'NA',
  },
  {
    reason: 'beneficiary_vpa_invalid',
    status: 'failed',
    source: 'business',
    description: 'UPI validation failed. If the UPI ID is valid, please retry after sometime.',
    nextSteps: 'Ensure UPI ID is valid and retry.',
  },
  {
    reason: 'insufficient_funds',
    status: 'failed',
    source: 'business',
    description: 'Payout failed due to insufficient funds in your account.',
    nextSteps: 'Add funds to your account and retry.',
  },
  {
    reason: 'invalid_beneficiary',
    status: 'failed',
    source: 'business',
    description: 'Customer account does not exist with the wallet provider for the given phone number.',
    nextSteps: 'NA',
  },
  {
    reason: 'gateway_down',
    status: 'failed',
    source: 'gateway',
    description: 'Payout failed as the partner bank is facing technical issues. Please retry.',
    nextSteps: 'Retry',
  },
  {
    reason: 'gateway_technical_error',
    status: 'failed',
    source: 'gateway',
    description: 'Payout failed due to a temporary technical issue at the partner bank. Please retry after 30 min.',
    nextSteps: 'Retry',
  },
  {
    reason: 'gateway_timeout',
    status: 'failed',
    source: 'gateway',
    description: 'Payout timed out at the partner bank. Please retry after 30 min.',
    nextSteps: 'Retry',
  },
  {
    reason: 'server_error',
    status: 'failed',
    source: 'internal',
    description: 'Payout failed. Contact support for help.',
    nextSteps: 'Contact support to find out the exact issue.',
  },
  {
    reason: 'server_error_temporary',
    status: 'failed',
    source: 'internal',
    description: 'Payout failed due to temporary technical issue. Please retry.',
    nextSteps: 'Retry',
  },

  // Status: processing
  {
    reason: 'beneficiary_bank_confirmation_pending',
    status: 'processing',
    source: 'beneficiary_bank',
    description:
      'Confirmation of credit to the beneficiary is pending from beneficiary_bank. Please check the status after (date,time).',
    nextSteps: 'Inform the customer of the delay, reason for the same and by when it will be cleared.',
  },
  {
    reason: 'bank_window_closed',
    status: 'processing',
    source: 'gateway',
    description: 'The mode window for the day is closed. Please check the status after (date,time).',
    nextSteps: 'Inform the customer of the delay, reason for the same and by when it will be cleared.',
  },
  {
    reason: 'payout_bank_processing',
    status: 'processing',
    source: 'gateway',
    description: 'Payout is being processed by the partner bank. Please check the final status after (date,time).',
    nextSteps: 'Inform the customer of the delay, reason for the same and by when it will be cleared.',
  },
  {
    reason: 'amount_limit_exhausted',
    status: 'processing',
    source: 'business',
    description: 'The (mode) 24*7 limits for your account has been exhausted. Please check the status after (date,time).',
    nextSteps: 'Inform the customer of the delay, reason for the same and by when it will be cleared.',
  },
  {
    reason: 'partner_bank_pending',
    status: 'processing',
    source: 'internal',
    description: 'Payout is being processed by our partner bank. Please check the final status after (date,time).',
    nextSteps: 'Inform the customer of the delay, reason for the same and by when it will be cleared.',
  },

  // Status: processed
  {
    reason: 'payout_processed',
    status: 'processed',
    source: 'beneficiary_bank',
    description: 'Payout is processed and the money has been credited into the beneficiary’s account.',
    nextSteps: 'NA',
  },

  // Status: pending
  {
    reason: 'pending_approval',
    status: 'pending',
    source: 'business',
    description: 'Workflow for the payout is pending approval from the approver(s).',
    nextSteps: 'NA',
  },

  // Status: queued
  {
    reason: 'gateway_degraded',
    status: 'queued',
    source: 'gateway',
    description: 'Payout is queued as Partner bank systems are down.',
    nextSteps: 'NA',
  },
  {
    reason: 'beneficiary_bank_down',
    status: 'queued',
    source: 'gateway',
    description:
      'Beneficiary bank’s systems are not working. Payout will be processed after the system starts working else it will be failed after the pre-defined time limit.',
    nextSteps: 'NA',
  },
  {
    reason: 'low_balance',
    status: 'queued',
    source: 'business',
    description: 'Payout is queued as there is insufficient balance in your account to process the payout.',
    nextSteps: 'NA',
  },
  {
    reason: 'syncing_balance',
    status: 'queued',
    source: 'gateway',
    description: 'Payout is queued as your balance is being synced with the bank. Please check the status after some time.',
    nextSteps: 'Check status after some time.',
  },
  {
    reason: 'fee_recovery_pending',
    status: 'queued',
    source: 'business',
    description:
      'Payout is queued as you have a pending fee recovery payout. It will get processed after the fee recovery payout is cleared.',
    nextSteps: 'NA',
  },
]

const BY_REASON = new Map(RAZORPAY_REASON_CATALOG.map((r) => [r.reason, r]))

/** Status tabs on the reconciliation page (Razorpay payout lifecycle). */
export type ReconStatusTab =
  | 'all'
  | 'failed'
  | 'processing'
  | 'processed'
  | 'pending'
  | 'queued'

export const RECON_STATUS_TABS: Array<{ id: ReconStatusTab; label: string }> = [
  { id: 'all', label: 'All' },
  { id: 'failed', label: 'Failed / reversed' },
  { id: 'processing', label: 'Processing' },
  { id: 'processed', label: 'Processed' },
  { id: 'pending', label: 'Pending' },
  { id: 'queued', label: 'Queued' },
]

export function reasonsForStatusTab(tab: ReconStatusTab): RazorpayReasonMeta[] {
  if (tab === 'all') return RAZORPAY_REASON_CATALOG
  if (tab === 'failed') {
    return RAZORPAY_REASON_CATALOG.filter((r) => r.status === 'failed' || r.status === 'reversed')
  }
  return RAZORPAY_REASON_CATALOG.filter((r) => r.status === tab)
}

export function normalizePayoutStatus(status: string | undefined | null): string {
  return String(status || '').trim().toLowerCase()
}

export function matchesStatusTab(status: string, tab: ReconStatusTab): boolean {
  const s = normalizePayoutStatus(status)
  if (tab === 'all') return true
  if (tab === 'failed') return s === 'failed' || s === 'reversed' || s === 'rejected' || s === 'cancelled'
  return s === tab
}

/** Map internal recon reasons → nearest Razorpay status_details.reason. */
const INTERNAL_REASON_ALIAS: Record<string, string> = {
  matched: 'payout_processed',
  processed_exact_debit: 'payout_processed',
  failed_no_money_movement: 'beneficiary_bank_rejected',
  payout_failed_with_bank_movement: 'bank_account_frozen',
  failed_with_bank_movement: 'bank_account_frozen',
  payout_missing_bank: 'partner_bank_pending',
  payout_open_past_sla: 'partner_bank_pending',
  payout_open: 'pending_approval',
  payout_reversed_unexplained: 'beneficiary_bank_rejected',
  amount_mismatch: 'gateway_technical_error',
  shared_utr_or_bank_candidates: 'gateway_timeout',
  ambiguous_bank_candidates: 'gateway_timeout',
  captured_missing_settlement: 'payout_bank_processing',
  optimizer_settlement_unobserved: 'gateway_timeout',
  orphan_bank_credit: 'invalid_ifsc_code',
  open_status_no_downstream: 'pending_approval',
}

function resolveReasonKey(raw: string | undefined | null): string {
  const key = String(raw || '').trim()
  if (!key) return ''
  if (BY_REASON.has(key)) return key
  return INTERNAL_REASON_ALIAS[key] || key
}

export function lookupRazorpayReason(reason?: string | null, statusHint?: string): RazorpayReasonMeta | null {
  return lookupReason(resolveReasonKey(reason), statusHint)
}

function lookupReason(reasonKey: string, statusHint?: string): RazorpayReasonMeta | null {
  const hit = BY_REASON.get(reasonKey)
  if (hit) return hit
  const st = normalizePayoutStatus(statusHint)
  if (st === 'processed') return BY_REASON.get('payout_processed') || null
  if (st === 'pending') return BY_REASON.get('pending_approval') || null
  if (st === 'queued') return BY_REASON.get('low_balance') || null
  if (st === 'processing') return BY_REASON.get('payout_bank_processing') || null
  if (st === 'cancelled') return BY_REASON.get('low_balance') || null
  if (st === 'failed' || st === 'reversed' || st === 'rejected') {
    return BY_REASON.get('beneficiary_bank_rejected') || null
  }
  return null
}

/** Map API recon rows into Razorpay-style payout reconciliation table rows. */
export function mapFinanceRowToPayoutRecon(row: FinanceReconRow): PayoutReconDisplayRow {
  const details = row.status_details
  const reasonKey = resolveReasonKey(details?.reason || row.error_code || row.reason)
  const meta = lookupReason(reasonKey, row.status || details?.reason)
  const status = normalizePayoutStatus(row.status) || String(row.status || '').trim()

  const errorCode = details?.reason || row.error_code || (row.result && row.result !== 'MATCHED' ? reasonKey : '') || ''
  const errorDescription = details?.description || row.error_description || meta?.description || ''
  const signalSource = details?.source || row.signal_source || meta?.source || ''
  const nextSteps = row.next_steps || meta?.nextSteps || ''
  const evidence = row.evidence || errorDescription
  const payoutId = row.payout_id || row.payment_id

  return {
    payoutId,
    status,
    amountMinor: row.amount_minor ?? 0,
    utr: row.utr || '',
    errorCode,
    errorDescription,
    signalSource,
    evidence,
    nextSteps: nextSteps === 'NA' ? '' : nextSteps,
    result: row.result || '',
    reason: reasonKey || errorCode || row.reason || '',
    reasonCode: row.reason_code || row.reason || undefined,
    contact: row.contact || '',
    varianceMinor: row.variance_amount || 0,
    settlement: row.settlement,
    bank: row.bank,
    mode: row.mode,
    purpose: row.purpose,
    fundAccountId: row.fund_account_id,
    referenceId: row.reference_id,
    paymentProvider: row.payment_provider,
    createdAt: row.created_at,
    fees: row.fees,
    tax: row.tax,
    currency: row.currency || 'INR',
    exceptionType: row.exception_type ?? null,
    direction: row.direction,
    rail: row.rail,
    twoWay: row.two_way,
    threeWay: row.three_way,
    bankCreditProven: row.bank_credit_proven === true || row.bank === true,
    statusDetails: details
      ? {
          description: details.description,
          source: details.source,
          reason: details.reason,
        }
      : undefined,
  }
}

function unixFromIso(value?: string | null): number | undefined {
  if (!value) return undefined
  const ms = Date.parse(value)
  if (!Number.isFinite(ms)) return undefined
  return Math.floor(ms / 1000)
}

export function mapPayoutResponseToReconRow(payout: FinancePayout): FinanceReconRow {
  const rec = payout.reconciliation
  const created = unixFromIso(payout.provider_created_at)
  return {
    payment_id: payout.payout_id,
    payout_id: payout.payout_id,
    settlement: null,
    bank: rec?.bank_credit_proven ?? null,
    bank_credit_proven: rec?.bank_credit_proven,
    result: rec?.result || '',
    variance_amount: rec?.variance_amount ?? 0,
    reason: rec?.reason,
    reason_code: rec?.reason_code || undefined,
    seller_id: overlaySellerId(payout),
    status: payout.provider_status || payout.status,
    utr: payout.utr ?? null,
    amount_minor: payout.amount_minor,
    currency: payout.currency,
    mode: payout.mode,
    purpose: payout.purpose,
    created_at: created,
    error_code: payout.status_reason,
    direction: rec?.direction,
    rail: rec?.rail || payout.mode,
    two_way: rec?.two_way,
    three_way: rec?.three_way,
  }
}

export function mapPaymentResponseToReconRow(payment: FinancePayment): FinanceReconRow {
  const rec = payment.reconciliation
  const created = unixFromIso(payment.provider_created_at)
  return {
    payment_id: payment.payment_id,
    settlement: rec?.bank_credit_proven != null ? rec.bank_credit_proven : null,
    bank: rec?.bank_credit_proven ?? null,
    bank_credit_proven: rec?.bank_credit_proven,
    result: rec?.result || '',
    variance_amount: rec?.variance_amount ?? 0,
    reason: rec?.reason,
    reason_code: rec?.reason_code || undefined,
    seller_id: overlaySellerId(payment),
    status: payment.provider_status || payment.status,
    amount_minor: payment.amount_minor,
    currency: payment.currency,
    created_at: created,
    payment_provider: payment.provider,
    direction: rec?.direction,
    rail: rec?.rail || payment.method,
    two_way: rec?.two_way,
    three_way: rec?.three_way,
  }
}


/** Bank cash is proven — list uses `bank`, overlays use `bank_credit_proven`. */
export function isBankCreditProven(row: {
  bankCreditProven?: boolean | null
  bank?: boolean | null
  bank_credit_proven?: boolean | null
  threeWay?: { result?: string } | null
  three_way?: { result?: string } | null
}): boolean {
  if (row.bankCreditProven === true) return true
  if (row.bank_credit_proven === true) return true
  if (row.bank === true) return true
  const three = String(row.threeWay?.result || row.three_way?.result || '').toUpperCase()
  // three_way MATCHED is only emitted when bank movement is proven (or agreed no-movement).
  return three === 'MATCHED'
}

/**
 * Green "Reconciled" / money-in-bank chrome.
 * Close result MATCHED alone is books/PSP agreement — not bank cash.
 */
export function isBankProvenReconciled(row: {
  result?: string | null
  bankCreditProven?: boolean | null
  bank?: boolean | null
  bank_credit_proven?: boolean | null
  threeWay?: { result?: string } | null
  three_way?: { result?: string } | null
}): boolean {
  if (String(row.result || '').toUpperCase() !== 'MATCHED') return false
  return isBankCreditProven(row)
}

/** Empty seller_id surfaces as "unknown" — never invent an id. */
export function displaySellerId(sellerId?: string | null): string {
  const s = String(sellerId ?? '').trim()
  return s ? s : 'unknown'
}

/** zord-recon reason: refund issued, seller reverse transfer not yet recorded. */
export const REASON_REFUND_WITHOUT_REVERSE_TRANSFER = 'refund_without_reverse_transfer'

type ReasonLegProbe = { reason?: string | null; reason_code?: string | null } | null | undefined

/** Any row shape that carries a recon reason (list row, exception, overlay, display row). */
export type ReverseTransferProbe = {
  reason?: string | null
  reason_code?: string | null
  reasonCode?: string | null
  errorCode?: string | null
  error_code?: string | null
  result?: string | null
  reconciliation_result?: string | null
  two_way?: ReasonLegProbe
  three_way?: ReasonLegProbe
  twoWay?: ReasonLegProbe
  threeWay?: ReasonLegProbe
}

function isReverseTransferReason(value?: string | null): boolean {
  return String(value ?? '').trim().toLowerCase() === REASON_REFUND_WITHOUT_REVERSE_TRANSFER
}

/**
 * Refund issued; marketplace reverse transfer from the seller not yet recorded.
 * NOT a cash gap: never styled as variance/shortfall and excluded from every cash total.
 * Matches reason / reason_code / two_way / three_way reasons case-insensitively.
 * A MATCHED row is never pending.
 */
export function isPendingSellerReverseTransfer(row?: ReverseTransferProbe | null): boolean {
  if (!row) return false
  const result = String(row.result || row.reconciliation_result || '').trim().toUpperCase()
  if (result === 'MATCHED') return false
  return [
    row.reason,
    row.reason_code,
    row.reasonCode,
    row.errorCode,
    row.error_code,
    row.two_way?.reason,
    row.two_way?.reason_code,
    row.three_way?.reason,
    row.three_way?.reason_code,
    row.twoWay?.reason,
    row.twoWay?.reason_code,
    row.threeWay?.reason,
    row.threeWay?.reason_code,
  ].some(isReverseTransferReason)
}

/** Amount that may count toward cash / variance / exposure totals — 0 for pending seller reverse transfers. */
export function cashCountableMinor(
  row: ReverseTransferProbe | null | undefined,
  amountMinor: number | null | undefined,
): number {
  if (isPendingSellerReverseTransfer(row)) return 0
  return amountMinor || 0
}

/** Rows that belong in cash totals (drops pending seller reverse transfers). */
export function withoutPendingReverseTransfers<T extends ReverseTransferProbe>(rows: T[]): T[] {
  return rows.filter((r) => !isPendingSellerReverseTransfer(r))
}

/**
 * D48 — never guess a seller. Only the explicit server `reconciliation.seller_id` field counts;
 * nothing is derived from evidence_refs, payments, transfers, amounts or timing.
 * Empty/absent → undefined → render via displaySellerId ("unknown").
 */
export function overlaySellerId(
  entity?: Pick<FinancePayment, 'reconciliation'> | Pick<FinancePayout, 'reconciliation'> | null,
): string | undefined {
  const direct = String(entity?.reconciliation?.seller_id ?? '').trim()
  return direct || undefined
}

/** Linked payment id from exception evidence_refs.sources (kind "payment"). */
export function exceptionLinkedPaymentId(ex?: Pick<FinanceException, 'evidence_refs'> | null): string | undefined {
  const hit = ex?.evidence_refs?.sources?.find((s) => String(s?.kind || '').toLowerCase() === 'payment')
  return hit?.id || undefined
}

export function isOpenReconResult(result: string): boolean {
  const r = result.toUpperCase()
  return r !== 'MATCHED'
}

export function payoutStatusBucket(status: string): 'processed' | 'review' | 'failed' {
  const s = normalizePayoutStatus(status)
  if (s === 'processed') return 'processed'
  if (s === 'pending' || s === 'queued' || s === 'processing' || s === 'scheduled') return 'review'
  return 'failed'
}

export type DemoPayoutKpis = {
  /** Informational only — pending seller reverse transfers are excluded from every amount/count below. */
  pendingReverseTransferCount: number
  scoredCount: number
  totalAmount: number
  processedCount: number
  processedAmount: number
  reviewCount: number
  reviewAmount: number
  failedCount: number
  failedAmount: number
}

export function sumPayoutKpis(
  rows: Array<{ status: string; amountMinor: number } & ReverseTransferProbe>,
): DemoPayoutKpis {
  const out: DemoPayoutKpis = {
    pendingReverseTransferCount: 0,
    scoredCount: 0,
    totalAmount: 0,
    processedCount: 0,
    processedAmount: 0,
    reviewCount: 0,
    reviewAmount: 0,
    failedCount: 0,
    failedAmount: 0,
  }
  for (const row of rows) {
    // Refund awaiting seller reverse transfer is not a cash gap — keep it out of every total.
    if (isPendingSellerReverseTransfer(row)) {
      out.pendingReverseTransferCount += 1
      continue
    }
    out.scoredCount += 1
    const amt = row.amountMinor || 0
    out.totalAmount += amt
    const bucket = payoutStatusBucket(row.status)
    if (bucket === 'processed') {
      out.processedCount += 1
      out.processedAmount += amt
    } else if (bucket === 'review') {
      out.reviewCount += 1
      out.reviewAmount += amt
    } else {
      out.failedCount += 1
      out.failedAmount += amt
    }
  }
  return out
}

export function isTerminalFailedStatus(status: string): boolean {
  return payoutStatusBucket(status) === 'failed'
}

/** Derived SLA label from Razorpay status + status_details (not a native payout field). */
export function slaFromPayout(opts: {
  status: string
  nextSteps?: string | null
  reason?: string | null
}): string {
  const st = normalizePayoutStatus(opts.status)
  const next = String(opts.nextSteps || '').toLowerCase()
  if (st === 'processed') return 'Met'
  if (st === 'reversed') return 'Recovered'
  if (st === 'cancelled' || st === 'rejected') return 'Closed'
  if (st === 'failed') return next.includes('retry') ? 'Retry window' : 'Closed'
  if (st === 'processing') return 'In bank window'
  if (st === 'queued') return 'Queued'
  if (st === 'pending') return 'Awaiting approval'
  if (st === 'scheduled') return 'Scheduled'
  if (opts.reason === 'payout_processed') return 'Met'
  return '—'
}

export type PayoutKpisSource = 'server' | 'client'

export type ResolvedPayoutKpis = DemoPayoutKpis & {
  /** 'server' = summary.payout_kpis from recon; 'client' = sumPayoutKpis over result rows. */
  source: PayoutKpisSource
  currency: 'INR'
  /** Client rows in a non-INR currency, kept out of every INR total. */
  nonInrExcludedCount: number
  /** Why server payout_kpis were not used (absent, non_inr_currency, invalid_amounts). */
  serverRejectedReason?: 'absent' | 'non_inr_currency' | 'invalid_amounts'
}

function isInr(currency?: string | null): boolean {
  const c = String(currency ?? '').trim().toUpperCase()
  return c === '' || c === 'INR'
}

function isMinorInt(n: unknown): n is number {
  return typeof n === 'number' && Number.isSafeInteger(n) && n >= 0
}

/**
 * Payout KPIs, INR only (EM Slice 8). Prefers server `payout_kpis` when present, INR (or no currency),
 * and every figure is a non-negative safe integer (D10). Otherwise falls back to the client sum over
 * INR result rows — which also drops pending seller reverse transfers (sumPayoutKpis).
 */
export function resolvePayoutKpis(
  server: FinancePayoutKpis | null | undefined,
  summaryCurrency: string | null | undefined,
  rows: Array<{ status: string; amountMinor: number; currency?: string } & ReverseTransferProbe>,
): ResolvedPayoutKpis {
  const inrRows = rows.filter((r) => isInr(r.currency))
  const nonInrExcludedCount = rows.length - inrRows.length
  const client = sumPayoutKpis(inrRows)

  let serverRejectedReason: ResolvedPayoutKpis['serverRejectedReason'] = 'absent'
  if (server) {
    const figures = [
      server.scored_count,
      server.processed_count,
      server.processed_amount_minor,
      server.review_count,
      server.review_amount_minor,
      server.failed_count,
      server.failed_amount_minor,
      server.total_amount_minor,
    ]
    if (!isInr(server.currency ?? summaryCurrency)) {
      serverRejectedReason = 'non_inr_currency'
    } else if (!figures.every(isMinorInt)) {
      serverRejectedReason = 'invalid_amounts'
    } else {
      return {
        source: 'server',
        currency: 'INR',
        nonInrExcludedCount,
        // Informational: server payout KPIs are payout-only, so refund-graph rows never enter them.
        pendingReverseTransferCount: client.pendingReverseTransferCount,
        scoredCount: server.scored_count,
        totalAmount: server.total_amount_minor,
        processedCount: server.processed_count,
        processedAmount: server.processed_amount_minor,
        reviewCount: server.review_count,
        reviewAmount: server.review_amount_minor,
        failedCount: server.failed_count,
        failedAmount: server.failed_amount_minor,
      }
    }
  }
  return { ...client, source: 'client', currency: 'INR', nonInrExcludedCount, serverRejectedReason }
}
