'use client'

import { useEffect, useMemo, useState } from 'react'
import { useRouter } from 'next/navigation'
import {
  getFinanceCashPosition,
  getFinanceCashSchedule,
  getFinanceExceptions,
  getFinanceResults,
} from '@/services/payout-command/prod-api/financeApi'
import type {
  FinanceCashPosition,
  FinanceCashSchedule,
  FinanceException,
  FinanceReconRow,
} from '@/services/payout-command/prod-api/financeTypes'
import { InfoDot, RZ_CARD, RZ_MUTED, RZ_PAGE } from './razorpayChrome'
import { formatPaise } from './reasonCopy'
import { mapFinanceRowToPayoutRecon } from './payoutReconCopy'

type BottomTab = 'inflows' | 'outflows' | 'settlements' | 'payouts'

const BOTTOM_TABS: { id: BottomTab; label: string }[] = [
  { id: 'inflows', label: 'Expected Inflows' },
  { id: 'outflows', label: 'Committed Outflows' },
  { id: 'settlements', label: 'Pending Settlements' },
  { id: 'payouts', label: 'Scheduled Payouts' },
]

function typeBadge(tone: 'settlement' | 'payout', label: string) {
  const cls = tone === 'settlement' ? 'bg-[#E8F8EE] text-[#147A3F]' : 'bg-[#EEF4FF] text-[#2B6CB0]'
  return (
    <span className={`inline-flex h-6 items-center rounded-[4px] px-2 text-[11px] font-semibold ${cls}`}>{label}</span>
  )
}

function KpiCard({
  label,
  value,
  hint,
  warn,
  onClick,
}: {
  label: string
  value: string
  hint: string
  warn?: boolean
  onClick?: () => void
}) {
  const inner = (
    <>
      <div className="flex items-center gap-1.5">
        <p className="text-[12px] font-medium text-[#6B6B6B]">{label}</p>
        <InfoDot label={label} />
      </div>
      <p className="mt-2 text-[20px] font-semibold tabular-nums tracking-[-0.02em] text-[#1A1A1A] sm:text-[22px]">
        {value}
      </p>
      <p className={`mt-1 ${RZ_MUTED}`}>{hint}</p>
    </>
  )
  if (onClick) {
    return (
      <button type="button" onClick={onClick} className={`${RZ_CARD} px-4 py-3.5 text-left transition hover:border-[#D5D8DE]`}>
        {inner}
      </button>
    )
  }
  return <div className={`${RZ_CARD} px-4 py-3.5`}>{inner}</div>
}

export function CashPositionSurface() {
  const router = useRouter()
  const [bottomTab, setBottomTab] = useState<BottomTab>('inflows')
  const [cash, setCash] = useState<FinanceCashPosition | null>(null)
  const [schedule, setSchedule] = useState<FinanceCashSchedule | null>(null)
  const [rows, setRows] = useState<FinanceReconRow[]>([])
  const [exceptions, setExceptions] = useState<FinanceException[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    void Promise.all([
      getFinanceCashPosition(),
      getFinanceCashSchedule(),
      getFinanceResults('ALL'),
      getFinanceExceptions(),
    ]).then(([pos, sch, recon, ex]) => {
      if (cancelled) return
      if (!pos.ok || !pos.data) {
        setError(pos.status === 401 ? 'Sign in to load cash position.' : 'Could not load cash position.')
        setLoading(false)
        return
      }
      setCash(pos.data)
      setSchedule(sch.ok ? sch.data : null)
      setRows(recon.ok ? recon.data?.results ?? [] : [])
      setExceptions(ex.ok ? ex.data?.exceptions ?? [] : [])
      setLoading(false)
    })
    return () => {
      cancelled = true
    }
  }, [])

  const payouts = useMemo(() => rows.map(mapFinanceRowToPayoutRecon), [rows])
  const committedOut = useMemo(
    () =>
      payouts
        .filter((r) => {
          const s = String(r.status || '').toLowerCase()
          return s === 'queued' || s === 'processing' || s === 'pending' || s === 'scheduled'
        })
        .reduce((sum, r) => s + r.amountMinor, 0),
    [payouts],
  )
  const expectedIn = cash?.in_flight_minor ?? 0
  const available = cash?.bank_credited_proven_minor ?? 0
  const unresolved = cash?.unresolved_exposure_minor ?? 0
  const projected =
    available +
    (schedule?.days ?? []).reduce((sum, d) => sum + (d.expected_credit_minor || 0) - (d.expected_debit_minor || 0), 0)

  const tableRows = useMemo(() => {
    if (bottomTab === 'payouts' || bottomTab === 'outflows') {
      return payouts
        .filter((r) => {
          const s = String(r.status || '').toLowerCase()
          return s === 'queued' || s === 'processing' || s === 'pending' || s === 'scheduled' || s === 'processed'
        })
        .map((r) => ({
          type: 'Payout' as const,
          typeTone: 'payout' as const,
          source: r.payoutId,
          count: 1,
          amountMinor: r.amountMinor,
          date: r.createdAt ? new Date(r.createdAt * 1000).toLocaleDateString('en-IN') : '—',
          description: r.mode || r.purpose || 'payout',
          status: r.status || '—',
        }))
    }
    return (schedule?.days ?? [])
      .filter((d) => (d.expected_credit_minor || 0) > 0)
      .map((d) => ({
        type: 'Settlement' as const,
        typeTone: 'settlement' as const,
        source: 'Expected bank credit',
        count: d.count,
        amountMinor: d.expected_credit_minor,
        date: d.date,
        description: 'From cash schedule (settled, bank not yet proven)',
        status: 'Expected',
      }))
  }, [bottomTab, payouts, schedule])

  return (
    <div className={RZ_PAGE}>
      <div className="mx-auto w-full max-w-[1280px] px-5 py-6 sm:px-8">
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div>
            <div className="flex items-center gap-2">
              <h1 className="text-[22px] font-semibold tracking-[-0.02em] text-[#1A1A1A]">Cash Position</h1>
              <InfoDot label="Bank-proven cash, in-flight settlement net, and payout book from recon." />
            </div>
            <p className={`mt-1 ${RZ_MUTED}`}>Proven bank credit vs expected settlement and open payouts. No forecast.</p>
          </div>
        </div>

        {loading ? (
          <p className={`mt-8 ${RZ_MUTED}`}>Loading cash position…</p>
        ) : error ? (
          <p className="mt-8 text-[13px] text-[#B91C1C]">{error}</p>
        ) : (
          <>
            <div className="mt-5 grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
              <KpiCard label="Available Cash" value={formatPaise(available, 2)} hint="Bank credited, proven" />
              <KpiCard label="Expected Incoming" value={formatPaise(expectedIn, 2)} hint="Settled, not yet proven at bank" />
              <KpiCard label="Committed Outflows" value={formatPaise(committedOut, 2)} hint="Open payouts in the book" />
              <KpiCard
                label="Unresolved Exposure"
                value={formatPaise(unresolved, 2)}
                hint={`${exceptions.length} exceptions`}
                warn
                onClick={() => router.push('/exceptions')}
              />
              <KpiCard
                label="Projected Cash (schedule)"
                value={formatPaise(projected, 2)}
                hint={`${schedule?.horizon_days ?? 7}-day cash schedule, not a forecast`}
              />
            </div>

            <div className="mt-4 grid gap-4 lg:grid-cols-[1.4fr_1fr]">
              <section className={`${RZ_CARD} px-5 py-4`}>
                <h2 className="text-[15px] font-semibold text-[#1A1A1A]">Cash schedule</h2>
                {schedule?.days?.length ? (
                  <ul className="mt-3 space-y-2 text-[13px]">
                    {schedule.days.map((d) => (
                      <li key={d.date} className="flex items-center justify-between border-b border-[#F1F5F9] py-2">
                        <span className="text-[#6B6B6B]">{d.date}</span>
                        <span className="tabular-nums text-[#1A1A1A]">
                          +{formatPaise(d.expected_credit_minor, 2)} · −{formatPaise(d.expected_debit_minor, 2)}
                        </span>
                      </li>
                    ))}
                  </ul>
                ) : (
                  <p className={`mt-3 ${RZ_MUTED}`}>No dated settlement window on this connector.</p>
                )}
                {schedule?.limitations?.[0] ? <p className={`mt-3 ${RZ_MUTED}`}>{schedule.limitations[0]}</p> : null}
              </section>

              <section className={`${RZ_CARD} px-5 py-4`}>
                <h2 className="text-[15px] font-semibold text-[#1A1A1A]">Cash position summary</h2>
                <ul className="mt-3 space-y-0 text-[13px]">
                  <li className="flex items-center justify-between border-b border-[#F1F5F9] py-2.5">
                    <span className="text-[#6B6B6B]">Gross captured</span>
                    <span className="tabular-nums font-medium text-[#1A1A1A]">
                      {formatPaise(cash?.gross_captured_minor, 2)}
                    </span>
                  </li>
                  <li className="flex items-center justify-between border-b border-[#F1F5F9] py-2.5">
                    <span className="text-[#6B6B6B]">Settlement expected net</span>
                    <span className="tabular-nums font-medium text-[#147A3F]">
                      {formatPaise(cash?.settlement_expected_net_minor, 2)}
                    </span>
                  </li>
                  <li className="flex items-center justify-between border-b border-[#F1F5F9] py-2.5">
                    <span className="text-[#6B6B6B]">Bank credited proven</span>
                    <span className="tabular-nums font-medium text-[#147A3F]">{formatPaise(available, 2)}</span>
                  </li>
                  <li className="flex items-center justify-between border-b border-[#F1F5F9] py-2.5">
                    <span className="text-[#6B6B6B]">In flight</span>
                    <span className="tabular-nums font-medium text-[#1A1A1A]">{formatPaise(expectedIn, 2)}</span>
                  </li>
                  <li className="flex items-center justify-between border-t border-[#E6E8EB] pt-3">
                    <span className="font-semibold text-[#1A1A1A]">Unresolved exposure</span>
                    <span className="text-[15px] font-semibold tabular-nums text-[#C0372A]">
                      {formatPaise(unresolved, 2)}
                    </span>
                  </li>
                </ul>
              </section>
            </div>

            <section className={`${RZ_CARD} mt-4 overflow-hidden`}>
              <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[#EEF0F3] px-5 py-4">
                <h2 className="text-[15px] font-semibold text-[#1A1A1A]">Expected inflows &amp; committed outflows</h2>
                <button
                  type="button"
                  onClick={() => router.push('/settlements')}
                  className="text-[13px] font-medium text-[#2563EB] hover:underline"
                >
                  View All →
                </button>
              </div>
              <div className="flex gap-5 overflow-x-auto border-b border-[#EEF0F3] px-5">
                {BOTTOM_TABS.map((t) => {
                  const on = t.id === bottomTab
                  return (
                    <button
                      key={t.id}
                      type="button"
                      onClick={() => setBottomTab(t.id)}
                      className={`-mb-px whitespace-nowrap border-b-2 pb-3 pt-3 text-[13px] ${
                        on
                          ? 'border-[#1A1A1A] font-semibold text-[#1A1A1A]'
                          : 'border-transparent font-medium text-[#6B6B6B] hover:text-[#1A1A1A]'
                      }`}
                    >
                      {t.label}
                    </button>
                  )
                })}
              </div>
              <div className="overflow-x-auto">
                {tableRows.length === 0 ? (
                  <p className={`px-5 py-8 ${RZ_MUTED}`}>No captured rows for this tab.</p>
                ) : (
                  <table className="w-full min-w-[960px] text-left text-[13px]">
                    <thead className="bg-[#FAFBFC] text-[11px] font-semibold uppercase tracking-[0.06em] text-[#8F8F8F]">
                      <tr>
                        <th className="px-5 py-3">Type</th>
                        <th className="px-4 py-3">Source</th>
                        <th className="px-4 py-3 text-right">Count</th>
                        <th className="px-4 py-3 text-right">Amount</th>
                        <th className="px-4 py-3">Date</th>
                        <th className="px-4 py-3">Description</th>
                        <th className="px-5 py-3">Status</th>
                      </tr>
                    </thead>
                    <tbody>
                      {tableRows.map((row) => (
                        <tr key={`${row.source}-${row.date}`} className="border-t border-[#F3F4F6] hover:bg-[#FAFBFC]">
                          <td className="px-5 py-3">{typeBadge(row.typeTone, row.type)}</td>
                          <td className="px-4 py-3 font-medium text-[#1A1A1A]">{row.source}</td>
                          <td className="px-4 py-3 text-right tabular-nums text-[#334155]">
                            {row.count.toLocaleString('en-IN')}
                          </td>
                          <td className="px-4 py-3 text-right font-medium tabular-nums text-[#1A1A1A]">
                            {formatPaise(row.amountMinor, 2)}
                          </td>
                          <td className="px-4 py-3 text-[#6B6B6B]">{row.date}</td>
                          <td className="px-4 py-3 text-[#6B6B6B]">{row.description}</td>
                          <td className="px-5 py-3">
                            <span className="inline-flex h-6 items-center rounded-[4px] bg-[#EEF4FF] px-2 text-[11px] font-semibold text-[#2B6CB0]">
                              {row.status}
                            </span>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                )}
              </div>
            </section>
          </>
        )}
      </div>
    </div>
  )
}
