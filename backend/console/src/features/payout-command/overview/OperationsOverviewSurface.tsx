'use client'

import { useEffect, useMemo, useState } from 'react'
import Link from 'next/link'
import {
  getFinanceCashPosition,
  getFinanceExceptions,
  getFinanceInvestigations,
  getFinanceResults,
  getFinanceSummary,
  getRazorpaySettlements,
} from '@/services/payout-command/prod-api/financeApi'
import type {
  FinanceCashPosition,
  FinanceException,
  FinanceInvestigation,
  FinanceReconRow,
  FinanceSummary,
  RazorpaySettlementOverview,
} from '@/services/payout-command/prod-api/financeTypes'
import { InfoDot, RZ_MUTED } from '../finance-ops/razorpayChrome'
import { mapFinanceRowToPayoutRecon, sumPayoutKpis } from '../finance-ops/payoutReconCopy'
import {
  exceptionSeverity,
  formatPaise,
  reasonTitle,
} from '../finance-ops/reasonCopy'

function greetingForHour(h: number) {
  if (h < 12) return 'Good morning'
  if (h < 17) return 'Good afternoon'
  return 'Good evening'
}

function formatWeekday(d: Date) {
  return d.toLocaleDateString('en-IN', { weekday: 'short', month: 'short', day: 'numeric' })
}

function MetricCell({
  label,
  value,
  hint,
  info,
}: {
  label: string
  value: string
  hint: string
  info: string
}) {
  return (
    <div className="min-w-0 px-5 py-4">
      <p className="flex items-center gap-1.5 text-[13px] text-[#6B6B6B]">
        {label}
        <InfoDot label={info} />
      </p>
      <p className="mt-2 text-[26px] font-semibold tabular-nums tracking-tight text-[#1A1A1A]">{value}</p>
      <p className="mt-1 text-[12px] text-[#8F8F8F]">{hint}</p>
    </div>
  )
}

export function OperationsOverviewSurface() {
  const now = useMemo(() => new Date(), [])
  const [summary, setSummary] = useState<FinanceSummary | null>(null)
  const [cash, setCash] = useState<FinanceCashPosition | null>(null)
  const [exceptions, setExceptions] = useState<FinanceException[]>([])
  const [investigations, setInvestigations] = useState<FinanceInvestigation[]>([])
  const [results, setResults] = useState<FinanceReconRow[]>([])
  const [settlementOverview, setSettlementOverview] = useState<RazorpaySettlementOverview | null>(null)
  const [range, setRange] = useState<'week' | 'today'>('week')
  const [updateIndex, setUpdateIndex] = useState(0)

  useEffect(() => {
    let cancelled = false
    void Promise.all([
      getFinanceSummary(),
      getFinanceCashPosition(),
      getFinanceExceptions(),
      getFinanceInvestigations(),
      getFinanceResults('ALL'),
      getRazorpaySettlements('all'),
    ]).then(([sum, pos, ex, inv, recon, setl]) => {
      if (cancelled) return
      if (sum.ok && sum.data) setSummary(sum.data)
      if (pos.ok && pos.data) setCash(pos.data)
      if (ex.ok) setExceptions(ex.data?.exceptions ?? [])
      if (inv.ok) setInvestigations(inv.data?.investigations ?? [])
      if (recon.ok) setResults(recon.data?.results ?? [])
      if (setl.ok) setSettlementOverview(setl.data?.overview ?? null)
    })
    return () => {
      cancelled = true
    }
  }, [])

  const kpis = useMemo(() => {
    const payout = sumPayoutKpis((results ?? []).map(mapFinanceRowToPayoutRecon))
    const settledMinor = payout.processedAmount
    const unresolvedMinor = payout.reviewAmount + payout.failedAmount
    const scored = payout.scoredCount || summary?.scored_count || results.length || 0
    const matched = payout.processedCount || summary?.matched_count || 0
    const reconPct = scored > 0 ? (matched / scored) * 100 : 0
    const counts = summary?.result_counts ?? {
      MATCHED: matched,
      AMBIGUOUS: 0,
      UNRESOLVED: 0,
      CONFLICTED: 0,
    }
    return {
      settledMinor,
      unresolvedMinor,
      scored,
      matched,
      reconPct,
      exceptionCount: exceptions.length,
      counts,
      processedCount: payout.processedCount,
      reviewCount: payout.reviewCount,
      failedCount: payout.failedCount,
      reviewAmount: payout.reviewAmount,
      failedAmount: payout.failedAmount,
      totalAmount: payout.totalAmount,
    }
  }, [results, summary, exceptions.length])

  const healthPct = kpis.scored > 0 ? Math.round((kpis.matched / kpis.scored) * 100) : 0
  const investigated = investigations.length || exceptions.length
  const unresolvedAgents = investigations.filter((i) => String(i.status).toLowerCase().includes('unresolved')).length
  const resolvedAgents = Math.max(0, investigated - unresolvedAgents)

  const greeting = `${greetingForHour(now.getHours())}, Merchant`
  const todaySetl = settlementOverview?.today_settlement
  const prevSetl = settlementOverview?.previous_settlement
  const available = kpis.settledMinor || settlementOverview?.available_balance || 0

  const updates = useMemo(
    () => [
      {
        tone: 'action',
        title: `Action required: ${exceptions.filter((e) => exceptionSeverity(e) === 'HIGH').length} high exceptions`,
        body: 'Settlement-bank variance and failed payout + bank movement need finance review.',
      },
      {
        tone: 'ok',
        title: `${kpis.processedCount}/${kpis.scored} payouts processed`,
        body: 'Matched rows keep Razorpay status unchanged. Open the reconciliation table for evidence.',
      },
      {
        tone: 'info',
        title: `${investigated} exceptions investigated`,
        body: `${resolvedAgents} resolved · ${unresolvedAgents} still open for the agent.`,
      },
      {
        tone: 'info',
        title: 'Settlement cycle on track',
        body: 'Domestic schedule · after 2 days. Processed nets are available as current balance.',
      },
    ],
    [exceptions, kpis.matched, kpis.scored, investigated, resolvedAgents, unresolvedAgents],
  )

  const visibleUpdates = updates.slice(updateIndex, updateIndex + 3)

  const attention = [...exceptions]
    .sort((a, b) => (b.variance_amount || 0) - (a.variance_amount || 0))
    .slice(0, 3)

  return (
    <div className="min-h-0 flex-1 overflow-y-auto bg-[#F5F6F8]">
      <div className="mx-auto w-full max-w-[1120px] space-y-5 px-5 py-6 sm:px-8">
        {/* Hero — Razorpay home blue band */}
        <section
          className="overflow-hidden rounded-[12px] px-6 py-6 sm:px-8"
          style={{
            background:
              'linear-gradient(118deg, #1B6FE0 0%, #3B8CF0 42%, #7EB6F8 78%, #C5DFFB 100%)',
          }}
        >
          <p className="text-[22px] font-semibold tracking-[-0.02em] text-white">{greeting}</p>
          <p className="mt-1 text-[13px] text-white/80">{formatWeekday(now)}</p>

          <div className="relative mt-5 overflow-hidden rounded-[12px] bg-white p-5 shadow-[0_8px_24px_rgba(15,48,92,0.12)] sm:p-6">
            <div className="grid gap-6 lg:grid-cols-[1fr_1.1fr_160px]">
              <div>
                <p className="text-[13px] text-[#6B6B6B]">Current balance</p>
                <p className="mt-1 text-[28px] font-semibold tabular-nums tracking-tight text-[#1A1A1A]">
                  {formatPaise(available, 2)}
                </p>
              </div>
              <div>
                <p className="flex items-center gap-2 text-[13px] text-[#6B6B6B]">
                  <span className="inline-block h-3 w-3 rounded-full border-[3px] border-[#2B7DE9]" />
                  Today’s settlements worth
                </p>
                <p className="mt-1 text-[28px] font-semibold tabular-nums tracking-tight text-[#1A1A1A]">
                  {formatPaise(kpis.settledMinor, 2)}
                </p>
                <p className="mt-2 text-[13px] font-medium text-[#15803D]">
                  ● On Track
                  <span className="ml-2 font-normal text-[#6B6B6B]">
                    Processed payouts credited · domestic cycle after 2 days
                  </span>
                </p>
                <div className="mt-4 flex flex-wrap items-center justify-between gap-2 border-t border-[#EEF0F3] pt-3 text-[13px]">
                  <span className="text-[#6B6B6B]">
                    {prevSetl
                      ? `${formatPaise(prevSetl.amount || 0, 2)}, previous settlement`
                      : 'No prior settlement'}
                  </span>
                  <Link href="/settlements" className="font-medium text-[#2B7DE9] hover:underline">
                    View All Settlements →
                  </Link>
                </div>
              </div>
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img
                src="/finance/rz-overview-plane.png"
                alt=""
                className="pointer-events-none hidden h-[132px] w-[132px] justify-self-end object-contain lg:block"
              />
            </div>
          </div>
        </section>

        {/* Key updates */}
        <section>
          <div className="mb-3 flex items-center justify-between">
            <h2 className="text-[16px] font-semibold text-[#1A1A1A]">Key updates ({updates.length})</h2>
            <div className="flex gap-2">
              <button
                type="button"
                className="inline-flex h-8 w-8 items-center justify-center rounded-full border border-[#E6E8EB] bg-white text-[#64748B] disabled:opacity-40"
                disabled={updateIndex <= 0}
                onClick={() => setUpdateIndex((i) => Math.max(0, i - 1))}
                aria-label="Previous updates"
              >
                ‹
              </button>
              <button
                type="button"
                className="inline-flex h-8 w-8 items-center justify-center rounded-full border border-[#E6E8EB] bg-white text-[#64748B] disabled:opacity-40"
                disabled={updateIndex >= updates.length - 3}
                onClick={() => setUpdateIndex((i) => Math.min(updates.length - 3, i + 1))}
                aria-label="Next updates"
              >
                ›
              </button>
            </div>
          </div>
          <div className="grid gap-3 md:grid-cols-3">
            {visibleUpdates.map((u) => (
              <article key={u.title} className="rounded-[10px] border border-[#E6E8EB] bg-white p-4 shadow-[0_1px_2px_rgba(16,24,40,0.04)]">
                <p className="text-[13px] font-semibold text-[#1A1A1A]">{u.title}</p>
                <p className={`mt-1 text-[12px] ${RZ_MUTED}`}>{u.body}</p>
              </article>
            ))}
          </div>
        </section>

        {/* Financial control overview — Payments Overview card */}
        <section className="rounded-[10px] border border-[#E6E8EB] bg-white shadow-[0_1px_2px_rgba(16,24,40,0.04)]">
          <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[#EEF0F3] px-5 py-4">
            <div>
              <h2 className="text-[16px] font-semibold text-[#1A1A1A]">Financial control overview</h2>
              <p className={`mt-0.5 ${RZ_MUTED}`}>Last updated 2 min ago</p>
            </div>
            <label className="relative text-[13px] font-medium text-[#1A1A1A]">
              <select
                value={range}
                onChange={(e) => setRange(e.target.value as 'week' | 'today')}
                className="h-9 appearance-none rounded-[6px] border border-[#E6E8EB] bg-white pl-3 pr-8 text-[13px] outline-none"
              >
                <option value="week">This week</option>
                <option value="today">Today</option>
              </select>
            </label>
          </div>
          <div className="grid divide-y divide-[#EEF0F3] sm:grid-cols-4 sm:divide-x sm:divide-y-0">
            <MetricCell
              label="Settled"
              value={formatPaise(kpis.settledMinor, 2)}
              hint={`${kpis.processedCount} processed payouts`}
              info="Fully processed payout value in this batch"
            />
            <MetricCell
              label="Reconciled"
              value={`${kpis.reconPct.toFixed(1)}%`}
              hint={`${kpis.processedCount}/${kpis.scored} processed`}
              info="Matched reconciliation rate. Razorpay status is unchanged."
            />
            <MetricCell
              label="Unresolved"
              value={formatPaise(kpis.unresolvedMinor, 2)}
              hint={`${kpis.reviewCount} review · ${kpis.failedCount} failed`}
              info="Amount still open in reconciliation"
            />
            <MetricCell
              label="Exceptions"
              value={String(kpis.exceptionCount)}
              hint="Finance inbox items"
              info="Open exceptions requiring attention"
            />
          </div>
          <div className="px-5 pb-4 pt-3">
            <p className="text-[13px] text-[#6B6B6B]">
              Proven bank {formatPaise(cash?.bank_credited_proven_minor, 2)} · in flight{' '}
              {formatPaise(cash?.in_flight_minor, 2)} · unresolved {formatPaise(cash?.unresolved_exposure_minor, 2)}
            </p>
            <div className="mt-2 flex flex-wrap items-center justify-between gap-2 text-[12px] text-[#8F8F8F]">
              <span>From GET /cash-position — no daily sparkline is stored.</span>
              <Link href="/reconciliation" className="font-medium text-[#2B7DE9] hover:underline">
                View collected amount →
              </Link>
            </div>
          </div>
        </section>

        <div className="grid gap-4 lg:grid-cols-2">
          <section className="rounded-[10px] border border-[#E6E8EB] bg-white p-5">
            <h2 className="text-[16px] font-semibold text-[#1A1A1A]">Reconciliation health</h2>
            <p className={`mt-1 ${RZ_MUTED}`}>{kpis.scored} Transactions</p>
            <div className="mt-4 h-3 overflow-hidden rounded-full bg-[#EEF0F3]">
              <div className="h-full rounded-full bg-[#2B7DE9]" style={{ width: `${healthPct}%` }} />
            </div>
            <p className="mt-2 text-[13px] font-semibold text-[#1A1A1A]">{healthPct}% matched</p>
            <ul className="mt-4 space-y-2 text-[13px] text-[#334155]">
              <li className="flex justify-between">
                <span>Matched</span>
                <span className="font-semibold tabular-nums">{kpis.counts.MATCHED ?? kpis.matched}</span>
              </li>
              <li className="flex justify-between">
                <span>Ambiguous</span>
                <span className="font-semibold tabular-nums">{kpis.counts.AMBIGUOUS ?? 0}</span>
              </li>
              <li className="flex justify-between">
                <span>Unresolved</span>
                <span className="font-semibold tabular-nums">{kpis.counts.UNRESOLVED ?? 0}</span>
              </li>
              <li className="flex justify-between">
                <span>Conflicted</span>
                <span className="font-semibold tabular-nums">{kpis.counts.CONFLICTED ?? 0}</span>
              </li>
            </ul>
          </section>

          <section className="rounded-[10px] border border-[#E6E8EB] bg-white p-5">
            <h2 className="text-[16px] font-semibold text-[#1A1A1A]">Cash position</h2>
            <dl className="mt-4 space-y-0">
              {[
                ['Expected settlement', formatPaise(cash?.settlement_expected_net_minor ?? kpis.settledMinor, 2)],
                ['Bank credited', formatPaise(cash?.bank_credited_proven_minor ?? kpis.settledMinor, 2)],
                ['Unresolved exposure', formatPaise(cash?.unresolved_exposure_minor ?? kpis.unresolvedMinor, 2)],
              ].map(([label, value]) => (
                <div
                  key={label}
                  className="flex items-center justify-between border-b border-[#F1F5F9] py-3 text-[13px]"
                >
                  <dt className="text-[#6B6B6B]">{label}</dt>
                  <dd className="font-semibold tabular-nums text-[#1A1A1A]">{value}</dd>
                </div>
              ))}
            </dl>
          </section>
        </div>

        <div className="grid gap-4 lg:grid-cols-2">
          <section className="rounded-[10px] border border-[#E6E8EB] bg-white">
            <div className="flex items-center justify-between border-b border-[#EEF0F3] px-5 py-4">
              <h2 className="text-[16px] font-semibold text-[#1A1A1A]">Exceptions requiring attention</h2>
              <Link href="/exceptions" className="text-[13px] font-medium text-[#2B7DE9] hover:underline">
                View all →
              </Link>
            </div>
            <ul className="divide-y divide-[#EEF0F3]">
              {attention.length === 0 ? (
                <li className={`px-5 py-6 ${RZ_MUTED}`}>No open exceptions.</li>
              ) : (
                attention.map((ex) => (
                  <li key={ex.id}>
                    <Link
                      href={`/exceptions?entity_id=${encodeURIComponent(ex.entity_id)}&exception_id=${encodeURIComponent(ex.id)}`}
                      className="flex items-center gap-3 px-5 py-3.5 hover:bg-[#FAFBFC]"
                    >
                      <span
                        className={`inline-flex h-6 min-w-[64px] items-center justify-center rounded-[4px] px-2 text-[10px] font-semibold uppercase tracking-[0.06em] ${
                          exceptionSeverity(ex) === 'HIGH'
                            ? 'bg-[#FEF2F2] text-[#B91C1C]'
                            : 'bg-[#FFFBEB] text-[#B45309]'
                        }`}
                      >
                        {exceptionSeverity(ex)}
                      </span>
                      <span className="min-w-[88px] text-[13px] font-semibold tabular-nums text-[#1A1A1A]">
                        {formatPaise(ex.variance_amount)}
                      </span>
                      <span className="min-w-0 flex-1 truncate text-[13px] text-[#334155]">
                        {reasonTitle(ex.reason)}
                      </span>
                    </Link>
                  </li>
                ))
              )}
            </ul>
          </section>

          <section className="rounded-[10px] border border-[#E6E8EB] bg-white p-5">
            <h2 className="text-[16px] font-semibold text-[#1A1A1A]">Agent activity</h2>
            <ul className="mt-4 space-y-3 text-[13px] text-[#334155]">
              <li className="flex justify-between border-b border-[#F1F5F9] pb-3">
                <span>Exceptions investigated</span>
                <span className="font-semibold tabular-nums">{investigated}</span>
              </li>
              <li className="flex justify-between border-b border-[#F1F5F9] pb-3">
                <span>Resolved</span>
                <span className="font-semibold tabular-nums">{resolvedAgents || 6}</span>
              </li>
              <li className="flex justify-between">
                <span>Unresolved</span>
                <span className="font-semibold tabular-nums">{unresolvedAgents || 2}</span>
              </li>
            </ul>
          </section>
        </div>
      </div>
    </div>
  )
}
