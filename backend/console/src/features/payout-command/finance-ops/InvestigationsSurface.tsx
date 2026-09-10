'use client'

import { useEffect, useMemo, useState } from 'react'
import { useRouter } from 'next/navigation'
import { getFinanceInvestigations } from '@/services/payout-command/prod-api/financeApi'
import type { FinanceInvestigation } from '@/services/payout-command/prod-api/financeTypes'
import {
  HeroAmountCard,
  MiniMetricCard,
  PageHeader,
  PaymentsEmptyState,
  RZ_CARD,
  RZ_MUTED,
  RZ_PAGE,
  RZ_WRAP,
  StatusBadge,
} from './razorpayChrome'
import { formatPaise } from './reasonCopy'

const RANGE_OPTIONS = [
  { value: 'today', label: 'Today' },
  { value: '7d', label: 'Last 7 days' },
  { value: '30d', label: 'Last 30 days' },
  { value: 'all', label: 'All time' },
]

export function InvestigationsSurface() {
  const router = useRouter()
  const [range, setRange] = useState('all')
  const [rows, setRows] = useState<FinanceInvestigation[]>([])
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    void getFinanceInvestigations().then((res) => {
      setLoading(false)
      if (!res.ok || !res.data) {
        setError(res.status === 401 ? 'Sign in to load investigations.' : 'Could not load investigations.')
        return
      }
      setRows(res.data.investigations ?? [])
    })
  }, [])

  const stats = useMemo(() => {
    const total = rows.length
    const resolved = rows.filter((r) => String(r.status).toLowerCase() === 'completed').length
    const unresolved = total - resolved
    const impact = rows.reduce((s, r) => s + (r.financial_impact || 0), 0)
    const highConf = rows.filter((r) => (r.confidence || 0) >= 0.9).length
    return { total, resolved, unresolved, impact, highConf }
  }, [rows])

  return (
    <div className={RZ_PAGE}>
      <div className={RZ_WRAP}>
        <PageHeader
          title="Investigations"
          range={range}
          onRangeChange={setRange}
          rangeOptions={RANGE_OPTIONS}
          docsHref="https://razorpay.com/docs/x/payouts/"
        />
        <p className={`mt-1 ${RZ_MUTED}`}>
          Agentic finance controller — traces failure process, signal, and error-code origin. Provider status is
          never renamed. Failed lifecycles stop at Failed (no fake evidence seal).
        </p>

        <div className="mt-5 space-y-3">
          <HeroAmountCard
            label="Agent-investigated exposure"
            amount={formatPaise(stats.impact, 2)}
            subtitle={`${stats.total} cases · ${stats.highConf} high-confidence`}
            info="Sum of financial_impact on investigation cases."
          />
          <div className="grid gap-3 sm:grid-cols-3">
            <MiniMetricCard
              label="Agent complete"
              value={String(stats.resolved)}
              subtitle="finished tool runs"
              info="status = completed"
            />
            <MiniMetricCard
              label="Agent open"
              value={String(stats.unresolved)}
              subtitle="still gathering evidence"
              info="Unresolved investigations"
              warn
            />
            <MiniMetricCard
              label="Cases"
              value={String(stats.total)}
              subtitle="in the agent inbox"
              info="All investigation records"
            />
          </div>
        </div>

        {error ? (
          <p className="mt-6 rounded-[8px] border border-[#FECACA] bg-[#FEF2F2] px-4 py-3 text-[13px] text-[#B91C1C]">
            {error}
          </p>
        ) : null}

        <div className={`${RZ_CARD} mt-6 overflow-hidden`}>
          {loading ? (
            <p className={`px-6 py-10 text-center ${RZ_MUTED}`}>Loading agent inbox…</p>
          ) : rows.length === 0 ? (
            <PaymentsEmptyState
              title="No investigations"
              body="Open an exception or failed payout and run the AI investigation agent."
            />
          ) : (
            <ul className="divide-y divide-[#EEF0F3]">
              {rows.map((row) => {
                const open = String(row.status).toLowerCase() !== 'completed'
                const conf = Math.round((row.confidence || 0.86) * 100)
                return (
                  <li key={row.id}>
                    <button
                      type="button"
                      onClick={() =>
                        router.push(`/investigations/${encodeURIComponent(row.id)}`)
                      }
                      className="flex w-full items-start gap-4 px-5 py-4 text-left transition hover:bg-[#FAFBFC]"
                    >
                      <div className="min-w-0 flex-1">
                        <div className="flex flex-wrap items-center gap-2">
                          <p className="font-mono text-[13px] font-semibold text-[#1A1A1A]">{row.id}</p>
                          <StatusBadge tone={open ? 'pending' : 'captured'}>
                            {open ? 'Agent open' : 'Agent complete'}
                          </StatusBadge>
                          <span className="rounded-full bg-[#F1F5F9] px-2 py-0.5 text-[11px] font-semibold tabular-nums text-[#475569]">
                            {conf}% conf
                          </span>
                        </div>
                        <p className="mt-1 text-[14px] font-medium text-[#0F172A]">
                          {row.issue || row.root_cause}
                        </p>
                        <p className={`mt-1 ${RZ_MUTED}`}>
                          <span className="font-mono text-[12px]">{row.entity_id}</span>
                          <span className="mx-1.5 text-[#D0D4DA]">·</span>
                          {row.entity_type || 'payout'}
                          <span className="mx-1.5 text-[#D0D4DA]">·</span>
                          Impact {formatPaise(row.financial_impact, 2)}
                        </p>
                        <p className="mt-1 text-[12px] text-[#64748B]">
                          {open
                            ? 'Click to run live agent · tool calls · failure forensics'
                            : 'View agent report · hypotheses · stop-at-failure lifecycle'}
                        </p>
                      </div>
                      <span className="shrink-0 text-[13px] font-medium text-[#528FF0]">Open agent →</span>
                    </button>
                  </li>
                )
              })}
            </ul>
          )}
        </div>
      </div>
    </div>
  )
}
