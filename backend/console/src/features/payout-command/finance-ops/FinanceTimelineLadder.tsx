'use client'

import { useEffect, useState } from 'react'
import { getFinanceTimeline } from '@/services/payout-command/prod-api/financeApi'
import type { FinanceEntityTimeline, FinanceTimelineStep } from '@/services/payout-command/prod-api/financeTypes'

function formatCaptured(value?: string | null) {
  if (!value) return 'Not captured'
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return value
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

function stepTone(step: FinanceTimelineStep) {
  if (!step.captured) return 'bg-[#CBD5E1]'
  const status = String(step.provider_status || '').toLowerCase()
  if (status === 'failed' || status === 'rejected' || status === 'cancelled') return 'bg-[#DC2626]'
  if (step.kind === 'BANK_SIDE_CASH_MOVEMENT' && step.detail && step.detail.found === false) {
    return 'bg-[#D97706]'
  }
  return 'bg-[#16A34A]'
}

function factRows(step: FinanceTimelineStep) {
  const rows: { label: string; value: string }[] = []
  if (step.source) rows.push({ label: 'Source', value: step.source })
  if (step.source_event_id) rows.push({ label: 'Webhook / event', value: step.source_event_id })
  if (step.provider_status) rows.push({ label: 'Provider status', value: step.provider_status })
  if (step.provider_at) rows.push({ label: 'Provider clock', value: formatCaptured(step.provider_at) })
  if (step.source_hash) rows.push({ label: 'Source hash', value: step.source_hash })
  if (step.detail) {
    for (const [k, v] of Object.entries(step.detail)) {
      if (v == null || v === '') continue
      if (typeof v === 'object') continue
      rows.push({ label: k.replace(/_/g, ' '), value: String(v) })
    }
  }
  return rows
}

export function useFinanceTimeline(entity: 'payouts' | 'payments', entityId?: string | null) {
  const [timeline, setTimeline] = useState<FinanceEntityTimeline | null>(null)
  const [loading, setLoading] = useState(Boolean(entityId))

  useEffect(() => {
    if (!entityId) {
      setTimeline(null)
      setLoading(false)
      return
    }
    let cancelled = false
    setLoading(true)
    void getFinanceTimeline(entity, entityId).then((res) => {
      if (cancelled) return
      setTimeline(res.ok && res.data ? res.data : null)
      setLoading(false)
    })
    return () => {
      cancelled = true
    }
  }, [entity, entityId])

  return { timeline, loading }
}

export function FinanceTimelineLadder({
  timeline,
  emptyLabel = 'No captured timeline for this payout.',
}: {
  timeline: FinanceEntityTimeline | null
  emptyLabel?: string
}) {
  const [openSeq, setOpenSeq] = useState<number | null>(null)
  if (!timeline?.steps?.length) {
    return <p className="text-[13px] text-[#94A3B8]">{emptyLabel}</p>
  }

  return (
    <ol className="space-y-0">
      {timeline.steps.map((step, i) => {
        const last = i === timeline.steps.length - 1
        const open = openSeq === step.seq
        return (
          <li key={step.kind} className="flex gap-3">
            <div className="flex w-4 flex-col items-center">
              <span className={`mt-1.5 h-2.5 w-2.5 shrink-0 rounded-full ${stepTone(step)}`} />
              {last ? null : <span className="my-0.5 w-px flex-1 bg-[#E2E8F0]" />}
            </div>
            <div className="min-w-0 flex-1 pb-4">
              <button
                type="button"
                onClick={() => setOpenSeq(open ? null : step.seq)}
                className="flex w-full items-start justify-between gap-3 text-left"
              >
                <div>
                  <p className="font-mono text-[11px] text-[#94A3B8]">
                    {step.captured ? formatCaptured(step.captured_at) : 'Not captured'}
                  </p>
                  <p className="text-[13px] font-semibold text-[#0F172A]">
                    {step.seq}. {step.label}
                  </p>
                </div>
                <span className="text-[11px] font-medium text-[#528FF0]">{open ? 'Hide' : 'Details'}</span>
              </button>
              {open ? (
                <div className="mt-2 rounded-[8px] border border-[#E6E8EB] bg-white p-3">
                  {step.note ? <p className="text-[13px] leading-relaxed text-[#334155]">{step.note}</p> : null}
                  <dl className="mt-2 space-y-1.5">
                    {factRows(step).map((fact) => (
                      <div key={`${step.seq}-${fact.label}`} className="grid grid-cols-[120px_1fr] gap-2 text-[12px]">
                        <dt className="text-[#94A3B8]">{fact.label}</dt>
                        <dd className="break-all font-medium text-[#0F172A]">{fact.value}</dd>
                      </div>
                    ))}
                  </dl>
                  {step.events?.length ? (
                    <ul className="mt-3 space-y-2 border-t border-[#F1F5F9] pt-2">
                      {step.events.map((ev, idx) => (
                        <li key={`${ev.source_event_id || ev.captured_at}-${idx}`} className="text-[12px] text-[#334155]">
                          <p className="font-mono text-[11px] text-[#94A3B8]">{formatCaptured(ev.captured_at)}</p>
                          <p>
                            {ev.source || 'source'} · {ev.provider_status || 'status'}
                            {ev.source_event_id ? ` · ${ev.source_event_id}` : ''}
                            {ev.utr ? ` · UTR ${ev.utr}` : ''}
                          </p>
                        </li>
                      ))}
                    </ul>
                  ) : null}
                </div>
              ) : (
                <p className="mt-0.5 text-[12px] text-[#64748B]">
                  {step.captured
                    ? `${step.source || 'captured'}${step.provider_status ? ` · ${step.provider_status}` : ''}`
                    : step.note || 'No event stored for this step.'}
                </p>
              )}
            </div>
          </li>
        )
      })}
    </ol>
  )
}
