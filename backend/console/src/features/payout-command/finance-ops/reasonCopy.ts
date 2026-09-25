import { fmtInrFull, minorToRupees } from '@/features/payout-command/command-center/commandCenterFormat'
import type { FinanceException } from '@/services/payout-command/prod-api/financeTypes'
import { isPendingSellerReverseTransfer } from './payoutReconCopy'

/** Row label for reason `refund_without_reverse_transfer` (any case). */
export const PENDING_REVERSE_TRANSFER_LABEL = 'Seller reverse transfer pending'

/** Short explanation shown wherever a pending seller reverse transfer is listed. */
export const PENDING_REVERSE_TRANSFER_EXPLANATION =
  'Refund issued; marketplace reverse transfer from the seller not yet recorded. Not a cash gap.'

/** Neutral (non-danger, non-variance) chip styling for pending seller reverse transfers. */
export const PENDING_REVERSE_TRANSFER_TONE_CLASS = 'bg-[#EEF4FF] text-[#2B6CB0]'

/** Outcome-engine amount_minor is paise. */
export function formatPaise(minor: number | null | undefined, decimals: 0 | 2 = 0): string {
  const rupees = minorToRupees(minor)
  if (rupees == null) return '—'
  return fmtInrFull(rupees, { decimals })
}

export function formatPaiseCompact(minor: number | null | undefined): string {
  const rupees = minorToRupees(minor)
  if (rupees == null) return '—'
  if (Math.abs(rupees) >= 100_000) {
    const lakhs = rupees / 100_000
    const digits = lakhs >= 10 ? 1 : 2
    return `₹${lakhs.toFixed(digits)}L`
  }
  return fmtInrFull(rupees, { decimals: 0 })
}

const IST_DATE_FMT = new Intl.DateTimeFormat('en-CA', {
  timeZone: 'Asia/Kolkata',
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
})

/**
 * IST civil date (YYYY-MM-DD) for cash-schedule dates / as_of / roll dates.
 * Never shifts by a day with the browser timezone:
 *  - plain "YYYY-MM-DD" or an IST-offset timestamp ("…T00:00:00+05:30") → its own date prefix;
 *  - any other instant (Z / other offset / unix seconds) → formatted in Asia/Kolkata.
 * No UTC getters and no toISOString().slice(0, 10).
 */
export function istCivilDate(value: string | number | null | undefined): string {
  if (value == null || value === '') return '—'
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) return '—'
    const ms = value < 1e12 ? value * 1000 : value
    return IST_DATE_FMT.format(new Date(ms))
  }
  const s = String(value).trim()
  const prefix = /^(\d{4}-\d{2}-\d{2})/.exec(s)?.[1]
  // Explicit non-IST zone (Z / ±hh:mm other than +05:30) → convert the instant to IST.
  const zone = /(Z|[+-]\d{2}:?\d{2})$/i.exec(s)?.[1]
  const isIstOrNoZone = !zone || zone === '+05:30' || zone === '+0530'
  if (prefix && isIstOrNoZone) return prefix
  const ms = Date.parse(s)
  if (!Number.isFinite(ms)) return s
  return IST_DATE_FMT.format(new Date(ms))
}

export type ExceptionSeverity = 'HIGH' | 'MEDIUM' | 'LOW'

export function exceptionSeverity(
  ex: Pick<FinanceException, 'reason' | 'variance_amount'> & Partial<Pick<FinanceException, 'reason_code'>>,
): ExceptionSeverity {
  // Not a cash gap — never escalate on the refund amount.
  if (isPendingSellerReverseTransfer(ex)) return 'LOW'
  if (
    ex.reason === 'failed_with_bank_movement' ||
    ex.reason === 'payout_failed_with_bank_movement' ||
    ex.reason === 'amount_mismatch' ||
    ex.variance_amount >= 1_000_000
  ) {
    return 'HIGH'
  }
  if (ex.variance_amount >= 100_000 || ex.reason === 'shared_utr_or_bank_candidates') return 'MEDIUM'
  return 'LOW'
}

export function reasonTitle(reason: string): string {
  if (isPendingSellerReverseTransfer({ reason })) return PENDING_REVERSE_TRANSFER_LABEL
  switch (reason) {
    case 'failed_with_bank_movement':
      return 'Failed payment + money movement'
    case 'amount_mismatch':
      return 'Settlement-bank variance'
    case 'shared_utr_or_bank_candidates':
      return 'UTR conflict'
    case 'captured_missing_settlement':
      return 'Captured, missing settlement'
    case 'optimizer_settlement_unobserved':
      return 'Optimizer settlement unobserved'
    case 'ambiguous_bank_candidates':
      return 'Ambiguous bank candidates'
    case 'payout_missing_bank':
      return 'Processed, bank credit missing'
    case 'orphan_bank_credit':
      return 'Orphan bank credit'
    case 'open_status_no_downstream':
      return 'Open status, no downstream'
    case 'settlement_on_hold':
      return 'Settlement on hold'
    case 'awaiting_settlement_cycle':
      return 'Awaiting settlement cycle'
    default:
      return reason.replace(/_/g, ' ')
  }
}

export type SettlementPill = {
  label: string
  tone: 'processed' | 'pending' | 'missing' | 'review'
}

export function settlementPill(opts: {
  result?: string
  reason?: string
  bankProven?: boolean
}): SettlementPill {
  const reason = opts.reason ?? ''
  const result = (opts.result ?? '').toUpperCase()
  if (isPendingSellerReverseTransfer({ reason, result })) {
    return { label: 'Reverse transfer pending', tone: 'pending' }
  }
  if (reason === 'settlement_on_hold') return { label: 'Under Review', tone: 'review' }
  if (reason === 'awaiting_settlement_cycle') return { label: 'Pending', tone: 'pending' }
  if (
    reason === 'captured_missing_settlement' ||
    reason === 'optimizer_settlement_unobserved' ||
    reason === 'failed_with_bank_movement'
  ) {
    return { label: 'Missing', tone: 'missing' }
  }
  // Bank-proven close only — never paint two-way MATCHED as cash-in-bank.
  if (result === 'MATCHED' && opts.bankProven) return { label: 'Bank proven', tone: 'processed' }
  if (result === 'MATCHED') return { label: 'Books/PSP matched', tone: 'pending' }
  if (result === 'AMBIGUOUS') return { label: 'Pending', tone: 'pending' }
  return { label: 'Missing', tone: 'missing' }
}

export function reconLabel(result?: string): string {
  const r = (result ?? '').toUpperCase()
  if (!r) return 'Pending'
  return r
}
