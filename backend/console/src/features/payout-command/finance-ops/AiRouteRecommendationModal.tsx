'use client'

import { useEffect, useMemo, useState } from 'react'
import { formatPaise } from './reasonCopy'
import {
  CONNECTED_BANKS,
  HDFC_NEFT_RECOMMENDATION,
  ROUTE_COMPARISON,
  ROUTING_STEPS,
  ROUTING_TOTAL_MS,
  type BulkBatchSummary,
  type ReceiverBankSlice,
  type RouteOption,
  type RouteRecommendation,
  type RoutingPhase,
} from './bulkRouteDemo'
import {
  getProcessors,
  pct,
  postRouteDecision,
  processorLabel,
  type RoutingDecision,
  type RoutingProcessor,
} from '@/services/payout-command/prod-api/routingApi'

function CheckIcon({ className = '' }: { className?: string }) {
  return (
    <svg className={className} width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden>
      <circle cx="8" cy="8" r="8" fill="#22C55E" />
      <path d="M4.5 8.2L6.8 10.5L11.5 5.5" stroke="white" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  )
}

function Spinner({ className = '' }: { className?: string }) {
  return (
    <span
      className={`inline-block h-4 w-4 animate-spin rounded-full border-2 border-[#BFDBFE] border-t-[#2F6FED] ${className}`}
      aria-hidden
    />
  )
}

function DonutChart({ slices }: { slices: ReceiverBankSlice[] }) {
  let acc = 0
  const gradient = slices
    .map((s) => {
      const start = acc
      acc += s.pct
      return `${s.color} ${start}% ${acc}%`
    })
    .join(', ')
  return (
    <div className="flex items-center gap-4">
      <div
        className="relative h-[88px] w-[88px] shrink-0 rounded-full"
        style={{ background: `conic-gradient(${gradient})` }}
        aria-hidden
      >
        <div className="absolute inset-[22px] rounded-full bg-white" />
      </div>
      <ul className="min-w-0 flex-1 space-y-1.5">
        {slices.map((s) => (
          <li key={s.name} className="flex items-center gap-2 text-[12px] text-[#334155]">
            <span className="h-2 w-2 shrink-0 rounded-full" style={{ background: s.color }} />
            <span className="min-w-0 flex-1 truncate">{s.name}</span>
            <span className="font-semibold tabular-nums text-[#1A1A1A]">{s.pct}%</span>
          </li>
        ))}
      </ul>
    </div>
  )
}

function ConfidenceBar({ value }: { value: number }) {
  const tone = value >= 90 ? 'bg-[#2F6FED]' : value >= 85 ? 'bg-[#60A5FA]' : 'bg-[#F97316]'
  return (
    <div className="flex min-w-[110px] items-center gap-2">
      <div className="h-2 flex-1 overflow-hidden rounded-full bg-[#E8EEF7]">
        <div className={`h-full rounded-full ${tone}`} style={{ width: `${Math.min(100, value)}%` }} />
      </div>
      <span className="w-8 text-right text-[12px] font-semibold tabular-nums text-[#1A1A1A]">{value}%</span>
    </div>
  )
}

function ThinkingStepper({ elapsedMs }: { elapsedMs: number }) {
  const elapsedSec = elapsedMs / 1000
  const remaining = Math.max(0, Math.ceil((ROUTING_TOTAL_MS - elapsedMs) / 1000))

  return (
    <div className="rounded-[10px] border border-[#E8EEF7] bg-[#F8FAFF] px-4 py-4 sm:px-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        {ROUTING_STEPS.map((step, i) => {
          const done = elapsedSec >= step.atSec
          const prevAt = i === 0 ? 0 : ROUTING_STEPS[i - 1].atSec
          const current = !done && elapsedSec >= prevAt
          return (
            <div key={step.id} className="relative flex min-w-[112px] flex-1 flex-col items-center text-center">
              {i < ROUTING_STEPS.length - 1 ? (
                <div
                  className={`absolute left-[calc(50%+14px)] right-[calc(-50%+14px)] top-[11px] h-[2px] ${
                    done ? 'bg-[#22C55E]' : 'bg-[#D6E0F0]'
                  }`}
                />
              ) : null}
              <div className="relative z-[1] flex h-6 w-6 items-center justify-center rounded-full bg-white">
                {done ? <CheckIcon /> : current ? <Spinner /> : (
                  <span className="h-2.5 w-2.5 rounded-full bg-[#CBD5E1]" />
                )}
              </div>
              <p
                className={`mt-2 text-[12px] font-semibold leading-tight ${
                  done || current ? 'text-[#1A1A1A]' : 'text-[#94A3B8]'
                }`}
              >
                {step.label}
              </p>
              <p className="mt-0.5 text-[11px] tabular-nums text-[#94A3B8]">{step.atSec}s</p>
            </div>
          )
        })}
      </div>
      <div className="mt-4 flex flex-wrap items-center justify-between gap-2 border-t border-[#E4EBF7] pt-3">
        <p className="text-[13px] text-[#334155]">
          AI is scoring eligible processors and building a fallback chain…
        </p>
        <p className="text-[12px] font-medium tabular-nums text-[#64748B]">
          Est. time remaining: {String(remaining).padStart(2, '0')}s
        </p>
      </div>
      <div className="mt-3 h-1.5 overflow-hidden rounded-full bg-[#E2E8F0]">
        <div
          className="h-full rounded-full bg-[#2F6FED] transition-[width] duration-300 ease-linear"
          style={{ width: `${Math.min(100, (elapsedMs / ROUTING_TOTAL_MS) * 100)}%` }}
        />
      </div>
    </div>
  )
}

export function AiRouteRecommendationModal({
  open,
  phase,
  summary,
  onAnalyzeComplete,
  onApprove,
  onCancel,
  onAskZord,
  onLiveDecision,
}: {
  open: boolean
  phase: RoutingPhase
  summary: BulkBatchSummary
  onAnalyzeComplete: () => void
  onApprove: () => void
  onCancel: () => void
  onAskZord?: () => void
  onLiveDecision?: (decision: { bank: string; rail: string } | null) => void
}) {
  const [elapsedMs, setElapsedMs] = useState(0)
  const [reveal, setReveal] = useState(false)
  const [decision, setDecision] = useState<RoutingDecision | null>(null)
  const [processors, setProcessors] = useState<RoutingProcessor[]>([])
  const [routeError, setRouteError] = useState<string | null>(null)
  const thinking = phase === 'analyzing'
  const ready = phase === 'ready' || phase === 'approved'

  useEffect(() => {
    if (!open || !thinking) return
    setElapsedMs(0)
    setReveal(false)
    setDecision(null)
    setProcessors([])
    setRouteError(null)
    const started = performance.now()
    let finished = false
    let liveDone = false
    let cancelled = false
    let minElapsed = false

    const finish = () => {
      if (finished || cancelled) return
      finished = true
      setElapsedMs(ROUTING_TOTAL_MS)
      onAnalyzeComplete()
    }

    const maybeFinish = () => {
      if (liveDone && minElapsed) finish()
    }

    void Promise.all([
      postRouteDecision({
        payment_id: summary.batchId || summary.fileName,
        amount_minor: summary.routeAmountMinor || summary.totalAmountMinor,
        currency: 'INR',
        payment_method: summary.requestedRail || 'IMPS',
        direction: 'OUTBOUND',
        country: 'IN',
      }),
      getProcessors(),
    ]).then(([routeRes, processorRes]) => {
      if (cancelled) return
      liveDone = true
      if (routeRes.ok && routeRes.data) {
        setDecision(routeRes.data)
        onLiveDecision?.({
          bank: processorLabel(routeRes.data.selected_processor || routeRes.data.psp),
          rail: routeRes.data.rail,
        })
      } else {
        setRouteError(routeRes.errorText || `router HTTP ${routeRes.status}`)
        onLiveDecision?.(null)
      }
      if (processorRes.ok) setProcessors(processorRes.processors)
      maybeFinish()
    })

    const progress = window.setInterval(() => {
      if (cancelled || finished) return
      setElapsedMs(Math.min(ROUTING_TOTAL_MS, performance.now() - started))
    }, 100)
    const minWait = window.setTimeout(() => {
      minElapsed = true
      maybeFinish()
    }, 1200)
    const maxWait = window.setTimeout(finish, ROUTING_TOTAL_MS)

    return () => {
      cancelled = true
      window.clearInterval(progress)
      window.clearTimeout(minWait)
      window.clearTimeout(maxWait)
    }
  }, [open, thinking, onAnalyzeComplete, onLiveDecision, summary.batchId, summary.fileName, summary.totalAmountMinor, summary.routeAmountMinor, summary.requestedRail])

  useEffect(() => {
    if (!ready) {
      setReveal(false)
      return
    }
    setElapsedMs(ROUTING_TOTAL_MS)
    const t = window.setTimeout(() => setReveal(true), 80)
    return () => window.clearTimeout(t)
  }, [ready])

  const rec: RouteRecommendation = useMemo(() => {
    if (!decision) return HDFC_NEFT_RECOMMENDATION
    const rail = (decision.rail || 'NEFT') as RouteRecommendation['rail']
    const bank = processorLabel(decision.selected_processor || decision.psp)
    const fallback = decision.fallback_processors?.[0]
      ? `${processorLabel(decision.fallback_processors[0])} · ${rail} if primary declines`
      : HDFC_NEFT_RECOMMENDATION.fallback
    return {
      ...HDFC_NEFT_RECOMMENDATION,
      bank,
      rail,
      eta: rail === 'IMPS' || rail === 'UPI' ? 'T+0' : 'T+1',
      etaNote: rail === 'IMPS' || rail === 'UPI' ? 'Near real-time' : 'Banking hours',
      confidence: pct(decision.score),
      successProbability: pct(decision.reason?.authorization_rate ?? decision.score),
      why: [
        `Selected by ${decision.routing_strategy} scoring (auth × 0.45 + health × 0.25 + cost × 0.15 + latency × 0.10 + priority × 0.05)`,
        `Authorization rate ${pct(decision.reason.authorization_rate)}% · health ${pct(decision.reason.health_score)}%`,
        `Connector ${decision.connector_id}`,
        decision.rail_rewritten_from
          ? `Rail rewritten ${decision.rail_rewritten_from} → ${rail} from file amount`
          : `Requested rail from file: ${summary.requestedRail || 'IMPS'}`,
        decision.rules_applied?.length
          ? `Rules applied: ${decision.rules_applied.join(', ')}`
          : 'No merchant override rules matched',
        `Metrics source: ${decision.metrics_source}`,
        fallback,
      ],
      fallback,
      confidenceLabel: decision.metrics_source === 'static_config' ? 'Config-scored' : 'Live-scored',
    }
  }, [decision, summary.requestedRail])

  const connectedBanks = useMemo(() => {
    if (!processors.length) return CONNECTED_BANKS
    return processors
      .filter((p) => p.enabled)
      .map((p) => ({
        name: p.name || processorLabel(p.psp),
        short: processorLabel(p.psp),
        health: p.health_score >= 0.9 ? ('Healthy' as const) : ('Degraded' as const),
        score: pct(p.health_score),
      }))
  }, [processors])

  const comparison: RouteOption[] = useMemo(() => {
    if (!decision?.candidates?.length) return ROUTE_COMPARISON
    return decision.candidates.map((c, i) => ({
      bank: processorLabel(c.psp),
      rail: (decision.rail || 'NEFT') as RouteOption['rail'],
      successRate: pct(c.breakdown.authorization_rate),
      eta: decision.rail === 'IMPS' || decision.rail === 'UPI' ? 'T+0' : 'T+1',
      etaNote: decision.rail === 'IMPS' || decision.rail === 'UPI' ? 'Near real-time' : 'Banking hours',
      completionDate: HDFC_NEFT_RECOMMENDATION.completionDate,
      completionTime: HDFC_NEFT_RECOMMENDATION.completionTime,
      costMinor: Math.round((1 - c.breakdown.cost_score) * 200_000),
      confidence: pct(c.score),
      recommended: i === 0,
    }))
  }, [decision])

  const title = useMemo(() => {
    if (thinking) return 'AI Route Recommendation'
    if (phase === 'approved') return 'Route approved · Dispatching'
    return 'AI Route Recommendation'
  }, [thinking, phase])

  if (!open) return null

  return (
    <div className="fixed inset-0 z-[80] flex items-start justify-center overflow-y-auto bg-black/40 px-3 py-6 sm:px-6 sm:py-10">
      <div
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className="relative w-full max-w-[1080px] overflow-hidden rounded-[14px] border border-[#E6E8EB] bg-white shadow-[0_24px_64px_rgba(15,23,42,0.22)]"
      >
        <div className="flex items-start justify-between gap-4 border-b border-[#EEF0F3] px-5 py-4 sm:px-6">
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <h2 className="text-[18px] font-semibold tracking-[-0.01em] text-[#1A1A1A]">{title}</h2>
              {thinking ? (
                <span className="inline-flex items-center gap-1.5 rounded-full bg-[#EEF4FF] px-2.5 py-0.5 text-[11px] font-semibold text-[#2F6FED]">
                  <Spinner className="h-3 w-3" />
                  Analyzing
                </span>
              ) : null}
              {phase === 'ready' ? (
                <span className="rounded-full bg-[#ECFDF5] px-2.5 py-0.5 text-[11px] font-semibold text-[#147A3F]">
                  Ready for approval
                </span>
              ) : null}
              {phase === 'approved' ? (
                <span className="rounded-full bg-[#ECFDF5] px-2.5 py-0.5 text-[11px] font-semibold text-[#147A3F]">
                  Approved
                </span>
              ) : null}
            </div>
            <p className="mt-1 text-[13px] text-[#64748B]">
              {thinking
                ? 'Routing engine is scoring eligible PSPs and rails for this batch.'
                : routeError
                  ? `Router did not answer (${routeError.slice(0, 80)}). Showing last demo layout — do not treat it as a live decision.`
                  : `Live decision ${rec.bank} · ${rec.rail}. Approval still required before dispatch.`}
            </p>
          </div>
          <button
            type="button"
            onClick={onCancel}
            className="h-8 shrink-0 rounded-[6px] px-2 text-[20px] leading-none text-[#94A3B8] hover:bg-[#F8FAFC] hover:text-[#1A1A1A]"
            aria-label="Close"
          >
            ×
          </button>
        </div>

        <div className="max-h-[min(78vh,860px)] space-y-5 overflow-y-auto px-5 py-5 sm:px-6">
          {thinking || !reveal ? <ThinkingStepper elapsedMs={elapsedMs} /> : null}

          {reveal ? (
            <div
              className="space-y-5 transition-opacity duration-500"
              style={{ opacity: reveal ? 1 : 0 }}
            >
              {/* Keep completed timeline visible so the story reads: think → then decide */}
              {!thinking ? (
                <div className="rounded-[10px] border border-[#DCFCE7] bg-[#F0FDF4] px-4 py-3 text-[13px] text-[#166534]">
                  <span className="font-semibold">Recommendation ready.</span> AI finished evaluating rails,
                  bank health, and receiver mix for this batch.
                </div>
              ) : null}
              <div className="grid gap-3 lg:grid-cols-3">
                <section className="rounded-[10px] border border-[#E6E8EB] bg-white p-4">
                  <p className="text-[12px] font-semibold uppercase tracking-[0.06em] text-[#8F8F8F]">
                    Batch Summary
                  </p>
                  <dl className="mt-3 space-y-2 text-[13px]">
                    {[
                      ['Batch ID', summary.batchId],
                      ['File Name', summary.fileName],
                      ['Total Records', summary.totalRecords.toLocaleString('en-IN')],
                      ['Total Amount', formatPaise(summary.totalAmountMinor, 2)],
                      ['Unique Beneficiaries', summary.uniqueBeneficiaries.toLocaleString('en-IN')],
                      ['Upload Time', summary.uploadTime],
                      ['Requested By', summary.requestedBy],
                    ].map(([k, v]) => (
                      <div key={k} className="grid grid-cols-[140px_1fr] gap-2">
                        <dt className="text-[#94A3B8]">{k}</dt>
                        <dd className="truncate font-medium text-[#1A1A1A]" title={String(v)}>
                          {v}
                        </dd>
                      </div>
                    ))}
                  </dl>
                </section>

                <section className="rounded-[10px] border border-[#E6E8EB] bg-white p-4">
                  <p className="text-[12px] font-semibold uppercase tracking-[0.06em] text-[#8F8F8F]">
                    {processors.length ? `Eligible processors (${connectedBanks.length})` : `Connected Banks (${connectedBanks.length})`}
                  </p>
                  <ul className="mt-3 space-y-2.5">
                    {connectedBanks.map((b) => (
                      <li
                        key={b.short}
                        className="flex items-center justify-between gap-2 rounded-[8px] border border-[#F1F5F9] bg-[#FAFBFC] px-3 py-2"
                      >
                        <div className="min-w-0">
                          <p className="text-[13px] font-semibold text-[#1A1A1A]">{b.name}</p>
                          <p className="text-[11px] text-[#94A3B8]">Sender connection</p>
                        </div>
                        <div className="flex items-center gap-2">
                          <span className="rounded-full bg-[#ECFDF5] px-2 py-0.5 text-[11px] font-semibold text-[#147A3F]">
                            {b.health}
                          </span>
                          <span className="text-[13px] font-semibold tabular-nums text-[#1A1A1A]">{b.score}%</span>
                        </div>
                      </li>
                    ))}
                  </ul>
                </section>

                <section className="rounded-[10px] border border-[#E6E8EB] bg-white p-4">
                  <p className="text-[12px] font-semibold uppercase tracking-[0.06em] text-[#8F8F8F]">
                    Receivers Bank Distribution
                  </p>
                  <p className="mt-1 text-[12px] text-[#94A3B8]">
                    {(summary.receiverIfscCount ?? 0) > 0
                      ? `From ${summary.receiverIfscCount.toLocaleString('en-IN')} beneficiary IFSC${summary.receiverIfscCount === 1 ? '' : 's'} in ${summary.fileName}`
                      : 'No beneficiary IFSC column found in this file'}
                  </p>
                  <div className="mt-3">
                    {summary.receiverBanks?.length ? (
                      <DonutChart slices={summary.receiverBanks} />
                    ) : (
                      <p className="rounded-[8px] border border-dashed border-[#E2E8F0] bg-[#FAFBFC] px-3 py-6 text-center text-[12px] text-[#94A3B8]">
                        Upload a payout CSV with IFSC (for example <span className="font-medium">beneficiary.instrument.ifsc</span>) to see this mix.
                      </p>
                    )}
                  </div>
                </section>
              </div>

              <div className="grid gap-3 lg:grid-cols-[1.35fr_1fr]">
                <section className="rounded-[10px] border border-[#BBF7D0] bg-[#F0FDF4] p-4">
                  <div className="flex flex-wrap items-center gap-2">
                    <p className="text-[12px] font-semibold uppercase tracking-[0.06em] text-[#166534]">
                      Recommended Route
                    </p>
                    <span className="rounded-full bg-[#22C55E] px-2 py-0.5 text-[11px] font-semibold text-white">
                      Recommended
                    </span>
                  </div>
                  <h3 className="mt-2 text-[22px] font-semibold tracking-[-0.02em] text-[#14532D]">
                    {rec.bank} ({rec.rail})
                  </h3>
                  <div className="mt-4 grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
                    {[
                      {
                        label: 'Expected Success Rate',
                        value: `${rec.successProbability}%`,
                        note: 'Historical success',
                      },
                      {
                        label: 'Processing Time (ETA)',
                        value: rec.processingEta,
                        note: rec.processingEtaNote,
                      },
                      {
                        label: 'Estimated Completion',
                        value: rec.completionDate,
                        note: rec.completionTime,
                      },
                      {
                        label: 'Confidence Score',
                        value: `${rec.confidence}%`,
                        note: rec.confidenceLabel,
                      },
                    ].map((m) => (
                      <div key={m.label} className="rounded-[8px] border border-[#BBF7D0] bg-white px-3 py-3">
                        <p className="text-[11px] font-medium text-[#64748B]">{m.label}</p>
                        <p className="mt-1 text-[18px] font-semibold tabular-nums text-[#1A1A1A]">{m.value}</p>
                        <p className="mt-0.5 text-[11px] text-[#94A3B8]">{m.note}</p>
                      </div>
                    ))}
                  </div>
                </section>

                <section className="rounded-[10px] border border-[#E6E8EB] bg-white p-4">
                  <p className="text-[12px] font-semibold uppercase tracking-[0.06em] text-[#8F8F8F]">
                    Why this route?
                  </p>
                  <ul className="mt-3 space-y-2.5">
                    {rec.why.map((w) => (
                      <li key={w} className="flex items-start gap-2 text-[13px] text-[#334155]">
                        <CheckIcon className="mt-0.5 shrink-0" />
                        <span>{w}</span>
                      </li>
                    ))}
                  </ul>
                </section>
              </div>

              <section className="overflow-hidden rounded-[10px] border border-[#E6E8EB] bg-white">
                <div className="border-b border-[#EEF0F3] px-4 py-3">
                  <p className="text-[13px] font-semibold text-[#1A1A1A]">Route Comparison</p>
                  <p className="mt-0.5 text-[12px] text-[#94A3B8]">
                    AI ranked processors by eligibility, rules, then weighted score.
                  </p>
                </div>
                <div className="overflow-x-auto">
                  <table className="w-full min-w-[780px] text-left text-[13px]">
                    <thead className="bg-[#FAFBFC] text-[11px] font-semibold uppercase tracking-[0.06em] text-[#8F8F8F]">
                      <tr>
                        <th className="px-4 py-3">Provider-Rail</th>
                        <th className="px-4 py-3">Success Rate</th>
                        <th className="px-4 py-3">ETA</th>
                        <th className="px-4 py-3">Est. Completion</th>
                        <th className="px-4 py-3 text-right">Cost (Est.)</th>
                        <th className="px-4 py-3">Confidence</th>
                      </tr>
                    </thead>
                    <tbody>
                      {comparison.map((row) => (
                        <tr
                          key={`${row.bank}-${row.rail}`}
                          className={`border-t border-[#F1F5F9] ${row.recommended ? 'bg-[#F8FBFF]' : ''}`}
                        >
                          <td className="px-4 py-3">
                            <div className="flex flex-wrap items-center gap-2">
                              <span className="font-semibold text-[#1A1A1A]">
                                {row.bank} - {row.rail}
                              </span>
                              {row.recommended ? (
                                <span className="rounded-full bg-[#ECFDF5] px-2 py-0.5 text-[10px] font-semibold text-[#147A3F]">
                                  Recommended
                                </span>
                              ) : null}
                            </div>
                          </td>
                          <td className="px-4 py-3 tabular-nums text-[#334155]">{row.successRate}%</td>
                          <td className="px-4 py-3 text-[#334155]">
                            {row.eta}
                            <span className="mt-0.5 block text-[11px] text-[#94A3B8]">{row.etaNote}</span>
                          </td>
                          <td className="px-4 py-3 text-[#334155]">
                            {row.completionDate}
                            <span className="mt-0.5 block text-[11px] text-[#94A3B8]">{row.completionTime}</span>
                          </td>
                          <td className="px-4 py-3 text-right tabular-nums font-medium text-[#1A1A1A]">
                            {formatPaise(row.costMinor, 2)}
                          </td>
                          <td className="px-4 py-3">
                            <ConfidenceBar value={row.confidence} />
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </section>
            </div>
          ) : null}
        </div>

        <div className="border-t border-[#EEF0F3] bg-[#FAFBFC] px-5 py-4 sm:px-6">
          <div className="mb-3 rounded-[8px] border border-[#FDE68A] bg-[#FFFBEB] px-3 py-2 text-[12px] text-[#92400E]">
            AI recommendation is not final. Your approval is required before dispatch. Provider status stays{' '}
            <span className="font-semibold">pending</span> until you approve.
          </div>
          <div className="flex flex-wrap items-center justify-end gap-2">
            <button
              type="button"
              onClick={onCancel}
              className="h-10 rounded-[8px] border border-[#E6E8EB] bg-white px-4 text-[13px] font-medium text-[#1A1A1A] hover:bg-[#F8FAFC]"
            >
              Cancel
            </button>
            {onAskZord && ready ? (
              <button
                type="button"
                onClick={onAskZord}
                className="h-10 rounded-[8px] border border-[#E6E8EB] bg-white px-4 text-[13px] font-medium text-[#2F6FED] hover:bg-[#F8FAFC]"
              >
                Ask Zord
              </button>
            ) : null}
            <button
              type="button"
              disabled={!ready || phase === 'approved'}
              onClick={onApprove}
              className="h-10 rounded-[8px] bg-[#2F6FED] px-4 text-[13px] font-semibold text-white hover:bg-[#2563EB] disabled:cursor-not-allowed disabled:bg-[#94A3B8]"
            >
              {phase === 'approved' ? 'Dispatch started' : 'Approve & Proceed to Dispatch'}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
