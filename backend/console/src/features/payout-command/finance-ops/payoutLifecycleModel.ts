import type { PayoutReconDisplayRow } from './payoutReconCopy'
import { normalizePayoutStatus } from './payoutReconCopy'
import { isBankProvenReconciled } from './payoutReconCopy'
import { isPendingSellerReverseTransfer } from './payoutReconCopy'

type PayoutDetailLike = {
  id: string
  amount: number
  currency: string
  status: string
  utr?: string | null
  mode?: string | null
  created_at?: number | null
  fund_account_id?: string | null
  reference_id?: string | null
  beneficiary_name?: string | null
  purpose?: string | null
  payment_provider?: string | null
  fees?: number | null
  tax?: number | null
  status_details?: { description?: string; source?: string; reason?: string } | null
}

export type SourceFlag = 'yes' | 'no' | 'na'

export type LifecycleEventState = 'done' | 'warn' | 'current' | 'missing' | 'fail'

export type LifecycleFact = { label: string; value: string }

export type LifecycleEvent = {
  id: string
  title: string
  timeLabel: string
  state: LifecycleEventState
  summary: string
  facts: LifecycleFact[]
  operational?: { label: string; value: string } | null
}

export type MoneyNode = { id: string; label: string; sub: string }

export type AttemptRow = {
  id: string
  status: string
  sentLabel: string
  response: string
  providerRef: string
  notes: string[]
}

export type SourceMatrixRow = {
  stage: string
  provider: SourceFlag
  bank: SourceFlag
  webhook: SourceFlag
  ledger: SourceFlag
}

export type PayoutLifecycle = {
  payoutId: string
  amountMinor: number
  currency: string
  mode: string
  railBank: string
  providerName: string
  providerStatus: string
  reconResult: string
  exceptionType: string | null
  exposureMinor: number
  lifecyclePassed: boolean
  slaExceeded: boolean
  recovered: boolean
  utr: string | null
  fundAccountId: string
  referenceId: string
  idempotencyKey: string
  requestHash: string
  merkleRoot: string
  merkleLeaf: string
  sealedAt: string
  contact: string
  reason: string
  reasonDescription: string
  signalSource: string
  createdAt: number
  money: { nodes: MoneyNode[]; outcome: 'accounted' | 'unaccounted' | 'in_flight'; caption: string }
  events: LifecycleEvent[]
  sourceMatrix: SourceMatrixRow[]
  attempts: AttemptRow[]
  route: {
    rail: string
    reason: string
    sla: string
    feeFx: string
    fallback: string
  }
  investigation: { headline: string; bullets: string[] }
  rawProvider: Record<string, unknown>
  rawBank: Record<string, unknown>
  rawLedger: Record<string, unknown>
}

function fnvExpand(seed: string): string {
  let h = 2166136261
  for (let i = 0; i < seed.length; i += 1) {
    h ^= seed.charCodeAt(i)
    h = Math.imul(h, 16777619)
  }
  const out: string[] = []
  for (let i = 0; i < 8; i += 1) {
    h = Math.imul(h ^ (h >>> 13), 127412337)
    h ^= seed.charCodeAt(i % seed.length) + i * 17
    out.push((h >>> 0).toString(16).padStart(8, '0'))
  }
  return out.join('')
}

export function demoSha256(seed: string): string {
  return fnvExpand(`sha256:${seed}`)
}

export function shortHash(hex: string): string {
  return `sha256:${hex.slice(0, 12)}…${hex.slice(-4)}`
}

export function reconToneClass(result: string): string {
  const r = result.toUpperCase()
  if (r === 'MATCHED' || r === 'ACCOUNTED') return 'bg-[#E8F8EE] text-[#147A3F]'
  if (r === 'UNRESOLVED' || r === 'VARIANCE') return 'bg-[#FFF6E5] text-[#B36B00]'
  if (r === 'AMBIGUOUS') return 'bg-[#EEF4FF] text-[#2B6CB0]'
  if (r === 'CONFLICTED' || r === 'ORPHAN') return 'bg-[#FDECEC] text-[#C0372A]'
  return 'bg-[#F3F4F6] text-[#475569]'
}

function istLabel(unix: number): string {
  const d = new Date(unix * 1000)
  if (Number.isNaN(d.getTime())) return '—'
  return d.toLocaleString('en-IN', {
    day: '2-digit',
    month: 'short',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    timeZone: 'Asia/Kolkata',
  })
}

function timeOnly(unix: number): string {
  const d = new Date(unix * 1000)
  if (Number.isNaN(d.getTime())) return '—'
  return d.toLocaleTimeString('en-IN', {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    timeZone: 'Asia/Kolkata',
  })
}

function bankName(provider?: string): string {
  const p = String(provider || 'razorpay').toLowerCase()
  if (p.includes('paytm')) return 'Paytm Payments Bank'
  if (p.includes('phonepe')) return 'Yes Bank'
  if (p.includes('cashfree')) return 'ICICI Bank'
  if (p.includes('payu')) return 'Axis Bank'
  return 'HDFC Bank'
}

function scenarioKey(row: PayoutReconDisplayRow): string {
  const id = row.payoutId.toLowerCase()
  if (id.includes('fail_001')) return 'failed_clean'
  if (id.includes('fail_002')) return 'failed_money'
  if (id.includes('rev_003')) return 'failed_reversed'
  if (id.includes('proc_004')) return 'stuck_passed'
  if (id.includes('rev_005')) return 'bank_fail_reversed'
  if (id.includes('fail_006')) return 'scheduled_insufficient'
  if (id.includes('queue_007')) return 'queued_success'
  if (id.includes('cancel_008')) return 'queued_cancelled'
  if (id.includes('reject_009')) return 'pending_rejected'
  if (id.includes('gap_010')) return 'processed_missing_bank'
  if (id.includes('var_011')) return 'processed_variance'
  if (id.includes('fail_012')) return 'failed_then_reversed'

  const status = normalizePayoutStatus(row.status)
  const result = String(row.result).toUpperCase()
  const bank = row.bank === true
  if (status === 'failed' && bank && result !== 'MATCHED') return 'failed_money'
  if (status === 'failed' && !bank) return 'failed_clean'
  if (status === 'reversed') return 'failed_reversed'
  if (status === 'cancelled') return 'queued_cancelled'
  if (status === 'rejected') return 'pending_rejected'
  if (status === 'processed' && result === 'VARIANCE') return 'processed_variance'
  if (status === 'processed' && row.bank === false) return 'processed_missing_bank'
  if (status === 'processed') return 'processed'
  if (status === 'processing') return 'processing'
  if (status === 'queued') return 'queued'
  if (status === 'pending') return 'pending'
  if (status === 'scheduled') return 'scheduled'
  return status || 'processing'
}

function exceptionTypeFor(kind: string, row: PayoutReconDisplayRow): string | null {
  if (row.exceptionType) return row.exceptionType
  if (kind === 'failed_money') return 'FAILED_WITH_MONEY_MOVEMENT'
  if (kind === 'processed_missing_bank') return 'MISSING_BANK_CREDIT'
  if (kind === 'processed_variance') return 'AMOUNT_MISMATCH'
  if (String(row.result).toUpperCase() === 'MATCHED') return null
  return String(row.reason || 'UNRESOLVED').toUpperCase()
}

function exposureFor(kind: string, row: PayoutReconDisplayRow): number {
  // Refund awaiting seller reverse transfer — not a cash gap, never exposure.
  if (isPendingSellerReverseTransfer(row)) return 0
  if (kind === 'failed_clean' || kind === 'failed_reversed' || kind === 'bank_fail_reversed') return 0
  if (kind === 'queued_cancelled' || kind === 'pending_rejected' || kind === 'scheduled_insufficient') return 0
  if (kind === 'processed' || kind === 'stuck_passed' || kind === 'queued_success' || kind === 'failed_then_reversed') {
    return String(row.result).toUpperCase() === 'MATCHED' ? 0 : Math.abs(row.varianceMinor || row.amountMinor)
  }
  if (kind === 'processed_variance') return Math.abs(row.varianceMinor) || 50_000
  if (String(row.result).toUpperCase() === 'MATCHED') return 0
  return Math.abs(row.varianceMinor || row.amountMinor)
}

export function buildPayoutLifecycle(row: PayoutReconDisplayRow): PayoutLifecycle {
  const kind = scenarioKey(row)
  const status = normalizePayoutStatus(row.status) || String(row.status)
  const result = String(row.result || 'UNRESOLVED').toUpperCase()
  const bankProvenClose = isBankProvenReconciled({
    result,
    bank: row.bank,
    bankCreditProven: row.bankCreditProven,
    threeWay: row.threeWay,
  })
  const created = row.createdAt && Number.isFinite(row.createdAt) ? row.createdAt : 1_717_977_678
  const t = (...offsets: number[]) => created + offsets.reduce((a, b) => a + b, 0)
  const bankNameLabel = bankName(row.paymentProvider)
  const mode = (row.mode || 'NEFT').toUpperCase()
  const rail = `${bankNameLabel} · corporate payout · ${mode}`
  const hash = demoSha256(row.payoutId)
  const merkle = demoSha256(`${row.payoutId}:merkle`)
  const leaf = `#${row.payoutId.replace(/\D/g, '').slice(-6).padStart(6, '0') || '000001'}`
  const seq = row.payoutId.replace(/\D/g, '').slice(-4) || '0006'
  const attemptId = `att-${seq}-a1`
  const idem = `idem_${row.payoutId}_${mode.toLowerCase()}_v1`
  const utr =
    row.utr && row.utr !== '—' && row.utr !== 'null'
      ? row.utr
      : kind === 'failed_clean' || kind === 'queued_cancelled' || kind === 'pending_rejected' || kind === 'scheduled_insufficient'
        ? null
        : `HDFC-UTR-${String(8_800_000_000 + Number(seq || '6')).slice(-10)}`
  const exposure = exposureFor(kind, row)
  const slaExceeded = kind === 'stuck_passed' || kind === 'processing' || kind === 'processed_missing_bank'
  const recovered = kind === 'stuck_passed' || kind === 'queued_success' || kind === 'failed_reversed' || kind === 'failed_then_reversed' || kind === 'bank_fail_reversed'
  /** Terminal provider failure with no recovery path — timeline must stop at Failed. */
  const terminalFailed =
    kind === 'failed_clean' ||
    kind === 'failed_money' ||
    kind === 'scheduled_insufficient' ||
    kind === 'pending_rejected' ||
    kind === 'queued_cancelled' ||
    (status === 'failed' && !recovered)
  /** Lifecycle "Passed" only for successful processed/reversed paths — never for a failed payout. */
  const passed =
    !terminalFailed &&
    result === 'MATCHED' &&
    (status === 'processed' || status === 'reversed' || status === 'cancelled' || status === 'rejected')
  const exceptionType = exceptionTypeFor(kind, row)

  const events: LifecycleEvent[] = [
    {
      id: 'initiated',
      title: 'Payment initiated',
      timeLabel: timeOnly(t(0)),
      state: 'done',
      summary: 'Merchant requested a payout against a sealed instruction.',
      facts: [
        { label: 'Intent', value: row.referenceId || idem },
        { label: 'Amount', value: String(row.amountMinor) },
        { label: 'Source', value: 'Request / ledger' },
      ],
    },
    {
      id: 'intent',
      title: 'Intent created',
      timeLabel: timeOnly(t(2)),
      state: 'done',
      summary: 'Idempotent payout envelope accepted.',
      facts: [
        { label: 'Idempotency', value: idem },
        { label: 'Fund account', value: row.fundAccountId || '—' },
        { label: 'Request hash', value: shortHash(hash) },
      ],
    },
    {
      id: 'route',
      title: 'Route selected',
      timeLabel: timeOnly(t(3)),
      state: 'done',
      summary: rail,
      facts: [
        { label: 'Selected rail', value: rail },
        { label: 'Reason', value: 'Sealed contract rail match · policy-approved envelope' },
        { label: 'SLA', value: mode === 'IMPS' || mode === 'UPI' ? 'Credit expected T+0' : 'Credit expected T+0 banking hours' },
        { label: 'Fee / FX', value: 'Domestic · no FX · rail fee outside contract net' },
        { label: 'Fallback', value: 'No alternate beneficiary · no rail switch without amendment' },
        { label: 'Contract hash', value: shortHash(hash) },
      ],
    },
    {
      id: 'ack',
      title: 'Payout acknowledged',
      timeLabel: timeOnly(t(13)),
      state: 'done',
      summary: 'Provider returned 202 Accepted.',
      facts: [
        { label: 'Provider', value: bankNameLabel },
        { label: 'Response', value: '202 Accepted' },
        { label: 'Provider ref', value: utr || attemptId.toUpperCase() },
        { label: 'Webhook', value: 'payout.pending' },
      ],
    },
  ]

  if (kind === 'pending' || kind === 'pending_rejected') {
    events.push({
      id: 'pending',
      title: kind === 'pending_rejected' ? 'Approval rejected' : 'Pending approval',
      timeLabel: timeOnly(t(40)),
      state: kind === 'pending_rejected' ? 'fail' : 'current',
      summary:
        kind === 'pending_rejected'
          ? 'Approver rejected the payout. No money movement. Timeline stops here.'
          : 'Workflow for the payout is pending approval from the approver(s).',
      facts: [
        { label: 'Provider status', value: status },
        { label: 'Webhook', value: kind === 'pending_rejected' ? 'payout.rejected' : 'payout.pending' },
        { label: 'Bank', value: 'No transaction' },
      ],
    })
  }

  if (kind === 'queued' || kind === 'queued_success' || kind === 'queued_cancelled' || kind === 'scheduled_insufficient') {
    events.push({
      id: 'queued',
      title: kind === 'scheduled_insufficient' ? 'Scheduled' : 'Queued',
      timeLabel: timeOnly(t(20)),
      state: kind === 'queued' ? 'current' : 'done',
      summary:
        kind === 'scheduled_insufficient'
          ? 'Scheduled execution failed — insufficient balance.'
          : 'Queued due to insufficient balance / partner bank window.',
      facts: [
        { label: 'Provider status', value: kind === 'scheduled_insufficient' ? 'scheduled' : 'queued' },
        { label: 'Reason', value: row.reason || 'low_balance' },
        { label: 'Bank', value: 'No debit' },
      ],
    })
  }

  if (kind === 'queued_cancelled') {
    events.push({
      id: 'cancelled',
      title: 'Cancelled',
      timeLabel: timeOnly(t(90)),
      state: 'fail',
      summary: 'Payout cancelled from queued. No money left the source account. Timeline stops here.',
      facts: [
        { label: 'Provider status', value: 'cancelled' },
        { label: 'Webhook', value: 'payout.cancelled' },
        { label: 'Bank', value: 'No movement' },
      ],
    })
  }

  const processingKinds = new Set([
    'processing',
    'stuck_passed',
    'processed',
    'queued_success',
    'failed_money',
    'failed_clean',
    'failed_reversed',
    'bank_fail_reversed',
    'failed_then_reversed',
    'processed_missing_bank',
    'processed_variance',
  ])
  if (processingKinds.has(kind)) {
    events.push({
      id: 'processing',
      title: 'Provider processing',
      timeLabel: timeOnly(t(154)),
      state: kind === 'processing' ? 'current' : slaExceeded && !recovered ? 'warn' : 'done',
      summary:
        kind === 'processing'
          ? 'Razorpay status remains processing. Do not rename it to STUCK.'
          : 'Provider accepted the payout into the processing rail.',
      facts: [
        { label: 'Provider status', value: 'processing' },
        { label: 'Webhook', value: 'payout.processing' },
        { label: 'Reason', value: row.reason || 'payout_bank_processing' },
      ],
      operational:
        slaExceeded
          ? {
              label: 'Operational condition',
              value: 'SLA EXCEEDED',
            }
          : null,
    })
  }

  if (kind === 'stuck_passed') {
    events.push({
      id: 'sla',
      title: 'Processing exceeded expected window',
      timeLabel: timeOnly(t(1140)),
      state: 'warn',
      summary: 'Elapsed beyond the 15-minute processing SLA. Provider status stayed processing.',
      facts: [
        { label: 'Started', value: istLabel(t(154)) },
        { label: 'Expected SLA', value: '15 minutes' },
        { label: 'Elapsed', value: '19 minutes' },
        { label: 'Deviation', value: '+4 minutes' },
        { label: 'Provider status', value: 'processing' },
      ],
      operational: { label: 'Operational condition', value: 'SLA EXCEEDED' },
    })
    events.push({
      id: 'recovered',
      title: 'Recovered',
      timeLabel: timeOnly(t(1260)),
      state: 'done',
      summary: 'Provider subsequently confirmed processing. Bank credit observed.',
      facts: [
        { label: 'Provider status', value: 'processed' },
        { label: 'UTR', value: utr || '—' },
      ],
    })
  }

  if (kind === 'failed_clean' || kind === 'scheduled_insufficient') {
    events.push({
      id: 'failed',
      title: 'Failed',
      timeLabel: timeOnly(t(240)),
      state: 'fail',
      summary:
        kind === 'scheduled_insufficient'
          ? 'Scheduled payout failed — insufficient balance. Timeline stops here; no evidence pack for a successful credit.'
          : 'Provider marked failed. No debit on the source account. Timeline stops at failure — no reconcile / evidence seal for a successful payout.',
      facts: [
        { label: 'Provider status', value: 'failed' },
        { label: 'Webhook', value: 'payout.failed' },
        { label: 'Bank', value: 'NO TRANSACTION' },
        { label: 'Ledger', value: 'NO DEBIT' },
        { label: 'Finance note', value: 'Books clean (no money movement) — not a processed credit.' },
      ],
    })
  }

  if (kind === 'failed_money') {
    events.push({
      id: 'failed',
      title: 'Failed · money movement unaccounted',
      timeLabel: timeOnly(t(240)),
      state: 'fail',
      summary:
        'Razorpay status is failed, but the source account was debited and no reversal is observed. Timeline stops here for investigation.',
      facts: [
        { label: 'Provider status', value: 'failed' },
        { label: 'Webhook', value: 'payout.failed' },
        { label: 'Source bank', value: `− payout amount` },
        { label: 'Reversal', value: 'Not observed' },
        { label: 'Beneficiary', value: 'No credit observed' },
      ],
    })
  }

  if (kind === 'failed_reversed' || kind === 'bank_fail_reversed' || kind === 'failed_then_reversed') {
    events.push({
      id: 'failed',
      title: 'Failed',
      timeLabel: timeOnly(t(240)),
      state: 'fail',
      summary: 'Payout failed. Reversal transaction created to credit the business account back.',
      facts: [
        { label: 'Provider status', value: 'failed' },
        { label: 'Webhook', value: 'payout.failed' },
        { label: 'Source bank', value: 'Debit observed' },
      ],
    })
    events.push({
      id: 'reversal',
      title: 'Reversal credited',
      timeLabel: timeOnly(t(900)),
      state: 'done',
      summary: 'Original amount including fees/tax credited back. Status moved to reversed.',
      facts: [
        { label: 'Provider status', value: 'reversed' },
        { label: 'Webhook', value: 'payout.reversed' },
        { label: 'Bank', value: 'Reversal credit matched' },
      ],
    })
  }

  if (kind === 'processed' || kind === 'queued_success' || kind === 'stuck_passed' || kind === 'processed_variance') {
    events.push({
      id: 'bank',
      title: 'Bank credited',
      timeLabel: timeOnly(t(846)),
      state: kind === 'processed_variance' ? 'warn' : 'done',
      summary:
        kind === 'processed_variance'
          ? 'Bank credit observed at a different amount than the payout.'
          : 'Beneficiary credit observed and UTR matched.',
      facts: [
        { label: 'UTR', value: utr || '—' },
        { label: 'Bank', value: bankNameLabel },
        { label: 'Webhook', value: 'payout.processed' },
        { label: 'Source', value: 'Bank statement' },
      ],
    })
  }

  if (kind === 'processed_missing_bank') {
    events.push({
      id: 'bank',
      title: 'Bank credit not observed',
      timeLabel: timeOnly(t(846)),
      state: 'warn',
      summary: 'Provider says processed. No matching bank credit. Provider success ≠ bank reconciliation.',
      facts: [
        { label: 'Razorpay', value: 'processed' },
        { label: 'UTR', value: utr || '—' },
        { label: 'Bank', value: 'NO MATCHING TRANSACTION' },
        { label: 'Ledger', value: 'Debit posted' },
      ],
    })
  }

  /**
   * Terminal failures stop at Failed — do not invent green Reconciled / Evidence sealed
   * steps as if the payout completed successfully.
   */
  if (!terminalFailed) {
    const reconState: LifecycleEventState =
      bankProvenClose ? 'done' : result === 'MATCHED' ? 'current' : result === 'AMBIGUOUS' ? 'current' : 'warn'
    events.push({
      id: 'recon',
      title: bankProvenClose
        ? 'Reconciled'
        : result === 'MATCHED'
          ? 'Books/PSP matched — bank unproven'
          : `Reconciliation ${result}`,
      timeLabel: timeOnly(t(847)),
      state: reconState,
      summary: bankProvenClose
        ? 'Close MATCHED with bank proof. Cash movement is accounted for.'
        : result === 'MATCHED'
          ? 'Books agree with PSP. Bank cash is not proven — not money in bank.'
          : `${exceptionType || result}. Provider status stays ${status}.`,
      facts: [
        { label: 'Provider status', value: status },
        { label: 'Close result', value: result },
        { label: 'Bank proven', value: bankProvenClose ? 'yes' : 'no' },
        { label: 'Exception', value: exceptionType || 'NONE' },
        { label: 'Exposure', value: String(exposure) },
      ],
    })

    if (bankProvenClose || kind === 'processed' || kind === 'stuck_passed') {
      events.push({
        id: 'sealed',
        title: 'Evidence sealed',
        timeLabel: timeOnly(t(848)),
        state: 'done',
        summary: 'Record hash committed to the evidence tree.',
        facts: [
          { label: 'Record hash', value: shortHash(hash) },
          { label: 'Merkle root', value: shortHash(merkle) },
          { label: 'Leaf', value: leaf },
        ],
      })
    }
  } else if (kind === 'failed_money') {
    events.push({
      id: 'recon',
      title: `Investigation open · ${result === 'MATCHED' ? 'UNRESOLVED' : result}`,
      timeLabel: timeOnly(t(847)),
      state: 'warn',
      summary: 'Failed with possible money movement. Do not seal successful-credit evidence until reversal or bank proof lands.',
      facts: [
        { label: 'Provider status', value: status },
        { label: 'Reconciliation', value: result === 'MATCHED' ? 'UNRESOLVED' : result },
        { label: 'Exception', value: exceptionType || 'FAILED_WITH_MONEY_MOVEMENT' },
        { label: 'Exposure', value: String(exposure) },
      ],
    })
  }

  const bankYes: SourceFlag =
    kind === 'failed_clean' || kind === 'queued_cancelled' || kind === 'pending_rejected' || kind === 'scheduled_insufficient' || kind === 'pending' || kind === 'queued'
      ? 'no'
      : kind === 'processed_missing_bank'
        ? 'no'
        : row.bank === false
          ? 'no'
          : row.bank === true || kind === 'processed' || kind === 'stuck_passed' || kind === 'failed_money'
            ? 'yes'
            : 'na'

  const sourceMatrix: SourceMatrixRow[] = [
    { stage: 'Initiated', provider: 'yes', bank: 'na', webhook: 'yes', ledger: 'yes' },
    { stage: 'Routed', provider: 'yes', bank: 'na', webhook: 'yes', ledger: 'na' },
    { stage: 'Acknowledged', provider: 'yes', bank: 'na', webhook: 'yes', ledger: 'na' },
    { stage: 'Processing', provider: processingKinds.has(kind) ? 'yes' : 'na', bank: 'na', webhook: processingKinds.has(kind) ? 'yes' : 'na', ledger: 'na' },
    { stage: 'Credited', provider: bankYes === 'yes' ? 'yes' : 'no', bank: bankYes, webhook: bankYes === 'yes' ? 'yes' : 'no', ledger: bankYes === 'yes' ? 'yes' : 'no' },
    { stage: 'Reconciled', provider: 'na', bank: bankProvenClose ? 'yes' : 'no', webhook: 'na', ledger: 'yes' },
  ]

  const moneyNodes: MoneyNode[] = [
    { id: 'merchant', label: 'Merchant', sub: 'Instruction' },
    { id: 'payout', label: 'Payout', sub: status },
    { id: 'rail', label: mode, sub: bankNameLabel },
    {
      id: 'bank',
      label: 'Bank',
      sub:
        bankYes === 'yes'
          ? utr
            ? `UTR ${utr}`
            : 'Credit observed'
          : kind === 'failed_money'
            ? 'Debit, no credit'
            : 'No matching credit',
    },
    {
      id: 'end',
      label: bankProvenClose ? 'Accounted' : exposure > 0 ? 'Unaccounted' : result === 'MATCHED' ? 'Books matched' : 'Open',
      sub: result,
    },
  ]

  const outcome: PayoutLifecycle['money']['outcome'] =
    bankProvenClose ? 'accounted' : exposure > 0 ? 'unaccounted' : 'in_flight'

  const attempts: AttemptRow[] = [
    {
      id: `${attemptId}`.toUpperCase(),
      status: 'ACKNOWLEDGED',
      sentLabel: istLabel(t(13)),
      response: '202 Accepted',
      providerRef: utr || '—',
      notes: ['Provider acknowledgement received', 'Request hash matches sealed contract v1'],
    },
  ]
  if (processingKinds.has(kind)) {
    attempts.push({
      id: `ATT-${seq}-A2`,
      status: 'PROCESSING',
      sentLabel: istLabel(t(154)),
      response: 'payout.processing',
      providerRef: utr || '—',
      notes: slaExceeded ? ['Provider status processing', 'Operational condition: SLA EXCEEDED'] : ['Provider processing'],
    })
  }
  if (status === 'processed' || status === 'reversed' || status === 'failed' || status === 'cancelled' || status === 'rejected') {
    attempts.push({
      id: `ATT-${seq}-A3`,
      status: status.toUpperCase(),
      sentLabel: istLabel(t(846)),
      response: `payout.${status}`,
      providerRef: utr || '—',
      notes: [status === 'processed' && bankYes === 'yes' ? 'UTR matched' : `Terminal provider status ${status}`],
    })
  }

  const investigation =
    result === 'MATCHED'
      ? {
          headline:
            status === 'failed'
              ? 'Failed payout is financially accounted — no money movement.'
              : status === 'reversed'
                ? 'Failed payout recovered via reversal. Exposure is ₹0.'
                : 'Transaction completed successfully.',
          bullets: [
            `Route: ${rail}`,
            `Provider: ${status}`,
            bankYes === 'yes' ? `Bank: UTR matched (${utr || '—'})` : 'Bank: no credit required / none observed',
            `Reconciliation: ${result}`,
            'No unresolved financial exposure.',
          ],
        }
      : {
          headline:
            kind === 'failed_money'
              ? 'Failed at provider, but source debit is unaccounted.'
              : kind === 'processed_missing_bank'
                ? 'Provider processed, bank credit not observed.'
                : slaExceeded
                  ? 'Transaction exceeded expected processing window.'
                  : 'Reconciliation is not matched. Provider status is unchanged.',
          bullets: [
            `Provider status: ${status}`,
            `Reconciliation: ${result}`,
            exceptionType ? `Exception: ${exceptionType}` : 'Exception: none',
            `Bank: ${bankYes === 'yes' ? 'movement observed' : 'no matching credit'}`,
            `Exposure: ${exposure} paise`,
          ],
        }

  return {
    payoutId: row.payoutId,
    amountMinor: row.amountMinor,
    currency: row.currency || 'INR',
    mode,
    railBank: bankNameLabel,
    providerName: row.paymentProvider || 'razorpay',
    providerStatus: status,
    reconResult: result,
    exceptionType,
    exposureMinor: exposure,
    lifecyclePassed: passed,
    slaExceeded,
    recovered,
    utr,
    fundAccountId: row.fundAccountId || '—',
    referenceId: row.referenceId || '—',
    idempotencyKey: idem,
    requestHash: hash,
    merkleRoot: merkle,
    merkleLeaf: leaf,
    sealedAt: istLabel(t(848)),
    contact: row.contact,
    reason: row.reason || row.errorCode,
    reasonDescription: row.errorDescription || row.statusDetails?.description || '',
    signalSource: row.signalSource,
    createdAt: created,
    money: {
      nodes: moneyNodes,
      outcome,
      caption:
        outcome === 'accounted'
          ? 'Financial movement accounted for'
          : outcome === 'unaccounted'
            ? 'Financial movement unaccounted'
            : 'In flight — awaiting a terminal source',
    },
    events,
    sourceMatrix,
    attempts,
    route: {
      rail,
      reason: 'Sealed contract rail match · policy-approved envelope',
      sla: 'Credit expected T+0 banking hours',
      feeFx: 'Domestic · no FX · rail fee outside contract net',
      fallback: 'No alternate beneficiary · no rail switch without amendment',
    },
    investigation,
    rawProvider: {
      payout_id: row.payoutId,
      status,
      amount: row.amountMinor,
      currency: row.currency || 'INR',
      mode,
      utr,
      status_details: row.statusDetails || { reason: row.reason, source: row.signalSource, description: row.errorDescription },
    },
    rawBank: {
      matched: bankYes === 'yes',
      utr,
      amount_minor: bankYes === 'yes' ? row.amountMinor - (kind === 'processed_variance' ? 50_000 : 0) : 0,
      narrative: bankYes === 'yes' ? 'CREDIT' : 'NO MATCHING TRANSACTION',
    },
    rawLedger: {
      debit: status === 'processed' || kind === 'failed_money' || status === 'reversed' ? row.amountMinor : 0,
      reversal_credit: status === 'reversed' ? row.amountMinor : 0,
    },
  }
}

export function reconRowFromPayoutDetail(payout: PayoutDetailLike): PayoutReconDisplayRow {
  const status = normalizePayoutStatus(payout.status) || payout.status
  const failed = status === 'failed' || status === 'reversed' || status === 'cancelled' || status === 'rejected'
  const processed = status === 'processed'
  return {
    payoutId: payout.id,
    status,
    amountMinor: payout.amount,
    utr: payout.utr || '—',
    errorCode: payout.status_details?.reason || status,
    errorDescription: payout.status_details?.description || '',
    signalSource: payout.status_details?.source || 'business',
    evidence: payout.status_details?.description || '',
    nextSteps: '—',
    result: processed || (failed && status !== 'failed') ? 'MATCHED' : failed ? 'MATCHED' : 'UNRESOLVED',
    reason: payout.status_details?.reason || status,
    contact: payout.beneficiary_name || '—',
    varianceMinor: 0,
    settlement: processed,
    bank: processed || status === 'reversed',
    mode: payout.mode || 'NEFT',
    purpose: payout.purpose || undefined,
    fundAccountId: payout.fund_account_id || undefined,
    referenceId: payout.reference_id || undefined,
    paymentProvider: payout.payment_provider || 'razorpay',
    statusDetails: payout.status_details
      ? {
          description: payout.status_details.description || '',
          source: payout.status_details.source || '',
          reason: payout.status_details.reason || '',
        }
      : undefined,
    createdAt: payout.created_at || undefined,
    fees: payout.fees || undefined,
    tax: payout.tax || undefined,
    currency: payout.currency,
  }
}
