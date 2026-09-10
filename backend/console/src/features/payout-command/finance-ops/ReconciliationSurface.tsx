'use client'

import { useCallback, useEffect, useMemo, useState } from 'react'
import { useRouter, useSearchParams } from 'next/navigation'
import {
  createFinanceInvestigation,
  getFinanceResults,
  runFinanceReconciliation,
} from '@/services/payout-command/prod-api/financeApi'
import type { FinanceReconRow } from '@/services/payout-command/prod-api/financeTypes'
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
  isOpenReconResult,
  isTerminalFailedStatus,
  mapFinanceRowToPayoutRecon,
  matchesStatusTab,
  reasonsForStatusTab,
  RECON_STATUS_TABS,
  sumPayoutKpis,
  type PayoutReconDisplayRow,
  type ReconStatusTab,
} from './payoutReconCopy'
import { formatPaise } from './reasonCopy'
import {
  payoutStatusTone,
  type RazorpayPayoutStatus,
  type StatusBadgeTone,
} from './razorpayPayoutStatus'
import { PaymentProviderBadge } from './PaymentProviderBadge'
import { PayoutLifecycleDrawer } from './PayoutLifecycleDrawer'
import { reconToneClass } from './payoutLifecycleModel'

const RANGE_OPTIONS = [
  { value: 'today', label: 'Today' },
  { value: '7d', label: 'Last 7 days' },
  { value: '30d', label: 'Last 30 days' },
  { value: 'all', label: 'All time' },
]

function asPayoutTone(status: string): StatusBadgeTone {
  const s = status.toLowerCase() as RazorpayPayoutStatus
  return payoutStatusTone(
    (
      [
        'pending',
        'scheduled',
        'queued',
        'processing',
        'processed',
        'reversed',
        'cancelled',
        'rejected',
        'failed',
      ] as RazorpayPayoutStatus[]
    ).includes(s)
      ? s
      : 'processing',
  )
}

export function ReconciliationSurface() {
  const router = useRouter()
  const searchParams = useSearchParams()
  const [range, setRange] = useState('today')
  const [tab, setTab] = useState<ReconStatusTab>('all')
  const [reasonFilter, setReasonFilter] = useState<string>('ALL')
  const [rows, setRows] = useState<FinanceReconRow[]>([])
  const [records, setRecords] = useState(0)
  const [loading, setLoading] = useState(true)
  const [running, setRunning] = useState(false)
  const [reconcilingId, setReconcilingId] = useState<string | null>(null)
  const [flash, setFlash] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [openId, setOpenId] = useState<string>(searchParams.get('payout_id')?.trim() || '')

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    const res = await getFinanceResults('ALL')
    if (!res.ok || !res.data) {
      setError(res.status === 401 ? 'Sign in to load reconciliation.' : 'Could not load reconciliation.')
      setRows([])
      setLoading(false)
      return
    }
    setRecords(res.data.records)
    setRows(res.data.results ?? [])
    setLoading(false)
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  useEffect(() => {
    setReasonFilter('ALL')
  }, [tab])

  useEffect(() => {
    const fromUrl = searchParams.get('payout_id')?.trim() || ''
    if (fromUrl) setOpenId(fromUrl)
  }, [searchParams])

  const mappedRows = useMemo(() => rows.map(mapFinanceRowToPayoutRecon), [rows])

  const reasonChips = useMemo(() => reasonsForStatusTab(tab), [tab])

  const selectedReasonMeta = useMemo(
    () => reasonChips.find((c) => c.reason === reasonFilter) ?? null,
    [reasonChips, reasonFilter],
  )

  const displayRows = useMemo(() => {
    return mappedRows.filter((r) => {
      if (!matchesStatusTab(String(r.status), tab)) return false
      if (reasonFilter !== 'ALL' && r.errorCode !== reasonFilter && r.reason !== reasonFilter) return false
      return true
    })
  }, [mappedRows, tab, reasonFilter])

  const totals = useMemo(() => {
    const kpis = sumPayoutKpis(mappedRows)
    return {
      amount: kpis.totalAmount,
      paymentCount: kpis.scoredCount || records,
      matchedCount: kpis.processedCount,
      openCount: kpis.reviewCount,
      failedCount: kpis.failedCount,
      matchedAmount: kpis.processedAmount,
      openAmount: kpis.reviewAmount,
      failedAmount: kpis.failedAmount,
    }
  }, [mappedRows, records])

  async function runAll() {
    setRunning(true)
    setFlash(null)
    const payoutIds = mappedRows.map((r) => r.payoutId).filter(Boolean)
    const batchId = searchParams.get('batch')?.trim() || ''
    const res = await runFinanceReconciliation(
      payoutIds.length ? { batch_id: batchId || undefined, payout_ids: payoutIds } : undefined,
    )
    setRunning(false)
    setFlash(res.ok ? 'Reconciliation run completed.' : 'Reconciliation run failed.')
    void load()
  }

  async function reconcileRow(row: PayoutReconDisplayRow) {
    setReconcilingId(row.payoutId)
    setFlash(null)
    const run = await runFinanceReconciliation()
    if (run.ok && isOpenReconResult(String(row.result))) {
      await createFinanceInvestigation({ entity_id: row.payoutId, payment_id: row.payoutId })
    }
    setReconcilingId(null)
    setFlash(run.ok ? `Reconcile queued for ${row.payoutId}` : `Could not reconcile ${row.payoutId}`)
    void load()
  }

  function openRow(row: PayoutReconDisplayRow) {
    setOpenId(row.payoutId)
    const params = new URLSearchParams(searchParams.toString())
    params.set('payout_id', row.payoutId)
    router.replace(`/reconciliation?${params.toString()}`, { scroll: false })
  }

  function closeDrawer() {
    setOpenId('')
    const params = new URLSearchParams(searchParams.toString())
    params.delete('payout_id')
    const q = params.toString()
    router.replace(q ? `/reconciliation?${q}` : '/reconciliation', { scroll: false })
  }

  const openRowData = useMemo(
    () => mappedRows.find((r) => r.payoutId === openId) ?? null,
    [mappedRows, openId],
  )

  return (
    <div className={RZ_PAGE}>
      <div className={`${RZ_WRAP} ${openRowData ? 'pr-[min(560px,100%)]' : ''}`}>
        <PageHeader
          title="Reconciliation"
          range={range}
          onRangeChange={setRange}
          rangeOptions={RANGE_OPTIONS}
          docsHref="https://razorpay.com/docs/payments/payouts/"
        />
        <p className={`mt-1 ${RZ_MUTED}`}>
          Provider status is Razorpay truth. Reconciliation is the control outcome. Click a payout for its
          lifecycle, or open the full trace.
        </p>

        <div className="mt-5 space-y-3">
          <HeroAmountCard
            label="Reconciling Amount"
            amount={formatPaise(totals.amount, 2)}
            subtitle={`from ${totals.paymentCount.toLocaleString('en-IN')} payout rows`}
            info="Sum of payout amounts in the current reconciliation result set."
          />
          <div className="grid gap-3 sm:grid-cols-3">
            <MiniMetricCard
              label="Processed"
              value={formatPaise(totals.matchedAmount, 2)}
              subtitle={`${totals.matchedCount.toLocaleString('en-IN')} processed`}
              info="Status processed · money credited"
              onClick={() => setTab('processed')}
            />
            <MiniMetricCard
              label="Needs review"
              value={formatPaise(totals.openAmount, 2)}
              subtitle={`${totals.openCount.toLocaleString('en-IN')} open · awaiting action`}
              info="Processing, pending, scheduled, or queued payouts"
              warn
              hrefLabel="View All"
              onClick={() => setTab('processing')}
            />
            <MiniMetricCard
              label="Failed"
              value={formatPaise(totals.failedAmount, 2)}
              subtitle={`${totals.failedCount.toLocaleString('en-IN')} payouts`}
              info="Failed, reversed, rejected, or cancelled"
              warn
              onClick={() => setTab('failed')}
            />
          </div>
        </div>

        <div className="mt-8">
          <div className="flex flex-wrap items-end justify-between gap-3">
            <UnderlineTabs
              items={RECON_STATUS_TABS}
              active={tab}
              onChange={(id) => setTab(id as ReconStatusTab)}
            />
            <div className="flex items-center gap-2">
              <button
                type="button"
                onClick={() => {
                  if (!openRowData) return
                  router.push(`/reconciliation/${encodeURIComponent(openRowData.payoutId)}`)
                }}
                disabled={!openRowData}
                className="h-9 rounded-[6px] border border-[#E6E8EB] bg-white px-4 text-[13px] font-semibold text-[#1A1A1A] hover:bg-[#FAFBFC]"
              >
                Open trace
              </button>
              <button
                type="button"
                onClick={() => void runAll()}
                disabled={running}
                className="h-9 rounded-[6px] bg-[#0B1324] px-4 text-[13px] font-semibold text-white hover:bg-[#1E293B] disabled:opacity-60"
              >
                {running ? 'Running…' : 'Run reconciliation'}
              </button>
            </div>
          </div>

          <div className="mt-4 flex flex-wrap items-end justify-between gap-3">
            <label className="flex min-w-[280px] flex-1 flex-col gap-1.5">
              <span className={RZ_MUTED}>
                Reason · <span className="font-mono text-[12px]">status_details.reason</span>
              </span>
              <select
                value={reasonFilter}
                onChange={(e) => setReasonFilter(e.target.value)}
                className="h-9 max-w-xl rounded-[6px] border border-[#E6E8EB] bg-white px-3 text-[13px] text-[#1A1A1A] outline-none focus:border-[#528FF0]"
              >
                <option value="ALL">All reasons in this status</option>
                {(['beneficiary_bank', 'business', 'gateway', 'internal'] as const).map((source) => {
                  const opts = reasonChips.filter((c) => c.source === source)
                  if (opts.length === 0) return null
                  return (
                    <optgroup key={source} label={source}>
                      {opts.map((chip) => (
                        <option key={chip.reason} value={chip.reason}>
                          {chip.reason}
                        </option>
                      ))}
                    </optgroup>
                  )
                })}
              </select>
            </label>
            {selectedReasonMeta ? (
              <div className={`${RZ_CARD} max-w-xl flex-1 px-4 py-3`}>
                <p className="text-[12px] font-medium text-[#1A1A1A]">{selectedReasonMeta.description}</p>
                <p className={`mt-1 ${RZ_MUTED}`}>
                  Source: <span className="font-mono">{selectedReasonMeta.source}</span>
                  <span className="mx-1.5 text-[#D0D4DA]">·</span>
                  Next: {selectedReasonMeta.nextSteps === 'NA' ? '—' : selectedReasonMeta.nextSteps}
                </p>
              </div>
            ) : (
              <p className={`max-w-md ${RZ_MUTED}`}>
                Pick a Razorpay reason to filter the table. Reasons are grouped by signal source.
              </p>
            )}
          </div>

          {flash ? (
            <p className="mt-4 rounded-[8px] border border-[#D1E7DD] bg-[#F0FDF4] px-4 py-3 text-[13px] text-[#15803D]">
              {flash}
            </p>
          ) : null}

          {error ? (
            <p className="mt-4 rounded-[8px] border border-[#FECACA] bg-[#FEF2F2] px-4 py-3 text-[13px] text-[#B91C1C]">
              {error}
            </p>
          ) : null}

          <div className={`${RZ_CARD} mt-4 overflow-hidden`}>
            {loading ? (
              <p className={`px-6 py-10 text-center ${RZ_MUTED}`}>Loading reconciliation…</p>
            ) : displayRows.length === 0 ? (
              <PaymentsEmptyState
                title="No payouts in this status / reason"
                body="Switch tabs or reason chips. Rows map Razorpay status + status_details.reason / source / description."
                actionLabel="Show all"
                onAction={() => {
                  setTab('all')
                  setReasonFilter('ALL')
                }}
              />
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full min-w-[1280px] text-left text-[13px]">
                  <thead className="border-b border-[#EEF0F3] bg-[#FAFBFC] text-[11px] font-semibold uppercase tracking-[0.06em] text-[#8F8F8F]">
                    <tr>
                      <th className="px-4 py-3 font-semibold">Payout ID</th>
                      <th className="px-4 py-3 font-semibold">Processor</th>
                      <th className="px-4 py-3 font-semibold">Provider</th>
                      <th className="px-4 py-3 font-semibold">Reconciliation</th>
                      <th className="px-4 py-3 text-right font-semibold">Amount</th>
                      <th className="px-4 py-3 font-semibold">UTR</th>
                      <th className="px-4 py-3 font-semibold">Reason</th>
                      <th className="px-4 py-3 font-semibold">Source</th>
                      <th className="px-4 py-3 font-semibold">Action</th>
                    </tr>
                  </thead>
                  <tbody>
                    {displayRows.map((row) => {
                      const open = isOpenReconResult(String(row.result)) || isTerminalFailedStatus(String(row.status))
                      const busy = reconcilingId === row.payoutId
                      const details = row.statusDetails
                      const selected = openId === row.payoutId
                      return (
                        <tr
                          key={row.payoutId}
                          onClick={() => openRow(row)}
                          className={`cursor-pointer border-t border-[#F3F4F6] hover:bg-[#FAFBFC] ${
                            selected ? 'bg-[#F8FAFC]' : ''
                          }`}
                        >
                          <td className="px-4 py-3 align-top">
                            <p className="font-mono text-[12px] text-[#1A1A1A]">{row.payoutId}</p>
                            <p className={`mt-0.5 ${RZ_MUTED}`}>
                              {row.fundAccountId || row.contact}
                              {row.mode ? ` · ${row.mode}` : ''}
                            </p>
                          </td>
                          <td className="px-4 py-3 align-top">
                            <PaymentProviderBadge provider={row.paymentProvider || 'razorpay'} />
                          </td>
                          <td className="px-4 py-3 align-top">
                            <StatusBadge tone={asPayoutTone(String(row.status))}>{row.status}</StatusBadge>
                          </td>
                          <td className="px-4 py-3 align-top">
                            <span
                              className={`inline-flex h-6 items-center rounded-[4px] px-2 text-[11px] font-semibold ${reconToneClass(String(row.result))}`}
                            >
                              {row.result}
                            </span>
                            {Math.abs(row.varianceMinor) > 0 && String(row.result).toUpperCase() !== 'MATCHED' ? (
                              <p className={`mt-1 tabular-nums ${RZ_MUTED}`}>
                                {formatPaise(Math.abs(row.varianceMinor), 2)}
                              </p>
                            ) : null}
                          </td>
                          <td className="px-4 py-3 align-top text-right font-medium tabular-nums text-[#1A1A1A]">
                            {formatPaise(row.amountMinor, 2)}
                          </td>
                          <td className="px-4 py-3 align-top font-mono text-[12px] text-[#334155]">
                            {row.utr && row.utr !== '—' ? row.utr : 'null'}
                          </td>
                          <td className="px-4 py-3 align-top">
                            <p className="font-mono text-[12px] text-[#1A1A1A]">{details?.reason || row.errorCode}</p>
                            <p className={`mt-0.5 max-w-[220px] ${RZ_MUTED}`}>
                              {details?.description || row.errorDescription}
                            </p>
                          </td>
                          <td className="px-4 py-3 align-top">
                            <span className="rounded-[4px] bg-[#F3F4F6] px-2 py-1 font-mono text-[11px] text-[#475569]">
                              {details?.source || row.signalSource}
                            </span>
                          </td>
                          <td className="px-4 py-3 align-top" onClick={(e) => e.stopPropagation()}>
                            <div className="flex flex-col items-start gap-1.5">
                              <button
                                type="button"
                                onClick={() =>
                                  router.push(`/reconciliation/${encodeURIComponent(row.payoutId)}`)
                                }
                                className="text-[12px] font-medium text-[#528FF0] hover:underline"
                              >
                                Trace →
                              </button>
                              {String(row.status).toLowerCase() === 'processed' && !open ? (
                                <span className="text-[12px] font-medium text-[#15803D]">Reconciled</span>
                              ) : (
                                <button
                                  type="button"
                                  disabled={busy || running}
                                  onClick={() => void reconcileRow(row)}
                                  className="h-8 rounded-[6px] bg-[#528FF0] px-3 text-[12px] font-semibold text-white hover:bg-[#3F7AE0] disabled:opacity-60"
                                >
                                  {busy ? 'Reconciling…' : 'Reconcile'}
                                </button>
                              )}
                            </div>
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

      {openRowData ? (
        <>
          <button
            type="button"
            className="fixed inset-0 z-30 bg-black/20 xl:bg-transparent"
            aria-label="Close payout lifecycle overlay"
            onClick={closeDrawer}
          />
          <PayoutLifecycleDrawer key={openRowData.payoutId} row={openRowData} onClose={closeDrawer} />
        </>
      ) : null}
    </div>
  )
}
