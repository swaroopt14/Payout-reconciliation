'use client'

import { formatJournalMoney } from '../intent-journal/formatJournalMoney'
import type { JournalIntentRow } from '@/services/payout-command/prod-api/mapIntentEngineBatch'
import {
  mapIntentRowToPayoutStatus,
  payoutStatusTone,
  type RazorpayPayoutStatus,
} from './razorpayPayoutStatus'
import { CopyIdButton, DrawerCloseButton, StatusBadge } from './razorpayChrome'
import { PaymentProviderBadge } from './PaymentProviderBadge'
import { ErrorInvestigationPanel } from './ErrorInvestigationPanel'
import { buildRazorpayXError } from './razorpayXErrors'
import { PayoutLifecycleView } from './PayoutLifecycleView'
import { buildPayoutLifecycle, reconRowFromPayoutDetail } from './payoutLifecycleModel'
import { useFinanceTimeline } from './FinanceTimelineLadder'

export type RazorpayPayoutDetail = {
  id: string
  entity: 'payout'
  fund_account_id?: string | null
  amount: number
  currency: string
  fees?: number | null
  tax?: number | null
  status: string
  utr?: string | null
  mode?: string | null
  created_at?: number | null
  fee_type?: string | null
  notes?: Record<string, string> | null
  status_details?: {
    description?: string
    source?: string
    reason?: string
  } | null
  reference_id?: string | null
  beneficiary_name?: string | null
  purpose?: string | null
  payment_provider?: string | null
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[132px_1fr] gap-3 border-b border-[#F1F5F9] py-2.5 text-[13px]">
      <dt className="text-[#64748B]">{label}</dt>
      <dd className="min-w-0 break-words font-medium text-[#0F172A]">{children || '—'}</dd>
    </div>
  )
}

function formatUnix(ts?: number | null): string {
  if (ts == null || !Number.isFinite(ts)) return '—'
  const d = new Date(ts * 1000)
  if (Number.isNaN(d.getTime())) return '—'
  return d.toLocaleString('en-IN', {
    day: '2-digit',
    month: 'short',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}

/** Build a Razorpay-shaped payout payload from a journal row (+ optional upstream fields). */
export function payoutDetailFromIntentRow(row: JournalIntentRow): RazorpayPayoutDetail {
  const status = mapIntentRowToPayoutStatus(row)
  const fromRaw = (row.rawIntent ?? {}) as Record<string, unknown>
  const payoutId = (() => {
    if (typeof fromRaw.payout_id === 'string' && fromRaw.payout_id.trim()) return fromRaw.payout_id.trim()
    if (typeof fromRaw.id === 'string' && fromRaw.id.startsWith('pout_')) return fromRaw.id
    if (row.reference?.startsWith('pout_')) return row.reference
    if (row.requestId.startsWith('pout_')) return row.requestId
    return `pout_${String(row.sourceRowNum ?? 1).padStart(14, '0')}`
  })()

  const amountMajor = Number.isFinite(row.amount) ? row.amount : 0
  const amountPaise =
    typeof fromRaw.amount_paise === 'number' && Number.isFinite(fromRaw.amount_paise)
      ? fromRaw.amount_paise
      : Math.round(amountMajor * 100)

  const notes =
    fromRaw.notes && typeof fromRaw.notes === 'object' && !Array.isArray(fromRaw.notes)
      ? (fromRaw.notes as Record<string, string>)
      : {
          notes_key_1: row.beneficiaryName || 'Payout',
          notes_key_2: row.clientBatchRef || row.batchId,
        }

  const statusDetails =
    fromRaw.status_details && typeof fromRaw.status_details === 'object'
      ? (fromRaw.status_details as RazorpayPayoutDetail['status_details'])
      : null

  return {
    id: payoutId,
    entity: 'payout',
    fund_account_id:
      (typeof fromRaw.fund_account_id === 'string' && fromRaw.fund_account_id) ||
      `fa_${String(row.sourceRowNum ?? 1).padStart(14, '0')}`,
    amount: amountPaise,
    currency: row.currency || 'INR',
    fees: typeof fromRaw.fees === 'number' ? fromRaw.fees : status === 'processed' ? Math.round(amountPaise * 0.002) : 0,
    tax: typeof fromRaw.tax === 'number' ? fromRaw.tax : status === 'processed' ? Math.round(amountPaise * 0.00036) : 0,
    status,
    utr: typeof fromRaw.utr === 'string' ? fromRaw.utr : null,
    mode:
      (typeof fromRaw.mode === 'string' && fromRaw.mode) ||
      row.paymentMethodDetail ||
      row.rail ||
      'NEFT',
    created_at:
      typeof fromRaw.created_at === 'number'
        ? fromRaw.created_at
        : Math.floor(Date.parse(row.intendedExecutionAt || '') / 1000) || undefined,
    fee_type: fromRaw.fee_type == null ? null : String(fromRaw.fee_type),
    notes,
    status_details: statusDetails,
    reference_id: row.reference || null,
    beneficiary_name: row.beneficiaryName || null,
    purpose: typeof fromRaw.purpose === 'string' ? fromRaw.purpose : 'payout',
    payment_provider:
      (typeof fromRaw.payment_provider === 'string' && fromRaw.payment_provider) ||
      (typeof fromRaw.provider_hint === 'string' && fromRaw.provider_hint) ||
      row.paymentPartner ||
      row.provider ||
      'razorpay',
  }
}

export function PayoutDetailDrawer({
  row,
  onClose,
}: {
  row: JournalIntentRow
  onClose: () => void
}) {
  const payout = payoutDetailFromIntentRow(row)
  const status = payout.status as RazorpayPayoutStatus
  const amountMajor = payout.amount / 100
  const { timeline, loading: timelineLoading } = useFinanceTimeline(
    payout.id.startsWith('pay_') ? 'payments' : 'payouts',
    payout.id,
  )

  return (
    <aside
      className="fixed inset-y-0 right-0 z-40 flex w-full max-w-[480px] flex-col border-l border-[#E6E8EB] bg-white shadow-[-12px_0_32px_rgba(26,26,26,0.08)]"
      aria-label="Payout Details"
    >
      <div className="flex items-start justify-between gap-3 border-b border-[#E6E8EB] px-5 py-4">
        <div className="min-w-0">
          <p className="text-[16px] font-semibold tracking-[-0.02em] text-[#1A1A1A]">Payout Details</p>
          <div className="mt-1 flex min-w-0 items-center gap-1">
            <p className="truncate font-mono text-[13px] text-[#6B6B6B]">{payout.id}</p>
            <CopyIdButton value={payout.id} />
          </div>
        </div>
        <DrawerCloseButton onClick={onClose} label="Close payout details" />
      </div>

      <div className="flex items-end justify-between gap-3 px-5 pt-5">
        <div>
          <p className="text-[11px] font-medium text-[#8F8F8F]">Amount</p>
          <p className="mt-1 text-[28px] font-semibold leading-none tracking-[-0.03em] tabular-nums text-[#1A1A1A]">
            {formatJournalMoney(amountMajor, payout.currency)}
          </p>
        </div>
        <StatusBadge tone={payoutStatusTone(status)}>{payout.status}</StatusBadge>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
        <div className="mb-4">
          <PaymentProviderBadge provider={payout.payment_provider} size="md" />
        </div>

        <section>
          <h3 className="mb-1 text-[11px] font-semibold uppercase tracking-[0.08em] text-[#94A3B8]">
            Payout details
          </h3>
          <dl>
            <Field label="Entity">{payout.entity}</Field>
            <Field label="Processor">
              <PaymentProviderBadge provider={payout.payment_provider} />
            </Field>
            <Field label="Fund account">
              <span className="font-mono text-[12px]">{payout.fund_account_id}</span>
            </Field>
            <Field label="Amount">{formatJournalMoney(amountMajor, payout.currency)}</Field>
            <Field label="Currency">{payout.currency}</Field>
            <Field label="Status">{payout.status}</Field>
            <Field label="Mode">{payout.mode}</Field>
            <Field label="UTR">{payout.utr || '—'}</Field>
            <Field label="Fees">
              {payout.fees != null ? formatJournalMoney((payout.fees || 0) / 100, payout.currency) : '—'}
            </Field>
            <Field label="Tax">
              {payout.tax != null ? formatJournalMoney((payout.tax || 0) / 100, payout.currency) : '—'}
            </Field>
            <Field label="Fee type">{payout.fee_type ?? 'null'}</Field>
            <Field label="Purpose">{payout.purpose || '—'}</Field>
            <Field label="Reference">{payout.reference_id || '—'}</Field>
            <Field label="Recipient">{payout.beneficiary_name || '—'}</Field>
            <Field label="Created at">{formatUnix(payout.created_at)}</Field>
          </dl>
        </section>

        {payout.status_details ? (
          <section className="mt-5">
            <h3 className="mb-1 text-[11px] font-semibold uppercase tracking-[0.08em] text-[#94A3B8]">
              Status details
            </h3>
            <dl>
              <Field label="Reason">{payout.status_details.reason || '—'}</Field>
              <Field label="Source">{payout.status_details.source || '—'}</Field>
              <Field label="Description">{payout.status_details.description || '—'}</Field>
            </dl>
          </section>
        ) : null}

        <section className="mt-5 border-t border-[#E2E8F0] pt-5">
          <PayoutLifecycleView
            life={buildPayoutLifecycle(reconRowFromPayoutDetail(payout))}
            variant="drawer"
            initialTab="events"
            traceHref={`/reconciliation/${encodeURIComponent(payout.id)}`}
            capturedTimeline={timeline}
            timelineLoading={timelineLoading}
          />
        </section>

        <ErrorInvestigationPanel
          errorView={buildRazorpayXError({
            reason: payout.status_details?.reason,
            status: payout.status,
            description: payout.status_details?.description,
            source: payout.status_details?.source,
            payoutId: payout.id,
            fundAccountId: payout.fund_account_id,
            field: payout.status_details?.reason?.includes('account') ? 'account_number' : null,
          })}
          financialImpactMinor={payout.amount}
          confidence={undefined}
          autoStart={
            String(payout.status || '').toLowerCase() === 'failed' ||
            String(payout.status || '').toLowerCase() === 'reversed'
          }
        />

        <section className="mt-5">
          <h3 className="mb-1 text-[11px] font-semibold uppercase tracking-[0.08em] text-[#94A3B8]">Notes</h3>
          <dl>
            {payout.notes && Object.keys(payout.notes).length > 0 ? (
              Object.entries(payout.notes).map(([key, value]) => (
                <Field key={key} label={key}>
                  {value}
                </Field>
              ))
            ) : (
              <p className="py-2 text-[13px] text-[#94A3B8]">No notes</p>
            )}
          </dl>
        </section>

        <section className="mt-5 rounded-[8px] border border-[#EEF0F3] bg-[#FAFBFC] p-3">
          <p className="text-[11px] font-semibold uppercase tracking-[0.06em] text-[#94A3B8]">Raw entity</p>
          <pre className="mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-all font-mono text-[11px] text-[#334155]">
            {JSON.stringify(
              {
                id: payout.id,
                entity: payout.entity,
                fund_account_id: payout.fund_account_id,
                amount: payout.amount,
                notes: payout.notes,
                fees: payout.fees,
                tax: payout.tax,
                status: payout.status,
                utr: payout.utr,
                mode: payout.mode,
                created_at: payout.created_at,
                fee_type: payout.fee_type,
                payment_provider: payout.payment_provider,
              },
              null,
              2,
            )}
          </pre>
        </section>
      </div>
    </aside>
  )
}
