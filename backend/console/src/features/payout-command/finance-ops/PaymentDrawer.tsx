'use client'

import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  createFinanceInvestigation,
  getFinancePayment,
  getFinancePayout,
  getFinanceRefunds,
  getFinanceSettlements,
} from '@/services/payout-command/prod-api/financeApi'
import type {
  FinanceException,
  FinanceInvestigation,
  FinancePayment,
  FinancePayout,
  FinanceRefund,
  FinanceSettlementLine,
} from '@/services/payout-command/prod-api/financeTypes'
import { formatPaise, reconLabel, reasonTitle } from './reasonCopy'
import { ErrorInvestigationPanel } from './ErrorInvestigationPanel'
import { buildRazorpayXError } from './razorpayXErrors'
import { FinanceTimelineLadder, useFinanceTimeline } from './FinanceTimelineLadder'
import {
  CopyIdButton,
  DrawerCloseButton,
  DrawerField,
  StatusBadge,
  UnderlineTabs,
} from './razorpayChrome'
import { payoutStatusTone, type RazorpayPayoutStatus } from './razorpayPayoutStatus'
import { PaymentProviderBadge } from './PaymentProviderBadge'

function asPayoutStatus(status?: string | null): RazorpayPayoutStatus {
  const s = String(status || '').toLowerCase()
  if (
    s === 'pending' ||
    s === 'scheduled' ||
    s === 'queued' ||
    s === 'processing' ||
    s === 'processed' ||
    s === 'reversed' ||
    s === 'cancelled' ||
    s === 'rejected' ||
    s === 'failed'
  ) {
    return s
  }
  if (s === 'captured' || s === 'settled') return 'processed'
  if (s === 'authorized' || s === 'created') return 'pending'
  return 'processing'
}

function formatWhen(value?: string | null): string {
  if (!value) return '—'
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return value.replace('T', ' ').replace('Z', ' UTC')
  return d.toLocaleString('en-IN', {
    day: '2-digit',
    month: 'short',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}

function drawerTitle(entityType?: string) {
  const t = String(entityType || '').toLowerCase()
  if (t === 'payment') return 'Payment Details'
  if (t === 'settlement') return 'Settlement Details'
  if (t === 'bank') return 'Bank credit'
  return 'Payout Details'
}

export function PaymentDrawer({
  entityId,
  exceptionId,
  exception,
  onClose,
}: {
  entityId: string
  exceptionId?: string
  exception?: FinanceException | null
  onClose: () => void
}) {
  const [payment, setPayment] = useState<FinancePayment | null>(null)
  const [payout, setPayout] = useState<FinancePayout | null>(null)
  const [refunds, setRefunds] = useState<FinanceRefund[]>([])
  const [settlements, setSettlements] = useState<FinanceSettlementLine[]>([])
  const [investigation, setInvestigation] = useState<FinanceInvestigation | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [investigating, setInvestigating] = useState(false)
  const [tab, setTab] = useState<'details' | 'timeline'>('details')
  const timelineKind =
    exception?.entity_type === 'payout' || entityId.startsWith('pout_') ? 'payouts' : 'payments'
  const { timeline, loading: timelineLoading } = useFinanceTimeline(timelineKind, entityId)

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError(null)
    setInvestigation(null)
    setPayment(null)
    setPayout(null)

    async function load() {
      const preferPayout = exception?.entity_type === 'payout' || entityId.startsWith('pout_')
      const first = preferPayout ? await getFinancePayout(entityId) : await getFinancePayment(entityId)
      if (cancelled) return
      if (first.ok && first.data) {
        if (preferPayout) setPayout(first.data as FinancePayout)
        else setPayment(first.data as FinancePayment)
      } else if (first.status === 404) {
        const second = preferPayout ? await getFinancePayment(entityId) : await getFinancePayout(entityId)
        if (cancelled) return
        if (second.ok && second.data) {
          if (preferPayout) setPayment(second.data as FinancePayment)
          else setPayout(second.data as FinancePayout)
        } else {
          setError(
            first.status === 401 || second.status === 401
              ? 'Sign in to load this payout.'
              : 'Could not load this payout.',
          )
          setLoading(false)
          return
        }
      } else {
        setError(first.status === 401 ? 'Sign in to load this payout.' : 'Could not load this payout.')
        setLoading(false)
        return
      }

      const [ref, setl] = await Promise.all([
        getFinanceRefunds(entityId),
        getFinanceSettlements(entityId),
      ])
      if (cancelled) return
      setRefunds(ref.ok ? ref.data?.refunds ?? [] : [])
      setSettlements(setl.ok ? setl.data?.settlements ?? [] : [])
      setLoading(false)
    }

    void load()
    return () => {
      cancelled = true
    }
  }, [entityId, exception?.entity_type])

  const runInvestigate = useCallback(async () => {
    setInvestigating(true)
    const rec = await createFinanceInvestigation({
      exception_id: exceptionId || exception?.id,
      entity_id: entityId,
      payment_id: entityId,
    })
    setInvestigating(false)
    if (rec.ok && rec.data) setInvestigation(rec.data)
  }, [entityId, exception?.id, exceptionId])

  const recon = payout?.reconciliation || payment?.reconciliation
  const amountMinor =
    payout?.amount_minor ??
    payment?.amount_minor ??
    exception?.expected_amount ??
    exception?.variance_amount ??
    investigation?.financial_impact ??
    0
  const currency = payout?.currency || payment?.currency || 'INR'
  const rawStatus = payout?.provider_status || payment?.provider_status || exception?.provider_status || ''
  const status = asPayoutStatus(rawStatus)
  const title = drawerTitle(exception?.entity_type || (payout ? 'payout' : payment ? 'payment' : 'payout'))
  const displayId = payout?.payout_id || payment?.payment_id || entityId
  const createdAt = payout?.provider_created_at || payment?.provider_created_at || exception?.created_at
  const settlementLine = settlements[0]
  const errorView = useMemo(
    () =>
      buildRazorpayXError({
        reason: recon?.reason || exception?.reason || exceptionId || '',
        status,
        description:
          investigation?.root_cause ||
          (exception?.reason ? reasonTitle(exception.reason) : null),
        nextSteps: investigation?.recommendation || undefined,
        source: undefined,
        payoutId: displayId,
      }),
    [investigation, recon?.reason, exception, exceptionId, status, displayId],
  )

  return (
    <aside
      className="flex h-full min-h-[calc(100dvh-7rem)] w-full flex-col border-l border-[#E6E8EB] bg-white shadow-[-12px_0_32px_rgba(26,26,26,0.08)] xl:max-w-[480px]"
      aria-label={title}
    >
      <div className="flex items-start justify-between gap-3 border-b border-[#E6E8EB] px-5 py-4">
        <div className="min-w-0">
          <p className="text-[16px] font-semibold tracking-[-0.02em] text-[#1A1A1A]">{title}</p>
          <div className="mt-1 flex min-w-0 items-center gap-1">
            <p className="truncate font-mono text-[13px] text-[#6B6B6B]">{displayId}</p>
            <CopyIdButton value={displayId} />
          </div>
        </div>
        <DrawerCloseButton onClick={onClose} label={`Close ${title.toLowerCase()}`} />
      </div>

      <div className="flex items-end justify-between gap-3 px-5 pt-5">
        <div>
          <p className="text-[11px] font-medium text-[#8F8F8F]">Amount</p>
          <p className="mt-1 text-[28px] font-semibold leading-none tracking-[-0.03em] tabular-nums text-[#1A1A1A]">
            {loading ? '—' : formatPaise(amountMinor, 2)}
          </p>
        </div>
        <StatusBadge tone={payoutStatusTone(status)}>{status}</StatusBadge>
      </div>

      <div className="mt-5 px-5">
        <UnderlineTabs
          items={[
            { id: 'details', label: 'Details' },
            { id: 'timeline', label: 'Timeline' },
          ]}
          active={tab}
          onChange={(id) => setTab(id as 'details' | 'timeline')}
        />
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
        {error ? <p className="text-[13px] text-[#C0372A]">{error}</p> : null}

        {tab === 'details' ? (
          <div className="space-y-5">
            <dl>
              <DrawerField label={payout ? 'Payout ID' : payment ? 'Payment ID' : 'Entity ID'} mono>
                <span className="inline-flex items-center gap-1">
                  {displayId}
                  <CopyIdButton value={displayId} />
                </span>
              </DrawerField>
              {exception?.id ? (
                <DrawerField label="Exception ID" mono>
                  {exception.id}
                </DrawerField>
              ) : null}
              {payment?.order_id ? <DrawerField label="Order ID" mono>{payment.order_id}</DrawerField> : null}
              <DrawerField label="Amount">{formatPaise(amountMinor, 2)}</DrawerField>
              <DrawerField label="Currency">{currency}</DrawerField>
              <DrawerField label="Status">{status}</DrawerField>
              <DrawerField label="Mode">{payout?.mode || payment?.method || exception?.entity_type || '—'}</DrawerField>
              <DrawerField label="UTR" mono>
                {payout?.utr || settlementLine?.utr || '—'}
              </DrawerField>
              <DrawerField label="Fees">
                {settlementLine?.fee_minor != null ? formatPaise(settlementLine.fee_minor, 2) : '₹0.00'}
              </DrawerField>
              <DrawerField label="Tax">
                {settlementLine?.tax_minor != null ? formatPaise(settlementLine.tax_minor, 2) : '₹0.00'}
              </DrawerField>
              {exception ? (
                <>
                  <DrawerField label="Expected">{formatPaise(exception.expected_amount, 2)}</DrawerField>
                  <DrawerField label="Observed">{formatPaise(exception.observed_amount, 2)}</DrawerField>
                  <DrawerField label="Variance">{formatPaise(exception.variance_amount, 2)}</DrawerField>
                </>
              ) : null}
              <DrawerField label="Processor">
                <PaymentProviderBadge provider={payment?.provider} />
              </DrawerField>
              {payment ? (
                <>
                  <DrawerField label="Refunds">
                    {refunds.length === 0 ? 'No refunds issued' : `${refunds.length} refund(s)`}
                  </DrawerField>
                  <DrawerField label="Settlement">
                    {settlements.length === 0
                      ? '—'
                      : `${settlementLine?.settlement_id || 'line'} · ${formatPaise(settlementLine?.amount_minor, 2)}`}
                  </DrawerField>
                </>
              ) : null}
              <DrawerField label="Created at">{formatWhen(createdAt)}</DrawerField>
              {recon?.result ? (
                <DrawerField label="Reconciliation">{reconLabel(recon.result)}</DrawerField>
              ) : exception?.reconciliation_result ? (
                <DrawerField label="Reconciliation">{reconLabel(exception.reconciliation_result)}</DrawerField>
              ) : null}
            </dl>

            <ErrorInvestigationPanel
              errorView={errorView}
              financialImpactMinor={investigation?.financial_impact ?? exception?.variance_amount ?? amountMinor}
              confidence={investigation?.confidence ?? recon?.confidence ?? exception?.confidence}
              investigating={investigating}
              hasRun={Boolean(investigation)}
              autoStart={
                status === 'failed' ||
                String(recon?.result || exception?.reconciliation_result || '').toUpperCase() !== 'MATCHED'
              }
              onInvestigate={() => void runInvestigate()}
              compact
              view="status"
            />
          </div>
        ) : timelineLoading ? (
          <p className="text-[13px] text-[#94A3B8]">Loading captured timeline…</p>
        ) : (
          <FinanceTimelineLadder timeline={timeline} />
        )}
      </div>
    </aside>
  )
}
