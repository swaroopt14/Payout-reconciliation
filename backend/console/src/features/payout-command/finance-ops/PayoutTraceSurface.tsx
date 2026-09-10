'use client'

import { useEffect, useMemo, useState } from 'react'
import Link from 'next/link'
import { getFinancePayment, getFinancePayout, getFinanceTimeline } from '@/services/payout-command/prod-api/financeApi'
import type { FinanceEntityTimeline } from '@/services/payout-command/prod-api/financeTypes'
import { formatPaise } from './reasonCopy'
import { RZ_MUTED, RZ_PAGE, RZ_WRAP } from './razorpayChrome'
import {
  mapFinanceRowToPayoutRecon,
  mapPaymentResponseToReconRow,
  mapPayoutResponseToReconRow,
} from './payoutReconCopy'
import { buildPayoutLifecycle } from './payoutLifecycleModel'
import { PayoutLifecycleView } from './PayoutLifecycleView'
import { ErrorInvestigationPanel } from './ErrorInvestigationPanel'
import { buildRazorpayXError } from './razorpayXErrors'

export function PayoutTraceSurface({ payoutId }: { payoutId: string }) {
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [row, setRow] = useState<ReturnType<typeof mapFinanceRowToPayoutRecon> | null>(null)
  const [timeline, setTimeline] = useState<FinanceEntityTimeline | null>(null)
  const [timelineLoading, setTimelineLoading] = useState(true)
  const [evidenceRefs, setEvidenceRefs] = useState<Record<string, unknown> | null>(null)
  const [providerRecord, setProviderRecord] = useState<Record<string, unknown> | null>(null)

  useEffect(() => {
    let cancelled = false
    async function load() {
      setLoading(true)
      setError(null)
      setTimelineLoading(true)
      const looksPayment = payoutId.startsWith('pay_')
      const entityRes = looksPayment ? await getFinancePayment(payoutId) : await getFinancePayout(payoutId)
      let kind: 'payouts' | 'payments' = looksPayment ? 'payments' : 'payouts'
      let mapped: ReturnType<typeof mapFinanceRowToPayoutRecon> | null = null
      let refs: Record<string, unknown> | null = null
      let record: Record<string, unknown> | null = null

      if (entityRes.ok && entityRes.data) {
        if (looksPayment && 'payment_id' in entityRes.data) {
          mapped = mapFinanceRowToPayoutRecon(mapPaymentResponseToReconRow(entityRes.data))
          refs = (entityRes.data.evidence_refs as Record<string, unknown> | undefined) ?? null
          record = entityRes.data as unknown as Record<string, unknown>
        } else if (!looksPayment && 'payout_id' in entityRes.data) {
          mapped = mapFinanceRowToPayoutRecon(mapPayoutResponseToReconRow(entityRes.data))
          refs = (entityRes.data.evidence_refs as Record<string, unknown> | undefined) ?? null
          record = entityRes.data as unknown as Record<string, unknown>
        }
      } else if (entityRes.status === 404) {
        const other = looksPayment ? await getFinancePayout(payoutId) : await getFinancePayment(payoutId)
        if (other.ok && other.data) {
          kind = looksPayment ? 'payouts' : 'payments'
          if ('payout_id' in other.data) {
            mapped = mapFinanceRowToPayoutRecon(mapPayoutResponseToReconRow(other.data))
          } else {
            mapped = mapFinanceRowToPayoutRecon(mapPaymentResponseToReconRow(other.data))
          }
          refs = (other.data.evidence_refs as Record<string, unknown> | undefined) ?? null
          record = other.data as unknown as Record<string, unknown>
        }
      }

      if (cancelled) return
      if (!mapped) {
        setError(
          entityRes.status === 401
            ? 'Sign in to load this payout.'
            : entityRes.status === 404
              ? 'Payout not found.'
              : 'Could not load payout trace.',
        )
        setRow(null)
        setTimeline(null)
        setLoading(false)
        setTimelineLoading(false)
        return
      }

      setRow(mapped)
      setEvidenceRefs(refs)
      setProviderRecord(record)
      const tl = await getFinanceTimeline(kind, payoutId)
      if (cancelled) return
      if (tl.ok && tl.data) {
        setTimeline(tl.data)
      } else {
        setTimeline(null)
      }
      setLoading(false)
      setTimelineLoading(false)
    }
    void load()
    return () => {
      cancelled = true
    }
  }, [payoutId])

  const life = useMemo(() => (row ? buildPayoutLifecycle(row) : null), [row])

  return (
    <div className={RZ_PAGE}>
      <div className={RZ_WRAP}>
        <Link href="/reconciliation" className="text-[13px] font-medium text-[#528FF0] hover:underline">
          ← Reconciliation
        </Link>

        {loading ? (
          <p className={`mt-8 ${RZ_MUTED}`}>Loading transaction lifecycle…</p>
        ) : error ? (
          <p className="mt-8 text-[13px] text-[#B91C1C]">{error}</p>
        ) : row && life ? (
          <>
            <div className="mt-4 flex flex-wrap items-end justify-between gap-3">
              <div>
                <p className="text-[11px] font-semibold uppercase tracking-[0.08em] text-[#94A3B8]">
                  Transaction lifecycle
                </p>
                <h1 className="mt-1 font-mono text-[22px] font-semibold tracking-tight text-[#1A1A1A]">
                  {row.payoutId}
                </h1>
                <p className="mt-2 text-[32px] font-semibold tabular-nums tracking-[-0.03em] text-[#1A1A1A]">
                  {formatPaise(row.amountMinor, 2)}
                  <span className="ml-2 text-[14px] font-medium text-[#8F8F8F]">{row.currency || 'INR'}</span>
                </p>
              </div>
              <p className={RZ_MUTED}>
                Provider truth stays Razorpay status. Reconciliation is our control outcome.
              </p>
            </div>
            <div className="mt-6">
              <PayoutLifecycleView
                life={life}
                variant="page"
                initialTab="events"
                capturedTimeline={timeline}
                timelineLoading={timelineLoading}
                evidenceRefs={evidenceRefs}
                providerRecord={providerRecord}
              />
            </div>
            {String(row.status || '').toLowerCase() === 'failed' ||
            String(row.result || '').toUpperCase() === 'UNRESOLVED' ||
            String(row.result || '').toUpperCase() === 'CONFLICTED' ||
            String(row.result || '').toUpperCase() === 'VARIANCE' ? (
              <div className="mt-6 rounded-[10px] border border-[#E6E8EB] bg-white px-5 py-2">
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
                  confidence={undefined}
                  autoStart
                />
              </div>
            ) : null}
          </>
        ) : (
          <p className={`mt-8 ${RZ_MUTED}`}>Payout not found.</p>
        )}
      </div>
    </div>
  )
}
