'use client'

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useRouter, useSearchParams } from 'next/navigation'
import { getFinanceResults } from '@/services/payout-command/prod-api/financeApi'
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
import { formatPaise } from './reasonCopy'
import {
  mapFinanceRowToPayoutRecon,
  matchesStatusTab,
  RECON_STATUS_TABS,
  sumPayoutKpis,
  type ReconStatusTab,
} from './payoutReconCopy'
import {
  payoutStatusTone,
  type RazorpayPayoutStatus,
  type StatusBadgeTone,
} from './razorpayPayoutStatus'
import { reconToneClass } from './payoutLifecycleModel'
import { PayoutLifecycleDrawer } from './PayoutLifecycleDrawer'
import { PaymentProviderBadge } from './PaymentProviderBadge'
import { parseUploadedSheet } from '@/services/payout-command/batch-model'
import {
  buildBulkBatchSummary,
  countPayoutRowsFromFile,
  HDFC_NEFT_RECOMMENDATION,
  mockBulkPayoutRows,
  type RoutingPhase,
} from './bulkRouteDemo'
import { AiRouteRecommendationModal } from './AiRouteRecommendationModal'

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

export function PayoutsSurface() {
  const router = useRouter()
  const searchParams = useSearchParams()
  const fileRef = useRef<HTMLInputElement>(null)
  const [range, setRange] = useState('all')
  const [tab, setTab] = useState<ReconStatusTab>('all')
  const [rows, setRows] = useState<FinanceReconRow[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [openId, setOpenId] = useState<string>('')
  const [bulkRows, setBulkRows] = useState<FinanceReconRow[]>([])
  const [batchMeta, setBatchMeta] = useState<{ name: string; uploadedAt: string } | null>(null)
  const [phase, setPhase] = useState<RoutingPhase>('idle')
  const [modalOpen, setModalOpen] = useState(false)
  const [fileIfscs, setFileIfscs] = useState<string[]>([])

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    const res = await getFinanceResults('ALL')
    if (!res.ok || !res.data) {
      setError(res.status === 401 ? 'Sign in to load payouts.' : 'Could not load payouts.')
      setRows([])
      setLoading(false)
      return
    }
    setRows(res.data.results ?? [])
    setLoading(false)
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const uploadFlag = searchParams.get('upload')
  const approvedFlag = searchParams.get('approved')
  const fileParam = searchParams.get('file')

  /** Landing from Upload success (?upload=1) — always restart AI modal. */
  useEffect(() => {
    if (uploadFlag !== '1') return
    const name = fileParam || 'bulk_payout_batch.csv'
    const mock = mockBulkPayoutRows(24, name)
    setBulkRows(mock)
    setFileIfscs([])
    setBatchMeta({
      name,
      uploadedAt: new Date().toLocaleString('en-IN', {
        day: '2-digit',
        month: 'short',
        hour: '2-digit',
        minute: '2-digit',
      }),
    })
    if (approvedFlag === '1') {
      setPhase('approved')
      setModalOpen(true)
    } else {
      setPhase('analyzing')
      setModalOpen(true)
    }
    setTab('all')
  }, [uploadFlag, approvedFlag, fileParam])

  const onPickFile = useCallback(() => fileRef.current?.click(), [])

  const onFile = useCallback(async (file: File | undefined) => {
    if (!file) return
    const parsed = await parseUploadedSheet(file).catch(() => [])
    const text = parsed.length ? '' : await file.text().catch(() => '')
    const count = parsed.length || countPayoutRowsFromFile(text)
    const mock = mockBulkPayoutRows(count, file.name)
    setBulkRows(mock)
    setFileIfscs(parsed.map((row) => row.ifsc).filter((value): value is string => Boolean(value)))
    setBatchMeta({
      name: file.name,
      uploadedAt: new Date().toLocaleString('en-IN', {
        day: '2-digit',
        month: 'short',
        hour: '2-digit',
        minute: '2-digit',
      }),
    })
    setPhase('analyzing')
    setModalOpen(true)
    setTab('all')
  }, [])

  const onAnalyzeComplete = useCallback(() => {
    setPhase('ready')
  }, [])

  const onApproveRoute = useCallback(() => {
    setBulkRows((prev) =>
      prev.map((r) => ({
        ...r,
        status: 'processing',
        status_details: {
          description: 'Dispatch accepted on the router-selected PSP and rail.',
          source: 'zord-router',
          reason: 'route_approved',
        },
      })),
    )
    setPhase('approved')
  }, [])

  const onCancelRoute = useCallback(() => {
    if (phase === 'approved') {
      setModalOpen(false)
      return
    }
    setModalOpen(false)
    setBulkRows([])
    setBatchMeta(null)
    setFileIfscs([])
    setPhase('idle')
  }, [phase])

  const routeSummary = useMemo(() => {
    const base = buildBulkBatchSummary({
      fileName: batchMeta?.name || 'bulk_payout_batch.csv',
      rows: bulkRows,
      uploadedAt: batchMeta?.uploadedAt || '—',
      ifscs: fileIfscs,
    })
    return {
      ...base,
      batchId: 'batch-001',
      requestedBy: 'finance.ops@merchant.in',
    }
  }, [batchMeta, bulkRows, fileIfscs])

  const mapped = useMemo(() => rows.map(mapFinanceRowToPayoutRecon), [rows])
  const kpis = useMemo(() => sumPayoutKpis(mapped), [mapped])
  const displayRows = useMemo(
    () => mapped.filter((r) => matchesStatusTab(String(r.status), tab)),
    [mapped, tab],
  )
  const openRow = mapped.find((r) => r.payoutId === openId) ?? null

  return (
    <div className={RZ_PAGE}>
      <div className={`${RZ_WRAP} ${openRow ? 'pr-[min(560px,100%)]' : ''}`}>
        <PageHeader
          title="Payouts"
          range={range}
          onRangeChange={setRange}
          rangeOptions={RANGE_OPTIONS}
          docsHref="https://razorpay.com/docs/x/payouts/"
        />
        <div className="mt-1 flex flex-wrap items-start justify-between gap-3">
          <p className={RZ_MUTED}>
            Bulk payout control. Upload lands in this table immediately. AI recommends rail — Razorpay status stays
            pending until you approve dispatch.
          </p>
          <div className="flex gap-2">
            <input
              ref={fileRef}
              type="file"
              accept=".csv,.json,text/csv,application/json"
              className="hidden"
              onChange={(e) => {
                const f = e.target.files?.[0]
                e.target.value = ''
                void onFile(f)
              }}
            />
            <button
              type="button"
              onClick={onPickFile}
              className="rounded-[6px] bg-[#1A1A1A] px-3 py-2 text-[13px] font-medium text-white"
            >
              Upload CSV
            </button>
            <button
              type="button"
              onClick={onPickFile}
              className="rounded-[6px] border border-[#E6E8EB] bg-white px-3 py-2 text-[13px] font-medium text-[#1A1A1A]"
            >
              Upload JSON
            </button>
            <button
              type="button"
              onClick={() => {
                const name = 'rzp-test-key.csv'
                const mock = mockBulkPayoutRows(24, name)
                setBulkRows(mock)
                setBatchMeta({
                  name,
                  uploadedAt: new Date().toLocaleString('en-IN', {
                    day: '2-digit',
                    month: 'short',
                    hour: '2-digit',
                    minute: '2-digit',
                  }),
                })
                setPhase('analyzing')
                setModalOpen(true)
              }}
              className="rounded-[6px] border border-[#BFDBFE] bg-[#EEF4FF] px-3 py-2 text-[13px] font-semibold text-[#1D4ED8]"
            >
              Demo AI route
            </button>
          </div>
        </div>

        <div className="mt-5 space-y-3">
          <HeroAmountCard
            label="Payout book"
            amount={formatPaise(kpis.totalAmount, 2)}
            subtitle={`from ${kpis.scoredCount.toLocaleString('en-IN')} payouts`}
            info="Sum of payout.amount (paise) in the current book."
          />
          <div className="grid gap-3 sm:grid-cols-3">
            <MiniMetricCard
              label="Processed"
              value={formatPaise(kpis.processedAmount, 2)}
              subtitle={`${kpis.processedCount.toLocaleString('en-IN')} processed`}
              info="status = processed"
              onClick={() => setTab('processed')}
            />
            <MiniMetricCard
              label="Needs review"
              value={formatPaise(kpis.reviewAmount, 2)}
              subtitle={`${kpis.reviewCount.toLocaleString('en-IN')} open`}
              info="queued, pending, processing"
              warn
              hrefLabel="View All"
              onClick={() => setTab('processing')}
            />
            <MiniMetricCard
              label="Failed"
              value={formatPaise(kpis.failedAmount, 2)}
              subtitle={`${kpis.failedCount.toLocaleString('en-IN')} payouts`}
              info="failed, reversed, cancelled, rejected"
              warn
              onClick={() => setTab('failed')}
            />
          </div>
        </div>

        {batchMeta && phase !== 'idle' && phase !== 'cancelled' ? (
          <section className={`${RZ_CARD} mt-5 px-5 py-4`}>
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div>
                <p className="text-[11px] font-semibold uppercase tracking-[0.08em] text-[#8F8F8F]">Batch</p>
                <h2 className="mt-1 text-[16px] font-semibold text-[#1A1A1A]">{batchMeta.name}</h2>
                <p className={`mt-1 ${RZ_MUTED}`}>
                  {bulkRows.length} payouts in working set · Uploaded {batchMeta.uploadedAt}
                </p>
              </div>
              <div className="flex flex-wrap items-center gap-2">
                <span className="rounded-full bg-[#EEF4FF] px-2.5 py-1 text-[12px] font-semibold text-[#2B6CB0]">
                  {phase === 'analyzing'
                    ? 'AI analyzing route…'
                    : `Recommendation: ${HDFC_NEFT_RECOMMENDATION.bank} · ${HDFC_NEFT_RECOMMENDATION.rail}`}
                </span>
                <span className="rounded-full bg-[#FFF6E5] px-2.5 py-1 text-[12px] font-semibold text-[#B36B00]">
                  Provider: {phase === 'approved' ? 'processing' : 'pending'}
                </span>
                {(phase === 'ready' || phase === 'analyzing' || phase === 'approved') && !modalOpen ? (
                  <button
                    type="button"
                    onClick={() => setModalOpen(true)}
                    className="rounded-[6px] bg-[#2F6FED] px-3 py-1.5 text-[12px] font-semibold text-white"
                  >
                    {phase === 'analyzing' ? 'Open AI progress' : 'Open AI recommendation'}
                  </button>
                ) : null}
              </div>
            </div>
            {phase === 'approved' ? (
              <p className="mt-3 text-[13px] text-[#147A3F]">
                Dispatch started on HDFC · NEFT. Provider status is now processing — not a reconciliation result.
              </p>
            ) : null}
          </section>
        ) : null}

        <div className="mt-8">
          <UnderlineTabs
            items={RECON_STATUS_TABS}
            active={tab}
            onChange={(id) => setTab(id as ReconStatusTab)}
          />

          <div className={`${RZ_CARD} mt-4 overflow-hidden`}>
            {loading ? (
              <p className={`px-6 py-10 text-center ${RZ_MUTED}`}>Loading payouts…</p>
            ) : error ? (
              <p className="px-6 py-10 text-center text-[13px] text-[#B91C1C]">{error}</p>
            ) : displayRows.length === 0 ? (
              <PaymentsEmptyState
                title="No payouts in this status"
                body="Switch tabs. Rows are Razorpay payout objects with your reconciliation_status overlay."
                actionLabel="Show all"
                onAction={() => setTab('all')}
              />
            ) : (
              <div className="overflow-x-auto">
                <table className="w-full min-w-[1280px] text-left text-[13px]">
                  <thead className="border-b border-[#EEF0F3] bg-[#FAFBFC] text-[11px] font-semibold uppercase tracking-[0.06em] text-[#8F8F8F]">
                    <tr>
                      <th className="px-4 py-3 font-semibold">Payout ID</th>
                      <th className="px-4 py-3 text-right font-semibold">Amount</th>
                      <th className="px-4 py-3 font-semibold">Provider</th>
                      <th className="px-4 py-3 font-semibold">Rail</th>
                      <th className="px-4 py-3 font-semibold">Status</th>
                      <th className="px-4 py-3 font-semibold">Bank</th>
                      <th className="px-4 py-3 font-semibold">UTR</th>
                      <th className="px-4 py-3 font-semibold">Reconciliation</th>
                    </tr>
                  </thead>
                  <tbody>
                    {displayRows.map((row) => {
                      const isBulk = row.payoutId.startsWith('pout_bulk_')
                      const bankLabel =
                        row.bank === true ? 'Credited' : row.bank === false ? 'No movement' : 'Pending'
                      const rail = isBulk
                        ? HDFC_NEFT_RECOMMENDATION.rail
                        : row.mode || 'NEFT'
                      const providerBank = isBulk ? HDFC_NEFT_RECOMMENDATION.bank : row.paymentProvider || 'razorpay'
                      return (
                        <tr
                          key={row.payoutId}
                          onClick={() => setOpenId(row.payoutId)}
                          className={`cursor-pointer border-t border-[#F3F4F6] hover:bg-[#FAFBFC] ${
                            openId === row.payoutId ? 'bg-[#F8FAFC]' : ''
                          }`}
                        >
                          <td className="px-4 py-3 align-top">
                            <p className="font-mono text-[12px] text-[#1A1A1A]">{row.payoutId}</p>
                            <p className={`mt-0.5 ${RZ_MUTED}`}>
                              {row.purpose || 'payout'}
                              {isBulk ? ' · bulk upload' : ''}
                            </p>
                          </td>
                          <td className="px-4 py-3 align-top text-right font-medium tabular-nums text-[#1A1A1A]">
                            {formatPaise(row.amountMinor, 2)}
                          </td>
                          <td className="px-4 py-3 align-top text-[#334155]">{providerBank}</td>
                          <td className="px-4 py-3 align-top text-[#334155]">{rail}</td>
                          <td className="px-4 py-3 align-top">
                            <StatusBadge tone={asPayoutTone(String(row.status))}>{row.status}</StatusBadge>
                            {!isBulk ? (
                              <div className="mt-1">
                                <PaymentProviderBadge provider={row.paymentProvider} />
                              </div>
                            ) : null}
                          </td>
                          <td className="px-4 py-3 align-top text-[#334155]">{bankLabel}</td>
                          <td className="px-4 py-3 align-top font-mono text-[12px] text-[#334155]">
                            {row.utr && row.utr !== '—' ? row.utr : 'null'}
                          </td>
                          <td className="px-4 py-3 align-top">
                            <span
                              className={`inline-flex h-6 items-center rounded-[4px] px-2 text-[11px] font-semibold ${reconToneClass(
                                isBulk && phase !== 'approved' ? 'UNRESOLVED' : String(row.result),
                              )}`}
                            >
                              {isBulk && phase !== 'approved'
                                ? 'Pending'
                                : row.result === 'UNRESOLVED' && isBulk
                                  ? 'Pending'
                                  : row.result}
                            </span>
                          </td>
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </div>
          <p className={`mt-3 ${RZ_MUTED}`}>
            Failure risk is not shown yet — that lands with the predictive phase.
            <button
              type="button"
              className="ml-3 text-[13px] font-medium text-[#528FF0] hover:underline"
              onClick={() => router.push('/reconciliation')}
            >
              Open reconciliation →
            </button>
          </p>
        </div>
      </div>

      {openRow ? (
        <>
          <button
            type="button"
            className="fixed inset-0 z-30 bg-black/20 xl:bg-transparent"
            aria-label="Close payout overlay"
            onClick={() => setOpenId('')}
          />
          <PayoutLifecycleDrawer key={openRow.payoutId} row={openRow} onClose={() => setOpenId('')} />
        </>
      ) : null}

      <AiRouteRecommendationModal
        open={modalOpen && (phase === 'analyzing' || phase === 'ready' || phase === 'approved')}
        phase={phase}
        summary={routeSummary}
        onAnalyzeComplete={onAnalyzeComplete}
        onApprove={onApproveRoute}
        onCancel={onCancelRoute}
        onAskZord={() => router.push('/ask?demo=sandbox')}
      />
    </div>
  )
}
