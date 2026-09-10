'use client'

import { useCallback, useEffect, useMemo, useState } from 'react'
import { useRouter, useSearchParams } from 'next/navigation'
import { getFinanceExceptions, getFinanceResults, getFinanceSummary } from '@/services/payout-command/prod-api/financeApi'
import type { FinanceException, FinanceSummary } from '@/services/payout-command/prod-api/financeTypes'
import { PaymentDrawer } from './PaymentDrawer'
import { reconToneClass } from './payoutLifecycleModel'
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
  UnderlineTabs,
} from './razorpayChrome'
import {
  exceptionSeverity,
  formatPaise,
  reasonTitle,
  reconLabel,
  type ExceptionSeverity,
} from './reasonCopy'
import { mapFinanceRowToPayoutRecon, sumPayoutKpis } from './payoutReconCopy'

type FilterId = 'all' | 'high' | 'payment' | 'settlement' | 'payout' | 'bank'

const FILTERS: { id: FilterId; label: string }[] = [
  { id: 'all', label: 'All' },
  { id: 'high', label: 'High' },
  { id: 'payment', label: 'Payment' },
  { id: 'settlement', label: 'Settlement' },
  { id: 'payout', label: 'Payout' },
  { id: 'bank', label: 'Bank' },
]

const RANGE_OPTIONS = [
  { value: 'today', label: 'Today' },
  { value: '7d', label: 'Last 7 days' },
  { value: '30d', label: 'Last 30 days' },
  { value: 'all', label: 'All time' },
]

function matchesFilter(ex: FinanceException, filter: FilterId): boolean {
  if (filter === 'all') return true
  if (filter === 'high') return exceptionSeverity(ex) === 'HIGH'
  return ex.entity_type === filter
}

function severityTone(sev: ExceptionSeverity): 'captured' | 'pending' | 'failed' | 'created' {
  if (sev === 'HIGH') return 'failed'
  if (sev === 'MEDIUM') return 'pending'
  return 'created'
}

export function ExceptionsSurface() {
  const router = useRouter()
  const searchParams = useSearchParams()
  const [range, setRange] = useState('today')
  const [exceptions, setExceptions] = useState<FinanceException[]>([])
  const [summary, setSummary] = useState<FinanceSummary | null>(null)
  const [kpis, setKpis] = useState(sumPayoutKpis([]))
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [filter, setFilter] = useState<FilterId>('all')
  const [search, setSearch] = useState('')

  const entityFromUrl = searchParams.get('entity_id')?.trim() || ''
  const exceptionFromUrl = searchParams.get('exception_id')?.trim() || ''
  const [openId, setOpenId] = useState<string>(entityFromUrl)

  useEffect(() => {
    setOpenId(entityFromUrl)
  }, [entityFromUrl])

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    const [exRes, sumRes, reconRes] = await Promise.all([
      getFinanceExceptions(),
      getFinanceSummary(),
      getFinanceResults('ALL'),
    ])
    if (!exRes.ok) {
      setError(
        exRes.status === 401
          ? 'Sign in to load exceptions.'
          : 'Could not load finance exceptions.',
      )
      setExceptions([])
      setSummary(null)
      setKpis(sumPayoutKpis([]))
      setLoading(false)
      return
    }
    setExceptions(exRes.data?.exceptions ?? [])
    setSummary(sumRes.data)
    const mapped = (reconRes.data?.results ?? []).map(mapFinanceRowToPayoutRecon)
    setKpis(sumPayoutKpis(mapped))
    setLoading(false)
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const visible = useMemo(() => {
    const q = search.trim().toLowerCase()
    return exceptions.filter((ex) => {
      if (!matchesFilter(ex, filter)) return false
      if (!q) return true
      const hay = [
        ex.id,
        ex.entity_id,
        ex.entity_type,
        ex.reason,
        ex.reconciliation_result,
        reasonTitle(ex.reason),
      ]
        .filter(Boolean)
        .join(' ')
        .toLowerCase()
      return hay.includes(q)
    })
  }, [exceptions, filter, search])

  const highCount = exceptions.filter((ex) => exceptionSeverity(ex) === 'HIGH').length
  const processedCount = kpis.processedCount || summary?.payout_kpis?.processed_count || summary?.matched_count || 0
  const scored = kpis.scoredCount || summary?.scored_count || 0

  function openRow(ex: FinanceException) {
    setOpenId(ex.entity_id)
    const params = new URLSearchParams(searchParams.toString())
    params.set('entity_id', ex.entity_id)
    params.set('exception_id', ex.id)
    router.replace(`/exceptions?${params.toString()}`, { scroll: false })
  }

  function closeDrawer() {
    setOpenId('')
    const params = new URLSearchParams(searchParams.toString())
    params.delete('entity_id')
    params.delete('exception_id')
    const q = params.toString()
    router.replace(q ? `/exceptions?${q}` : '/exceptions', { scroll: false })
  }

  const openException =
    exceptions.find((ex) => ex.entity_id === openId) ??
    exceptions.find((ex) => ex.id === exceptionFromUrl)

  return (
    <div className={RZ_PAGE}>
      <div className={`${RZ_WRAP} ${openId ? 'pr-[min(480px,100%)]' : ''}`}>
        <PageHeader
          title="Exceptions"
          range={range}
          onRangeChange={setRange}
          rangeOptions={RANGE_OPTIONS}
          docsHref="https://razorpay.com/docs/payments/"
        />
        <p className={`mt-1 ${RZ_MUTED}`}>
          Finance operations inbox. Razorpay status stays unchanged — reconciliation is separate.
          {exceptions.length ? ` ${exceptions.length} open exceptions.` : ''}
        </p>

        <div className="mt-5 space-y-3">
          <HeroAmountCard
            label="Unresolved exposure"
            amount={formatPaise(kpis.failedAmount + kpis.reviewAmount, 2)}
            subtitle={`${kpis.failedCount.toLocaleString('en-IN')} failed · ${kpis.reviewCount.toLocaleString('en-IN')} in review · ${highCount} high`}
            info="Same failed + review buckets as Transactions and Reconciliation."
          />
          <div className="grid gap-3 sm:grid-cols-3">
            <MiniMetricCard
              label="Processed"
              value={formatPaise(kpis.processedAmount, 2)}
              subtitle={`${processedCount.toLocaleString('en-IN')} processed`}
              info="Status processed · same amount as Reconciliation"
              onClick={() => router.push('/reconciliation')}
            />
            <MiniMetricCard
              label="Needs review"
              value={formatPaise(kpis.reviewAmount, 2)}
              subtitle={`${kpis.reviewCount.toLocaleString('en-IN')} open · awaiting action`}
              info="Processing, pending, scheduled, or queued"
              warn
              hrefLabel="View All"
              onClick={() => setFilter('all')}
            />
            <MiniMetricCard
              label="Failed"
              value={formatPaise(kpis.failedAmount, 2)}
              subtitle={`${kpis.failedCount.toLocaleString('en-IN')} payouts`}
              info="Failed, reversed, rejected, or cancelled"
              warn
              onClick={() => setFilter('high')}
            />
          </div>
        </div>

        <div className="mt-8">
          <UnderlineTabs
            items={FILTERS.map((f) => ({ id: f.id, label: f.label }))}
            active={filter}
            onChange={(id) => setFilter(id as FilterId)}
          />

          <div className="mt-4 flex flex-wrap items-center justify-between gap-3">
            <input
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search exception id, entity, reason…"
              className="h-9 w-full max-w-md rounded-[6px] border border-[#E6E8EB] bg-white px-3 text-[13px] text-[#1A1A1A] outline-none placeholder:text-[#A0A4AB] focus:border-[#528FF0]"
            />
            <button
              type="button"
              onClick={() => void load()}
              className="h-9 rounded-[6px] border border-[#E6E8EB] bg-white px-3 text-[13px] font-medium text-[#1A1A1A] hover:bg-[#FAFBFC]"
            >
              Refresh
            </button>
          </div>

          {error ? (
            <p className="mt-4 rounded-[8px] border border-[#FECACA] bg-[#FEF2F2] px-4 py-3 text-[13px] text-[#B91C1C]">
              {error}
            </p>
          ) : null}

          <div className={`${RZ_CARD} mt-4 overflow-hidden`}>
            {loading ? (
              <p className={`px-6 py-10 text-center ${RZ_MUTED}`}>Loading exceptions…</p>
            ) : visible.length === 0 ? (
              <PaymentsEmptyState
                title="No exceptions in this filter"
                body="Switch tabs or clear search. Open exceptions appear when reconciliation finds unresolved exposure."
                actionLabel="Show all"
                onAction={() => {
                  setFilter('all')
                  setSearch('')
                }}
              />
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full min-w-[960px] text-left text-[13px]">
                  <thead className="border-b border-[#EEF0F3] bg-[#FAFBFC] text-[11px] font-semibold uppercase tracking-[0.06em] text-[#8F8F8F]">
                    <tr>
                      <th className="px-4 py-3 font-semibold">Exception ID</th>
                      <th className="px-4 py-3 font-semibold">Entity</th>
                      <th className="px-4 py-3 font-semibold">Type</th>
                      <th className="px-4 py-3 font-semibold">Reason</th>
                      <th className="px-4 py-3 text-right font-semibold">Exposure</th>
                      <th className="px-4 py-3 font-semibold">Provider</th>
                      <th className="px-4 py-3 font-semibold">Reconciliation</th>
                      <th className="px-4 py-3 font-semibold">Severity</th>
                    </tr>
                  </thead>
                  <tbody>
                    {visible.map((ex) => {
                      const sev = exceptionSeverity(ex)
                      const selected = openId === ex.entity_id
                      return (
                        <tr
                          key={ex.id}
                          onClick={() => openRow(ex)}
                          className={`cursor-pointer border-t border-[#F3F4F6] hover:bg-[#FAFBFC] ${
                            selected ? 'bg-[#FFFBEB]' : ''
                          }`}
                        >
                          <td className="px-4 py-3 font-mono text-[12px] text-[#1A1A1A]">{ex.id}</td>
                          <td className="px-4 py-3 font-mono text-[12px] text-[#334155]">{ex.entity_id}</td>
                          <td className="px-4 py-3 capitalize text-[#6B6B6B]">{ex.entity_type}</td>
                          <td className="max-w-[240px] px-4 py-3 text-[#334155]">{reasonTitle(ex.reason)}</td>
                          <td className="px-4 py-3 text-right font-medium tabular-nums text-[#1A1A1A]">
                            {formatPaise(ex.variance_amount, 2)}
                          </td>
                          <td className="px-4 py-3">
                            <span className="font-mono text-[11px] uppercase text-[#475569]">
                              {ex.provider_status || (ex.entity_type === 'bank' ? 'N/A' : '—')}
                            </span>
                          </td>
                          <td className="px-4 py-3">
                            <span
                              className={`inline-flex h-6 items-center rounded-[4px] px-2 text-[11px] font-semibold ${reconToneClass(ex.reconciliation_result)}`}
                            >
                              {reconLabel(ex.reconciliation_result)}
                            </span>
                          </td>
                          <td className="px-4 py-3">
                            <StatusBadge tone={severityTone(sev)}>{sev.toLowerCase()}</StatusBadge>
                          </td>
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        </div>
      </div>

      {openId ? (
        <>
          <button
            type="button"
            className="fixed inset-0 z-30 bg-black/20 xl:bg-transparent"
            aria-label="Close exception details overlay"
            onClick={closeDrawer}
          />
          <div className="fixed inset-y-0 right-0 z-40 w-full max-w-[480px]">
            <PaymentDrawer
              key={openId}
              entityId={openId}
              exceptionId={openException?.id}
              exception={openException}
              onClose={closeDrawer}
            />
          </div>
        </>
      ) : null}
    </div>
  )
}
