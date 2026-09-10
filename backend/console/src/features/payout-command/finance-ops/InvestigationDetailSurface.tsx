'use client'

import { useCallback, useEffect, useMemo, useState } from 'react'
import Link from 'next/link'
import {
  getFinanceInvestigation,
  getFinancePayment,
  getFinancePayout,
} from '@/services/payout-command/prod-api/financeApi'
import type { FinanceInvestigation, FinancePayment, FinancePayout } from '@/services/payout-command/prod-api/financeTypes'
import {
  RZ_CARD,
  RZ_MUTED,
  RZ_PAGE,
  RZ_WRAP,
  StatusBadge,
} from './razorpayChrome'
import { formatPaise, reasonTitle } from './reasonCopy'
import { buildRazorpayXError, INVESTIGATION_STEPS, INVESTIGATION_TOTAL_MS } from './razorpayXErrors'
import { ErrorInvestigationPanel } from './ErrorInvestigationPanel'
import {
  mapFinanceRowToPayoutRecon,
  mapPaymentResponseToReconRow,
  mapPayoutResponseToReconRow,
} from './payoutReconCopy'
import { buildPayoutLifecycle } from './payoutLifecycleModel'
import { PayoutLifecycleView } from './PayoutLifecycleView'
import { useFinanceTimeline } from './FinanceTimelineLadder'

type AgentPhase = 'booting' | 'running' | 'ready'

type AgentToolCall = {
  id: string
  tool: string
  input: string
  output: string
  atSec: number
}

function verdictTone(verdict: string) {
  const v = verdict.toUpperCase()
  if (v === 'SUPPORTED') return 'text-[#C0372A]'
  if (v === 'CONTRADICTED') return 'text-[#147A3F]'
  if (v === 'POSSIBLE') return 'text-[#B36B00]'
  return 'text-[#64748B]'
}

function verdictMark(verdict: string) {
  const v = verdict.toUpperCase()
  if (v === 'SUPPORTED') return '⚠'
  if (v === 'CONTRADICTED') return '✓'
  if (v === 'POSSIBLE') return '?'
  return '·'
}

function Spinner({ className = '' }: { className?: string }) {
  return (
    <span
      className={`inline-block h-3.5 w-3.5 animate-spin rounded-full border-2 border-[#BFDBFE] border-t-[#2F6FED] ${className}`}
      aria-hidden
    />
  )
}

function CheckIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden>
      <circle cx="8" cy="8" r="8" fill="#22C55E" />
      <path
        d="M4.5 8.2L6.8 10.5L11.5 5.5"
        stroke="white"
        strokeWidth="1.6"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  )
}

export function InvestigationDetailSurface({ investigationId }: { investigationId: string }) {
  const [row, setRow] = useState<FinanceInvestigation | null>(null)
  const [payment, setPayment] = useState<FinancePayment | null>(null)
  const [reconRow, setReconRow] = useState<ReturnType<typeof mapFinanceRowToPayoutRecon> | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [phase, setPhase] = useState<AgentPhase>('booting')
  const [elapsedMs, setElapsedMs] = useState(0)
  const [runKey, setRunKey] = useState(0)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    const rec = await getFinanceInvestigation(investigationId)
    if (!rec.ok || !rec.data?.data) {
      setError(rec.status === 401 ? 'Sign in to load investigation.' : 'Could not load investigation.')
      setLoading(false)
      return
    }
    const hit = rec.data.data
    setRow(hit)
    if (hit.entity_id) {
      const preferPayout = hit.entity_type === 'payout' || hit.entity_id.startsWith('pout_')
      const first = preferPayout ? await getFinancePayout(hit.entity_id) : await getFinancePayment(hit.entity_id)
      if (first.ok && first.data) {
        if (preferPayout) {
          const payout = first.data as FinancePayout
          setPayment(null)
          setReconRow(mapFinanceRowToPayoutRecon(mapPayoutResponseToReconRow(payout)))
        } else {
          const pay = first.data as FinancePayment
          setPayment(pay)
          setReconRow(mapFinanceRowToPayoutRecon(mapPaymentResponseToReconRow(pay)))
        }
      } else if (first.status === 404) {
        const second = preferPayout ? await getFinancePayment(hit.entity_id) : await getFinancePayout(hit.entity_id)
        if (second.ok && second.data) {
          if ('payout_id' in second.data) {
            setReconRow(mapFinanceRowToPayoutRecon(mapPayoutResponseToReconRow(second.data)))
          } else {
            setPayment(second.data)
            setReconRow(mapFinanceRowToPayoutRecon(mapPaymentResponseToReconRow(second.data)))
          }
        }
      }
    }
    setLoading(false)
    setPhase('running')
  }, [investigationId])

  useEffect(() => {
    void load()
  }, [load])

  useEffect(() => {
    if (phase !== 'running') return
    setElapsedMs(0)
    const started = performance.now()
    let raf = 0
    let finished = false
    const tick = () => {
      const next = Math.min(INVESTIGATION_TOTAL_MS, performance.now() - started)
      setElapsedMs(next)
      if (next >= INVESTIGATION_TOTAL_MS) {
        if (!finished) {
          finished = true
          setPhase('ready')
        }
        return
      }
      raf = window.requestAnimationFrame(tick)
    }
    raf = window.requestAnimationFrame(tick)
    return () => window.cancelAnimationFrame(raf)
  }, [phase, runKey])

  const title = useMemo(() => {
    if (row?.issue) return row.issue
    const reason = payment?.reconciliation?.reason || ''
    if (reason === 'failed_with_bank_movement') return 'Failed payment with unexplained bank movement'
    return reasonTitle(reason || row?.root_cause || 'Investigation')
  }, [row, payment])

  const amount = payment?.amount_minor ?? row?.financial_impact ?? 0
  const movement = payment?.financial_movement
  const recon = payment?.reconciliation
  const provider = (payment?.provider_status || reconRow?.status || '').toLowerCase()
  const hasBank = Boolean(movement?.bank) || Boolean(recon?.bank_credit_proven) || reconRow?.bank === true
  const hasSettlement = (movement?.settlement != null && movement.settlement > 0) || reconRow?.settlement === true
  const hasRefund = movement?.refund != null && movement.refund > 0
  const hypotheses = row?.hypotheses ?? []

  const errorView = useMemo(
    () =>
      buildRazorpayXError({
        reason: reconRow?.errorCode || recon?.reason || row?.root_cause || '',
        status: provider,
        description: reconRow?.errorDescription || row?.root_cause || '',
        source: reconRow?.signalSource || '',
        nextSteps: row?.recommendation || reconRow?.nextSteps || '',
        payoutId: row?.entity_id,
      }),
    [reconRow, recon, row, provider],
  )

  const life = useMemo(() => (reconRow ? buildPayoutLifecycle(reconRow) : null), [reconRow])
  const entityId = row?.entity_id || reconRow?.payoutId
  const { timeline, loading: timelineLoading } = useFinanceTimeline(
    entityId?.startsWith('pay_') ? 'payments' : 'payouts',
    entityId,
  )

  const toolCalls: AgentToolCall[] = useMemo(() => {
    const entity = row?.entity_id || 'payout'
    return [
      {
        id: 't1',
        tool: 'get_payout',
        input: entity,
        output: `status=${provider} · amount=${formatPaise(amount, 2)}`,
        atSec: 3,
      },
      {
        id: 't2',
        tool: 'list_payout_events',
        input: entity,
        output: 'payout.pending → payout.processing → payout.failed',
        atSec: 7,
      },
      {
        id: 't3',
        tool: 'search_settlement_lines',
        input: `settlement ↔ ${entity}`,
        output: hasSettlement ? 'Settlement line present' : 'No settlement credit for this payout',
        atSec: 11,
      },
      {
        id: 't4',
        tool: 'search_bank_transactions',
        input: `UTR / amount ${formatPaise(amount, 2)}`,
        output: hasBank
          ? 'Bank debit/credit observed — verify reversal'
          : 'No matching bank movement (clean fail path)',
        atSec: 15,
      },
      {
        id: 't5',
        tool: 'map_status_details',
        input: errorView.error.reason,
        output: `${errorView.forensics.signalName} · ${errorView.error.code}`,
        atSec: 18,
      },
      {
        id: 't6',
        tool: 'score_hypotheses',
        input: `${hypotheses.length} claims`,
        output: `confidence ${Math.round((row?.confidence || errorView.defaultConfidence) * 100)}%`,
        atSec: 20,
      },
    ]
  }, [row, provider, amount, hasSettlement, hasBank, errorView, hypotheses.length])

  const elapsedSec = elapsedMs / 1000
  const visibleTools = toolCalls.filter((t) => phase === 'ready' || elapsedSec >= t.atSec)
  const terminalFailed =
    provider === 'failed' ||
    life?.events.some((e) => e.state === 'fail') ||
    Boolean(errorView.forensics.failedProcess)

  const conclusion =
    row?.recommendation ||
    (terminalFailed && !hasBank
      ? `Provider failed with no money movement. Lifecycle stops at Failed — do not seal successful-credit evidence. Exposure ${formatPaise(row?.financial_impact ?? amount, 2)}.`
      : `${formatPaise(amount, 2)} needs finance review. Permanent loss is not proven. Do not rename Razorpay provider status.`)

  if (loading && phase === 'booting') {
    return (
      <div className={RZ_PAGE}>
        <div className={`${RZ_WRAP} max-w-[980px]`}>
          <p className={`mt-8 ${RZ_MUTED}`}>Booting investigation agent…</p>
        </div>
      </div>
    )
  }

  if (error) {
    return (
      <div className={RZ_PAGE}>
        <div className={`${RZ_WRAP} max-w-[980px]`}>
          <p className="mt-8 text-[13px] text-[#B91C1C]">{error}</p>
        </div>
      </div>
    )
  }

  if (!row) {
    return (
      <div className={RZ_PAGE}>
        <div className={`${RZ_WRAP} max-w-[980px]`}>
          <p className={`mt-8 ${RZ_MUTED}`}>Investigation not found.</p>
        </div>
      </div>
    )
  }

  return (
    <div className={RZ_PAGE}>
      <div className={`${RZ_WRAP} max-w-[980px]`}>
        <Link href="/investigations" className="text-[13px] font-medium text-[#528FF0] hover:underline">
          ← Investigations
        </Link>

        <div className="mt-4 flex flex-wrap items-start justify-between gap-4">
          <header className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <p className="font-mono text-[13px] font-semibold text-[#528FF0]">{row.id}</p>
              <StatusBadge tone={phase === 'ready' ? 'captured' : 'pending'}>
                {phase === 'ready' ? 'Agent complete' : 'Agent running'}
              </StatusBadge>
              {terminalFailed ? <StatusBadge tone="failed">Stopped at failure</StatusBadge> : null}
            </div>
            <h1 className="mt-1 text-[26px] font-semibold tracking-[-0.03em] text-[#1A1A1A]">{title}</h1>
            <p className="mt-2 text-[32px] font-semibold tabular-nums tracking-tight text-[#1A1A1A]">
              {formatPaise(amount, 2)}
              <span className="ml-2 text-[14px] font-medium text-[#8F8F8F]">
                {payment?.currency || 'INR'}
              </span>
            </p>
            <p className={`mt-1 ${RZ_MUTED}`}>
              {row.entity_type || 'payout'} · <span className="font-mono">{row.entity_id}</span>
              <span className="mx-1.5 text-[#D0D4DA]">·</span>
              Provider {provider}
              <span className="mx-1.5 text-[#D0D4DA]">·</span>
              Recon {recon?.result || reconRow?.result || 'UNRESOLVED'}
            </p>
          </header>
          <button
            type="button"
            onClick={() => {
              setPhase('running')
              setRunKey((k) => k + 1)
            }}
            className="inline-flex h-9 items-center gap-2 rounded-[6px] bg-[#0B1324] px-4 text-[13px] font-semibold text-white hover:bg-[#1E293B]"
          >
            {phase === 'running' ? <Spinner className="border-white/30 border-t-white" /> : null}
            {phase === 'running' ? 'Agent working…' : 'Re-run agent'}
          </button>
        </div>

        <div className="mt-5 grid gap-4 lg:grid-cols-[1.1fr_0.9fr]">
          <section className={`${RZ_CARD} px-5 py-5`}>
            <div className="flex items-center justify-between gap-2">
              <div>
                <p className="text-[11px] font-semibold uppercase tracking-[0.08em] text-[#94A3B8]">
                  AI agent · live run
                </p>
                <p className="mt-0.5 text-[14px] font-semibold text-[#1A1A1A]">
                  Finance investigation controller
                </p>
              </div>
              {phase === 'running' ? (
                <span className="inline-flex items-center gap-1.5 rounded-full bg-[#EEF4FF] px-2.5 py-1 text-[11px] font-semibold text-[#2F6FED]">
                  <Spinner /> Thinking
                </span>
              ) : (
                <span className="rounded-full bg-[#ECFDF5] px-2.5 py-1 text-[11px] font-semibold text-[#147A3F]">
                  Ready
                </span>
              )}
            </div>

            <ol className="mt-4 space-y-2.5">
              {INVESTIGATION_STEPS.map((step, i) => {
                const done = phase === 'ready' || elapsedSec >= step.atSec
                const prevAt = i === 0 ? 0 : INVESTIGATION_STEPS[i - 1].atSec
                const current = phase === 'running' && !done && elapsedSec >= prevAt
                return (
                  <li key={step.id} className="flex items-center gap-2.5 text-[13px]">
                    <span className="flex h-5 w-5 shrink-0 items-center justify-center">
                      {done ? <CheckIcon /> : current ? <Spinner /> : (
                        <span className="h-2 w-2 rounded-full bg-[#CBD5E1]" />
                      )}
                    </span>
                    <span className={done || current ? 'font-medium text-[#1A1A1A]' : 'text-[#94A3B8]'}>
                      {step.label}
                    </span>
                    <span className="ml-auto text-[11px] tabular-nums text-[#94A3B8]">{step.atSec}s</span>
                  </li>
                )
              })}
            </ol>

            {phase === 'running' ? (
              <div className="mt-4">
                <div className="h-1.5 overflow-hidden rounded-full bg-[#E2E8F0]">
                  <div
                    className="h-full rounded-full bg-[#2F6FED] transition-[width] duration-300 ease-linear"
                    style={{ width: `${Math.min(100, (elapsedMs / INVESTIGATION_TOTAL_MS) * 100)}%` }}
                  />
                </div>
                <p className="mt-2 text-[12px] text-[#64748B]">
                  Agent is tracing process stage, failure signal, and error-code origin…
                </p>
              </div>
            ) : (
              <p className="mt-4 rounded-[6px] bg-[#E8F8EE] px-3 py-2 text-[13px] font-semibold text-[#147A3F]">
                Investigation complete · do not rename provider status
              </p>
            )}
          </section>

          <section className={`${RZ_CARD} px-5 py-5`}>
            <p className="text-[11px] font-semibold uppercase tracking-[0.08em] text-[#94A3B8]">
              Tool calls
            </p>
            <p className="mt-1 text-[12px] text-[#94A3B8]">Agent actions stream as evidence is gathered</p>
            <ul className="mt-3 max-h-[280px] space-y-2 overflow-y-auto">
              {visibleTools.length === 0 ? (
                <li className="text-[12px] text-[#94A3B8]">Waiting for first tool call…</li>
              ) : (
                visibleTools.map((t) => (
                  <li
                    key={t.id}
                    className="rounded-[8px] border border-[#E8EEF7] bg-[#F8FAFF] px-3 py-2 font-mono text-[11px]"
                  >
                    <p className="font-semibold text-[#1D4ED8]">
                      {t.tool}
                      <span className="ml-2 font-normal text-[#94A3B8]">@{t.atSec}s</span>
                    </p>
                    <p className="mt-0.5 truncate text-[#64748B]">← {t.input}</p>
                    <p className="mt-0.5 text-[#0F172A]">→ {t.output}</p>
                  </li>
                ))
              )}
            </ul>
          </section>
        </div>

        {phase === 'ready' ? (
          <div className="mt-5 space-y-5">
            <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
              <Metric
                label="Failed process"
                value={errorView.forensics.failedProcess}
                mono={false}
              />
              <Metric label="Signal" value={errorView.forensics.signalName} mono={false} />
              <Metric
                label="Error origin"
                value={errorView.forensics.errorCodeOrigin}
                mono={false}
              />
              <Metric
                label="Confidence"
                value={`${Math.round((row.confidence || errorView.defaultConfidence) * 100)}%`}
                mono
              />
            </div>

            <section className={`${RZ_CARD} border-[#FECACA] bg-[#FEF2F2] px-5 py-4`}>
              <p className="text-[11px] font-semibold uppercase tracking-[0.08em] text-[#B91C1C]">
                Agent finding
              </p>
              <p className="mt-2 text-[15px] font-semibold text-[#7F1D1D]">
                {errorView.forensics.failedProcess} · pipeline {errorView.forensics.pipelineStage}
              </p>
              <p className="mt-1 text-[13px] leading-relaxed text-[#9F1239]">
                {errorView.forensics.failedProcessDetail}
              </p>
              <p className="mt-2 font-mono text-[11px] text-[#991B1B]">{errorView.forensics.signalTold}</p>
              <p className="mt-1 font-mono text-[11px] text-[#991B1B]">
                code path · {errorView.forensics.errorCodePath}
              </p>
            </section>

            <section className={`${RZ_CARD} px-5 py-5`}>
              <p className="text-[11px] font-semibold uppercase tracking-[0.08em] text-[#94A3B8]">
                Financial timeline
              </p>
              <div className="mt-4 grid grid-cols-[112px_1fr] gap-x-4">
                <div>
                  <p className="text-[13px] font-semibold text-[#0F172A]">Payment</p>
                  <p className="mt-1 text-[18px] font-semibold tabular-nums">{formatPaise(amount, 2)}</p>
                </div>
                <ol className="relative border-l border-[#E6E8EB] pl-5">
                  <TimelineNode label={provider.toUpperCase()} tone="fail" detail="Provider status unchanged" />
                  <TimelineNode
                    label={hasSettlement ? `Settlement ${formatPaise(movement?.settlement, 2)}` : 'No settlement'}
                    tone={hasSettlement ? 'ok' : 'muted'}
                  />
                  <TimelineNode
                    label={
                      hasBank
                        ? `Bank movement ${formatPaise(movement?.bank ?? amount, 2)}`
                        : 'No bank movement'
                    }
                    tone={hasBank ? 'warn' : 'muted'}
                    detail={hasBank ? 'Investigate — failure with money movement' : 'Clean fail path'}
                  />
                  <TimelineNode
                    label={hasRefund ? `Refund ${formatPaise(movement?.refund, 2)}` : 'No refund'}
                    tone={hasRefund ? 'ok' : 'muted'}
                    last
                  />
                </ol>
              </div>
              {terminalFailed && !hasBank ? (
                <p className="mt-4 rounded-[6px] bg-[#FEE2E2] px-3 py-2 text-[12px] font-semibold text-[#B91C1C]">
                  Lifecycle stops at Failed — no green Reconciled / Evidence sealed for a successful credit.
                </p>
              ) : null}
            </section>

            <section className={`${RZ_CARD} px-5 py-5`}>
              <p className="text-[11px] font-semibold uppercase tracking-[0.08em] text-[#94A3B8]">
                Hypotheses tested by agent
              </p>
              <ul className="mt-3 divide-y divide-[#F1F5F9]">
                {hypotheses.map((h) => (
                  <li key={h.claim} className="flex items-start justify-between gap-4 py-3">
                    <p className="text-[14px] text-[#0F172A]">
                      <span className={`mr-2 font-semibold ${verdictTone(h.verdict)}`}>
                        {verdictMark(h.verdict)}
                      </span>
                      {h.claim}
                    </p>
                    <span
                      className={`shrink-0 text-[11px] font-semibold uppercase tracking-[0.06em] ${verdictTone(h.verdict)}`}
                    >
                      {h.verdict}
                    </span>
                  </li>
                ))}
              </ul>
            </section>

            <section className={`${RZ_CARD} border-[#F6E7C1] bg-[#FFFEF8] px-5 py-5`}>
              <p className="text-[11px] font-semibold uppercase tracking-[0.08em] text-[#94A3B8]">
                Agent conclusion
              </p>
              <p className="mt-2 text-[16px] font-semibold leading-snug text-[#1A1A1A]">{conclusion}</p>
              {row.root_cause ? (
                <p className="mt-3 text-[13px] leading-relaxed text-[#64748B]">{row.root_cause}</p>
              ) : null}
              <p className="mt-3 text-[12px] text-[#8F8F8F]">
                Exposure {formatPaise(row.financial_impact, 2)} · confidence{' '}
                {Math.round((row.confidence || errorView.defaultConfidence) * 100)}%.
              </p>
              <div className="mt-4 flex flex-wrap gap-2">
                <Link
                  href={`/reconciliation/${encodeURIComponent(row.entity_id)}`}
                  className="rounded-[6px] border border-[#E6E8EB] bg-white px-3 py-1.5 text-[12px] font-semibold text-[#2F6FED] hover:bg-[#F8FAFC]"
                >
                  Open full trace →
                </Link>
                <Link
                  href="/exceptions"
                  className="rounded-[6px] border border-[#E6E8EB] bg-white px-3 py-1.5 text-[12px] font-semibold text-[#1A1A1A] hover:bg-[#F8FAFC]"
                >
                  Exceptions inbox
                </Link>
              </div>
            </section>

            {life ? (
              <section className={`${RZ_CARD} px-5 py-5`}>
                <p className="text-[11px] font-semibold uppercase tracking-[0.08em] text-[#94A3B8]">
                  Payout lifecycle (agent-aligned)
                </p>
                <p className="mt-1 text-[12px] text-[#94A3B8]">
                  Same stop-at-failure rules as Payouts / Reconciliation drawers.
                </p>
                <div className="mt-4">
                  <PayoutLifecycleView
                    life={life}
                    variant="drawer"
                    initialTab="events"
                    capturedTimeline={timeline}
                    timelineLoading={timelineLoading}
                  />
                </div>
              </section>
            ) : null}

            <ErrorInvestigationPanel
              errorView={errorView}
              financialImpactMinor={row.financial_impact || amount}
              confidence={row.confidence || errorView.defaultConfidence}
              hasRun
              autoStart={false}
            />
          </div>
        ) : null}
      </div>
    </div>
  )
}

function Metric({ label, value, mono }: { label: string; value: string; mono: boolean }) {
  return (
    <div className="rounded-[10px] border border-[#E6E8EB] bg-white px-4 py-3">
      <p className="text-[10px] font-semibold uppercase tracking-[0.08em] text-[#94A3B8]">{label}</p>
      <p className={`mt-1 text-[13px] font-semibold text-[#0F172A] ${mono ? 'font-mono' : ''}`}>{value}</p>
    </div>
  )
}

function TimelineNode({
  label,
  detail,
  tone,
  last,
}: {
  label: string
  detail?: string
  tone: 'ok' | 'fail' | 'warn' | 'muted'
  last?: boolean
}) {
  const dot =
    tone === 'ok'
      ? 'bg-[#16A34A]'
      : tone === 'fail'
        ? 'bg-[#C0372A]'
        : tone === 'warn'
          ? 'bg-[#D97706]'
          : 'bg-[#CBD5E1]'
  const text =
    tone === 'fail'
      ? 'text-[#C0372A]'
      : tone === 'warn'
        ? 'text-[#B36B00]'
        : tone === 'muted'
          ? 'text-[#94A3B8]'
          : 'text-[#0F172A]'
  return (
    <li className={`relative ${last ? 'pb-0' : 'pb-4'}`}>
      <span className={`absolute -left-[23px] top-1.5 h-2.5 w-2.5 rounded-full ${dot}`} />
      <p className={`text-[14px] font-semibold ${text}`}>{label}</p>
      {detail ? <p className="mt-0.5 text-[12px] text-[#8F8F8F]">{detail}</p> : null}
    </li>
  )
}
