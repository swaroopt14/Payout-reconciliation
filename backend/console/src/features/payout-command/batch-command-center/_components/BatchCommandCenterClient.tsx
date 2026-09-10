'use client'

import Link from 'next/link'
import { usePathname, useRouter, useSearchParams } from 'next/navigation'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useSessionTenant } from '@/services/auth/useSessionTenantId'
import { Glyph, LiveDataHint } from '../../shared'
import type {
  BatchIntakeSnapshot,
  BatchUploadStatus,
  IntentIngestSuccessPayload,
  SettlementIngestSuccessPayload,
} from './BatchIntakePanel'
import { CreatePayoutObligationPanel } from './CreatePayoutObligationPanel'
import { PageExplainerBanner } from '../../demo/PageExplainerBanner'
import { BatchGetStartedCard } from './BatchGetStartedCard'
import { HydrationSafeLocaleTime } from '../../command-center/HydrationSafeLocaleTime'
import {
  derivePaymentProofTimeline,
  paymentProofProgressPct,
  type BatchSummary,
} from '@/services/payout-command/batch-model'
import { useBatchOperationsFeed } from '@/services/payout-command/batch-operations/useBatchOperationsFeed'
import { BATCH_REVIEW_COPY } from '../copy/batchCommandCenterCopy'
import { BatchAdvancedDetails } from './BatchAdvancedDetails'
import { BatchIngestSuccessDialog } from './BatchIngestSuccessDialog'
import { BatchProgressPanel } from './BatchProgressPanel'
import { PaymentStatusBreakdown } from './PaymentStatusBreakdown'
import { ReviewItemsTable } from './ReviewItemsTable'
import type { BatchRow } from '@/services/payout-command/batch-model'
import { mapPaymentStatusBreakdown } from '../mappers/mapBatchReviewKpis'
import { AiRouteRecommendationModal } from '../../finance-ops/AiRouteRecommendationModal'
import {
  buildBulkBatchSummary,
  mockBulkPayoutRows,
  receiverBanksFromIfscs,
  requestedRailFromKinds,
  type RoutingPhase,
} from '../../finance-ops/bulkRouteDemo'

function summaryFromEngineRows(
  intentCount: number,
  successCount: number,
  failureCount: number,
  pendingCount: number,
  processingCount: number,
): BatchSummary {
  const totalRows = intentCount + failureCount
  if (totalRows <= 0) {
    return { totalRows: 0, processed: 0, success: 0, failed: 0, pending: 0 }
  }
  const success = successCount
  const failed = failureCount
  const pending = pendingCount
  const processed = success + failed + pending + processingCount
  return { totalRows, processed, success, failed, pending }
}

type IngestDialogState =
  | { kind: 'intent'; batchId: string; fileName: string }
  | { kind: 'settlement'; batchId: string; fileName: string }
  | null


export default function BatchCommandCenterClient() {
  const pathname = usePathname()
  const router = useRouter()
  const searchParams = useSearchParams()
  const { tenantId, tenantReady } = useSessionTenant()

  const initialBatchFromUrl = searchParams.get('batch_id')?.trim() ?? ''
  const [committedBatchId, setCommittedBatchId] = useState(initialBatchFromUrl)
  const [intakeSnapshot, setIntakeSnapshot] = useState<BatchIntakeSnapshot>({
    intakeStep: 'idle',
    intentFileName: null,
    intentIngestOk: false,
    settlementFileName: null,
    settlementIngestOk: false,
    uploadedFileName: null,
    uploadState: 'idle',
    settlementBatchId: null,
  })
  const [ingestDialog, setIngestDialog] = useState<IngestDialogState>(null)
  const [aiRouteOpen, setAiRouteOpen] = useState(false)
  const [aiRoutePhase, setAiRoutePhase] = useState<RoutingPhase>('idle')
  const [aiRouteFileName, setAiRouteFileName] = useState('bulk_payout_batch.csv')
  const [intentFilePreviewRows, setIntentFilePreviewRows] = useState<BatchRow[]>([])
  const [settlementFilePreviewRows, setSettlementFilePreviewRows] = useState<BatchRow[]>([])
  const [uploadStatus, setUploadStatus] = useState<BatchUploadStatus>({ state: 'idle', message: null })
  const [toolbarNotice, setToolbarNotice] = useState<string | null>(null)
  const [shareBusy, setShareBusy] = useState(false)
  const toolbarNoticeTimerRef = useRef<number | null>(null)

  const activeBatchId = useMemo(() => {
    const fromInput = committedBatchId.trim()
    const fromIntake = intakeSnapshot.settlementBatchId?.trim() ?? ''
    return fromInput || fromIntake
  }, [committedBatchId, intakeSnapshot.settlementBatchId])

  const feed = useBatchOperationsFeed({
    enabled: tenantReady,
    batchId: activeBatchId,
  })

  // Browser back/forward or external ?batch_id= link - not fired while the user is typing.
  useEffect(() => {
    const urlBatch = searchParams.get('batch_id')?.trim() ?? ''
    setCommittedBatchId(urlBatch)
  }, [searchParams])

  const syncBatchIdToUrl = useCallback(
    (id: string) => {
      const trimmed = id.trim()
      const params = new URLSearchParams(searchParams.toString())
      if (trimmed) params.set('batch_id', trimmed)
      else params.delete('batch_id')
      const qs = params.toString()
      router.replace(`${pathname}${qs ? `?${qs}` : ''}`, { scroll: false })
    },
    [pathname, router, searchParams],
  )

  useEffect(() => {
    const urlBatch = searchParams.get('batch_id')?.trim() ?? ''
    const nextBatch = committedBatchId.trim()
    if (urlBatch === nextBatch) return
    syncBatchIdToUrl(committedBatchId)
  }, [committedBatchId, searchParams, syncBatchIdToUrl])

  const handleBatchIdCommit = useCallback((value: string) => {
    setCommittedBatchId(value)
  }, [])

  const showToolbarNotice = useCallback((message: string) => {
    setToolbarNotice(message)
    if (toolbarNoticeTimerRef.current) window.clearTimeout(toolbarNoticeTimerRef.current)
    toolbarNoticeTimerRef.current = window.setTimeout(() => {
      setToolbarNotice(null)
      toolbarNoticeTimerRef.current = null
    }, 4500)
  }, [])

  useEffect(() => {
    return () => {
      if (toolbarNoticeTimerRef.current) window.clearTimeout(toolbarNoticeTimerRef.current)
    }
  }, [])

  const onIntentIngestSuccess = useCallback(
    (payload: IntentIngestSuccessPayload) => {
      const batchId = payload.effectiveBatch ?? payload.batchId
      setIntentFilePreviewRows(payload.parsedRows)
      setIngestDialog(null)
      setAiRouteFileName(payload.fileName || 'bulk_payout_batch.csv')
      setAiRoutePhase('analyzing')
      setAiRouteOpen(true)
      void feed.refreshBatchFeed()
      setToolbarNotice(`Batch ${batchId} uploaded · AI is recommending route`)
    },
    [feed],
  )

  const onSettlementIngestSuccess = useCallback(
    (payload: SettlementIngestSuccessPayload) => {
      setSettlementFilePreviewRows(payload.parsedRows)
      setIngestDialog({ kind: 'settlement', batchId: payload.batchId, fileName: payload.fileName })
      void feed.refreshBatchFeed()
    },
    [feed],
  )

  const scrollToIntakeStep = useCallback((step: 1 | 2) => {
    const el = document.getElementById(step === 1 ? 'batch-intake-step-1' : 'batch-intake-step-2')
    el?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }, [])

  // Top-bar Upload / ?upload=1 lands on the intake section.
  // ?settlement=1 (from Settlement Journal) scrolls to the bank settlement file step.
  useEffect(() => {
    if (searchParams.get('upload') !== '1') return
    const targetId = searchParams.get('settlement') === '1' ? 'batch-intake-step-2' : 'batch-intake-step-1'
    let attempts = 0
    const tryScroll = () => {
      const el = document.getElementById(targetId)
      if (el) {
        el.scrollIntoView({ behavior: 'smooth', block: 'start' })
        return
      }
      attempts += 1
      if (attempts < 12) window.setTimeout(tryScroll, 100)
    }
    const t = window.setTimeout(tryScroll, 80)
    return () => window.clearTimeout(t)
  }, [searchParams])

  const engineSummary = useMemo(() => {
    const success = feed.intentRows.filter((r) => r.status === 'Confirmed').length
    const pending = feed.intentRows.filter((r) => r.status === 'Pending').length
    const processing = feed.intentRows.filter((r) => r.status === 'In Progress').length
    const failed = feed.failureRows.length
    return summaryFromEngineRows(feed.intentRows.length + failed, success, failed, pending, processing)
  }, [feed.intentRows, feed.failureRows])

  const statCardsSummary = feed.intelligenceSummary ?? engineSummary
  const payoutsHref = useMemo(() => '/payouts?demo=sandbox&upload=1', [])
  const failuresTabHref = useMemo(() => '/exceptions?demo=sandbox', [])

  const aiRouteSummary = useMemo(() => {
    const parsed = intentFilePreviewRows
    const mock = mockBulkPayoutRows(Math.max(parsed.length, 8), aiRouteFileName)
    const ifscs = parsed.map((r) => r.ifsc)
    const kinds = parsed.map((r) => r.instrumentKind)
    const maxRowMinor = parsed.length
      ? parsed.reduce((m, row) => Math.max(m, Math.round((Number(row.amount) || 0) * 100)), 0)
      : 0
    const base = buildBulkBatchSummary({
      fileName: aiRouteFileName,
      rows: mock,
      uploadedAt: new Date().toLocaleString('en-IN', {
        day: '2-digit',
        month: 'short',
        hour: '2-digit',
        minute: '2-digit',
      }),
      ifscs,
      kinds,
      routeAmountMinor: maxRowMinor || undefined,
    })
    const amountMinor = parsed.length
      ? parsed.reduce((sum, row) => sum + Math.round((Number(row.amount) || 0) * 100), 0)
      : base.totalAmountMinor
    return {
      ...base,
      batchId: activeBatchId || 'batch-001',
      totalRecords: parsed.length || base.totalRecords,
      totalAmountMinor: amountMinor,
      uniqueBeneficiaries: parsed.length
        ? new Set(parsed.map((row) => row.beneficiary || row.refId)).size
        : base.uniqueBeneficiaries,
      receiverBanks: receiverBanksFromIfscs(ifscs),
      receiverIfscCount: ifscs.filter(Boolean).length,
      requestedRail: requestedRailFromKinds(kinds),
      routeAmountMinor: maxRowMinor || base.routeAmountMinor,
      requestedBy: 'finance.ops@merchant.in',
    }
  }, [aiRouteFileName, activeBatchId, intentFilePreviewRows])

  const onAiAnalyzeComplete = useCallback(() => {
    setAiRoutePhase('ready')
  }, [])

  const onAiApprove = useCallback(() => {
    setAiRoutePhase('approved')
    setToolbarNotice('Route approved · opening Payouts for dispatch')
    window.setTimeout(() => {
      router.push(
        `/payouts?demo=sandbox&upload=1&approved=1&file=${encodeURIComponent(aiRouteFileName)}`,
      )
    }, 600)
  }, [router, aiRouteFileName])

  const onAiCancel = useCallback(() => {
    setAiRouteOpen(false)
    setAiRoutePhase('idle')
  }, [])

  const settlementJournalHref = useMemo(() => {
    if (!activeBatchId) return '/settlements?demo=sandbox'
    return `/settlements?demo=sandbox&settlement_id=${encodeURIComponent(activeBatchId)}`
  }, [activeBatchId])

  const pieSlices = useMemo(() => mapPaymentStatusBreakdown(statCardsSummary), [statCardsSummary])

  const pipelineBusy = useMemo(
    () =>
      intakeSnapshot.intakeStep === 'intent_uploading' ||
      intakeSnapshot.intakeStep === 'settlement_uploading' ||
      uploadStatus.state === 'syncing' ||
      (feed.detailLoading && Boolean(activeBatchId)),
    [intakeSnapshot, uploadStatus.state, feed.detailLoading, activeBatchId],
  )

  const pipelineSteps = useMemo(
    () => derivePaymentProofTimeline(statCardsSummary, intakeSnapshot),
    [statCardsSummary, intakeSnapshot],
  )

  const pipelineProgressPct = useMemo(
    () => paymentProofProgressPct(pipelineSteps),
    [pipelineSteps],
  )

  const shareBatchSummary = useCallback(async () => {
    const url = typeof window !== 'undefined' ? window.location.href : ''
    const batchLabel = activeBatchId || '-'
    const tid = tenantId.trim() || '-'
    const text = [
      'Zord - Payment Batch Review snapshot',
      '',
      `Tenant: ${tid}`,
      `Batch id: ${batchLabel}`,
      `Total rows: ${statCardsSummary.totalRows}`,
      `Confirmed: ${statCardsSummary.success} · Pending: ${statCardsSummary.pending} · Failed: ${statCardsSummary.failed}`,
      '',
      `Open: ${url}`,
    ].join('\n')
    const subject = `Batch status · ${batchLabel}`
    if (typeof navigator !== 'undefined' && typeof navigator.share === 'function') {
      try {
        await navigator.share({ title: subject, text, url })
        showToolbarNotice('Shared via your device.')
        return
      } catch (e) {
        if ((e as { name?: string })?.name === 'AbortError') return
      }
    }
    window.location.href = `mailto:?subject=${encodeURIComponent(subject)}&body=${encodeURIComponent(text)}`
    showToolbarNotice('Opened email draft with batch summary.')
  }, [activeBatchId, showToolbarNotice, statCardsSummary, tenantId])

  return (
    <div
      className="payout-command-console text-[13px] font-normal leading-relaxed text-[#1A1A1A] antialiased"
      data-testid="batch-review-page"
    >
      <div className="mx-auto w-full max-w-[1280px] space-y-5">
        <PageExplainerBanner page="upload" />
        <BatchGetStartedCard />

        <CreatePayoutObligationPanel
          batchId={committedBatchId}
          uploadAnchorId="batch-intake-step-1"
          onDraftIntentsCreated={(payload) => {
            handleBatchIdCommit(payload.batchId)
            setIntentFilePreviewRows(payload.parsedRows)
            setIntakeSnapshot((prev) => ({
              ...prev,
              intakeStep: 'intent_ready',
              intentFileName: payload.fileName,
              intentIngestOk: true,
              uploadedFileName: payload.fileName,
              uploadState: 'ready',
              settlementBatchId: payload.batchId,
            }))
            setUploadStatus({
              state: 'synced',
              message: `Draft intents created · batch ${payload.batchId}`,
            })
            onIntentIngestSuccess({
              batchId: payload.batchId,
              effectiveBatch: payload.batchId,
              parsedRows: payload.parsedRows,
              fileName: payload.fileName,
            })
            void feed.refreshBatchFeed()
          }}
          onSettlementUploaded={(payload) => {
            setSettlementFilePreviewRows(payload.parsedRows)
            setIntakeSnapshot((prev) => ({
              ...prev,
              intakeStep: 'closed',
              settlementFileName: payload.fileName,
              settlementIngestOk: true,
              settlementBatchId: payload.batchId,
            }))
            setUploadStatus({
              state: 'synced',
              message: `Settlement confirmation accepted · ${payload.fileName}`,
            })
            onSettlementIngestSuccess(payload)
            void feed.refreshBatchFeed()
          }}
        />

        <div className="flex flex-wrap items-center justify-between gap-2 border border-[#E2E8F0] bg-white px-4 py-3">
          <div className="flex flex-wrap items-center gap-3">
            <LiveDataHint isLive={Boolean(tenantReady && feed.feedLoaded)} source={BATCH_REVIEW_COPY.toolbar.liveSource} />
            {feed.syncAt ? (
              <span className="text-[12px] text-[#64748b]">
                Synced <HydrationSafeLocaleTime date={feed.syncAt} />
              </span>
            ) : null}
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Link
              href={payoutsHref}
              className="inline-flex h-9 items-center border border-[#CBD5E1] bg-white px-3 text-[13px] font-semibold text-[#0B1324] hover:bg-[#F8FAFC]"
            >
              {BATCH_REVIEW_COPY.toolbar.intentJournal}
            </Link>
            {settlementJournalHref ? (
              <Link
                href={settlementJournalHref}
                className="inline-flex h-9 items-center border border-[#CBD5E1] bg-white px-3 text-[13px] font-semibold text-[#0B1324] hover:bg-[#F8FAFC]"
              >
                {BATCH_REVIEW_COPY.toolbar.settlementJournal}
              </Link>
            ) : null}
            <button
              type="button"
              onClick={() => void feed.refreshBatchFeed()}
              disabled={feed.detailLoading}
              title={BATCH_REVIEW_COPY.toolbar.refresh}
              className="flex h-9 w-9 items-center justify-center border border-[#CBD5E1] bg-white text-[#64748b] hover:bg-[#F8FAFC] disabled:opacity-50"
            >
              <Glyph name="refresh" className={`h-[15px] w-[15px] ${feed.detailLoading ? 'animate-spin' : ''}`} />
            </button>
            <button
              type="button"
              disabled={shareBusy}
              onClick={() => {
                void (async () => {
                  setShareBusy(true)
                  try {
                    await shareBatchSummary()
                  } finally {
                    setShareBusy(false)
                  }
                })()
              }}
              className="flex h-9 items-center gap-2 bg-[#0B1324] px-4 text-[13px] font-semibold text-white hover:bg-[#1E293B] disabled:opacity-70"
            >
              {shareBusy ? 'Opening…' : BATCH_REVIEW_COPY.toolbar.share}
            </button>
          </div>
        </div>

        {toolbarNotice ? (
          <div role="status" className="border border-slate-200 bg-slate-50 px-4 py-2.5 text-[13px] font-medium text-slate-800">
            {toolbarNotice}
          </div>
        ) : null}

        {feed.feedError ? (
          <div role="alert" className="border border-[#0B1324]/20 bg-[#F1F5F9] px-4 py-2.5 text-[13px] text-[#0B1324]">
            {feed.feedError}
          </div>
        ) : null}

        <BatchAdvancedDetails
          batchId={committedBatchId}
          onBatchIdChange={handleBatchIdCommit}
          onAfterFetch={() => void feed.refreshBatchFeed()}
        />

        <BatchProgressPanel
          steps={pipelineSteps}
          progressPct={pipelineProgressPct}
          busy={pipelineBusy}
        />

        {uploadStatus.message ? (
          <div
            role="status"
            className={`flex items-center gap-3 rounded-xl border px-4 py-3 text-[13px] font-medium ${
              uploadStatus.state === 'failed'
                ? 'border-[#0B1324]/20 bg-[#F1F5F9] text-[#0B1324]'
                : uploadStatus.state === 'synced'
                  ? 'border-[#0B1324]/20 bg-[#F1F5F9] text-[#0B1324]'
                  : 'border-slate-200 bg-slate-50 text-slate-800'
            }`}
          >
            {uploadStatus.state === 'synced' && (
              <svg className="h-4 w-4 shrink-0 text-[#0B1324]" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.5} aria-hidden="true">
                <path strokeLinecap="round" strokeLinejoin="round" d="M9 12.75L11.25 15 15 9.75M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
              </svg>
            )}
            {uploadStatus.message}
          </div>
        ) : null}

        <PaymentStatusBreakdown slices={pieSlices} hasBatch={Boolean(activeBatchId) || statCardsSummary.totalRows > 0} />

        <ReviewItemsTable
          failures={feed.failureRows}
          intents={feed.intentRows}
          settlementRows={feed.settlementObservationRows}
          intentFileRows={intentFilePreviewRows}
          settlementFileRows={settlementFilePreviewRows}
          failuresTabHref={failuresTabHref}
          loading={feed.detailLoading && !feed.feedLoaded}
        />
      </div>

      {ingestDialog ? (
        <BatchIngestSuccessDialog
          kind={ingestDialog.kind}
          batchId={ingestDialog.batchId}
          fileName={ingestDialog.fileName}
          primaryHref={ingestDialog.kind === 'intent' ? payoutsHref : settlementJournalHref || '/settlements?demo=sandbox'}
          onClose={() => setIngestDialog(null)}
        />
      ) : null}

      <AiRouteRecommendationModal
        open={aiRouteOpen}
        phase={aiRoutePhase}
        summary={aiRouteSummary}
        onAnalyzeComplete={onAiAnalyzeComplete}
        onApprove={onAiApprove}
        onCancel={onAiCancel}
        onAskZord={() => router.push('/ask?demo=sandbox')}
      />
    </div>
  )
}
