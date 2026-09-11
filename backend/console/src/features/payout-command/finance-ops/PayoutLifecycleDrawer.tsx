'use client'

import { useCallback, useState } from 'react'
import { formatPaise } from './reasonCopy'
import { CopyIdButton, DrawerCloseButton, StatusBadge } from './razorpayChrome'
import { PayoutLifecycleView } from './PayoutLifecycleView'
import { buildPayoutLifecycle } from './payoutLifecycleModel'
import type { PayoutReconDisplayRow } from './payoutReconCopy'
import { ErrorInvestigationPanel } from './ErrorInvestigationPanel'
import { buildRazorpayXError } from './razorpayXErrors'
import { payoutStatusTone, type RazorpayPayoutStatus } from './razorpayPayoutStatus'
import { useFinanceTimeline } from './FinanceTimelineLadder'
import { ReconLegBadge } from './reconLegs'

function asStatus(status?: string | null): RazorpayPayoutStatus {
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
  return 'processing'
}

export function PayoutLifecycleDrawer({
  row,
  onClose,
}: {
  row: PayoutReconDisplayRow
  onClose: () => void
}) {
  const life = buildPayoutLifecycle(row)
  const traceHref = `/reconciliation/${encodeURIComponent(row.payoutId)}`
  const { timeline, loading: timelineLoading } = useFinanceTimeline(
    row.payoutId.startsWith('pay_') ? 'payments' : 'payouts',
    row.payoutId,
  )
  const [hasRun, setHasRun] = useState(false)
  const needsInvestigate =
    String(row.status || '').toLowerCase() === 'failed' ||
    String(row.result || '').toUpperCase() !== 'MATCHED' ||
    (row.varianceMinor || 0) > 0
  const status = asStatus(row.status)

  const onInvestigate = useCallback(() => {
    setHasRun(true)
  }, [])

  return (
    <aside
      className="fixed inset-y-0 right-0 z-40 flex w-full max-w-[480px] flex-col border-l border-[#E6E8EB] bg-white shadow-[-12px_0_32px_rgba(26,26,26,0.08)]"
      aria-label="Payout Details"
    >
      <div className="flex items-start justify-between gap-3 border-b border-[#E6E8EB] px-5 py-4">
        <div className="min-w-0">
          <p className="text-[16px] font-semibold tracking-[-0.02em] text-[#1A1A1A]">Payout Details</p>
          <div className="mt-1 flex min-w-0 items-center gap-1">
            <p className="truncate font-mono text-[13px] text-[#6B6B6B]">{row.payoutId}</p>
            <CopyIdButton value={row.payoutId} />
          </div>
        </div>
        <DrawerCloseButton onClick={onClose} label="Close payout details" />
      </div>
      <div className="flex items-end justify-between gap-3 px-5 pt-5">
        <div>
          <p className="text-[11px] font-medium text-[#8F8F8F]">Amount</p>
          <p className="mt-1 text-[28px] font-semibold leading-none tracking-[-0.03em] tabular-nums text-[#1A1A1A]">
            {formatPaise(row.amountMinor, 2)}
          </p>
        </div>
        <StatusBadge tone={payoutStatusTone(status)}>{status}</StatusBadge>
      </div>
      <div className="grid grid-cols-3 gap-3 border-b border-[#EEF0F3] px-5 py-3">
        <div>
          <p className="text-[10px] font-semibold uppercase tracking-[0.06em] text-[#8F8F8F]">Close</p>
          <p className="mt-1 text-[12px] font-semibold text-[#1A1A1A]">{row.result || '—'}</p>
        </div>
        <ReconLegBadge label="2-way" leg={row.twoWay} />
        <ReconLegBadge label="3-way" leg={row.threeWay} />
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
        <div className="mt-1">
          <PayoutLifecycleView
            life={life}
            variant="drawer"
            initialTab="events"
            traceHref={traceHref}
            capturedTimeline={timeline}
            timelineLoading={timelineLoading}
          />
        </div>
        {needsInvestigate ? (
          <ErrorInvestigationPanel
            errorView={buildRazorpayXError({
              reason: row.errorCode || row.reason,
              status: row.status,
              description: row.errorDescription || row.evidence,
              source: row.signalSource,
              nextSteps: row.nextSteps,
              payoutId: row.payoutId,
            })}
            financialImpactMinor={row.varianceMinor || row.amountMinor}
            hasRun={hasRun}
            autoStart
            onInvestigate={onInvestigate}
          />
        ) : null}
      </div>
    </aside>
  )
}
