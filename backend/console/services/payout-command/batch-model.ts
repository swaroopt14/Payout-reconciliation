export type BatchStepState = 'done' | 'active' | 'warning' | 'upcoming'
export type BatchRowStatus = 'Success' | 'Failed' | 'Pending' | 'Processing'

export type BatchTimelineStep = {
  label: string
  state: BatchStepState
}

export type BatchRowTimelineStep = {
  label: string
  time: string
  state: 'done' | 'active' | 'pending'
}

export type BatchRow = {
  refId: string
  invoiceNo?: string
  amount: number
  beneficiary: string
  ifsc?: string
  status: BatchRowStatus
  stage: string
  reason: string
  time: string
  actionLabel: string
  provider: 'RazorpayX' | 'Cashfree' | 'PayU' | 'Stripe'
  dispatchId: string
  bankReference: string
  timeline: BatchRowTimelineStep[]
}

export type BatchSummary = {
  totalRows: number
  processed: number
  success: number
  failed: number
  pending: number
}

export const FAILURE_REASON_ORDER = [
  'Insufficient Balance',
  'Invalid Account',
  'Bank Timeout',
  'Duplicate',
  'Unknown',
] as const

const BENEFICIARY_MASKS = [
  'XXXXX1234',
  'XXXXX5678',
  'XXXXX9923',
  'XXXXX7711',
  'XXXXX4408',
  'XXXXX2910',
  'XXXXX8214',
] as const

const STAGES_BY_STATUS: Record<BatchRowStatus, string> = {
  Success: 'Confirmed',
  Failed: 'Sent to payment partner',
  Pending: 'Awaiting bank confirmation',
  Processing: 'Disbursement processing',
}

const PROVIDERS: BatchRow['provider'][] = ['RazorpayX', 'Cashfree', 'PayU', 'Stripe']
const FRIENDLY_REASONS = [...FAILURE_REASON_ORDER]

function seedFor(index: number) {
  return Math.abs(Math.sin(index * 12.9898) * 43758.5453)
}

function seededPick<T>(items: readonly T[], index: number): T {
  return items[Math.floor(seedFor(index) % items.length)]
}

function resolveStatus(index: number): BatchRowStatus {
  const value = seedFor(index) % 100
  if (value < 63) return 'Success'
  if (value < 72) return 'Processing'
  if (value < 86) return 'Pending'
  return 'Failed'
}

function buildTimeline(status: BatchRowStatus, rowTime: string): BatchRowTimelineStep[] {
  const base = [
    { label: 'Loan system', time: '10:01:02', state: 'done' as const },
    { label: 'Standardized', time: '10:01:05', state: 'done' as const },
    { label: 'Validated', time: '10:01:07', state: 'done' as const },
    { label: 'Payment partner', time: '10:01:10', state: 'done' as const },
  ]

  if (status === 'Success') {
    return [...base, { label: 'Bank confirmation', time: '10:02:04', state: 'done' }, { label: 'Reference on record', time: rowTime, state: 'done' }]
  }

  if (status === 'Failed') {
    return [...base, { label: 'Bank confirmation', time: '-', state: 'active' }, { label: 'Reference on record', time: '-', state: 'pending' }]
  }

  if (status === 'Pending') {
    return [...base, { label: 'Bank confirmation', time: '-', state: 'active' }, { label: 'Reference on record', time: '-', state: 'pending' }]
  }

  return [...base, { label: 'Bank confirmation', time: '-', state: 'active' }, { label: 'Reference on record', time: '-', state: 'pending' }]
}

export function formatInr(value: number) {
  return new Intl.NumberFormat('en-IN', {
    style: 'currency',
    currency: 'INR',
    maximumFractionDigits: 0,
  }).format(Math.round(value))
}

/** Table / summary amounts - preserve paise (no integer rounding). */
export function formatInrPrecise(value: number) {
  if (!Number.isFinite(value)) return '-'
  return new Intl.NumberFormat('en-IN', {
    style: 'currency',
    currency: 'INR',
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(value)
}

export function formatPercent(value: number) {
  return `${value.toFixed(1)}%`
}

export function formatClock(offsetSeconds: number) {
  const baseSeconds = 10 * 3600 + 2 * 60 + 30
  const total = baseSeconds + offsetSeconds
  const hh = String(Math.floor(total / 3600) % 24).padStart(2, '0')
  const mm = String(Math.floor((total % 3600) / 60)).padStart(2, '0')
  const ss = String(total % 60).padStart(2, '0')
  return `${hh}:${mm}:${ss}`
}

function actionFor(status: BatchRowStatus) {
  if (status === 'Success') return 'Export confirmation row'
  if (status === 'Failed') return 'Retry row'
  if (status === 'Pending') return 'Inspect queue'
  return 'Track progress'
}

function reasonFor(status: BatchRowStatus, index: number) {
  if (status !== 'Failed') return '-'
  return seededPick(FRIENDLY_REASONS, index)
}

export function createRow(index: number): BatchRow {
  const status = resolveStatus(index)
  const secondsOffset = Math.floor(seedFor(index + 3) % 900)
  const time = status === 'Pending' ? '-' : formatClock(secondsOffset)
  const refId = `P${String(123 + index).padStart(5, '0')}`
  const amount = 500 + Math.floor(seedFor(index + 9) % 80_000)
  const beneficiary = seededPick(BENEFICIARY_MASKS, index)
  const provider = seededPick(PROVIDERS, index)
  const reason = reasonFor(status, index)
  const stage = STAGES_BY_STATUS[status]
  return {
    refId,
    amount,
    beneficiary,
    status,
    stage,
    reason,
    time,
    actionLabel: actionFor(status),
    provider,
    dispatchId: `disp_${Math.floor(seedFor(index + 33) % 99_999)}`,
    bankReference: `UTR${Math.floor(seedFor(index + 41) % 99999999999)
      .toString()
      .padStart(11, '0')}`,
    timeline: buildTimeline(status, time),
  }
}

export function buildDefaultBatchRows(count = 180): BatchRow[] {
  return Array.from({ length: count }, (_, index) => createRow(index))
}

export function buildSeedSummary(): BatchSummary {
  return {
    totalRows: 10_000,
    processed: 7_000,
    success: 6_500,
    failed: 300,
    pending: 200,
  }
}

export type ZordPipelineIntake = {
  intakeStep: 'idle' | 'intent_uploading' | 'intent_ready' | 'settlement_uploading' | 'closed'
  intentFileName: string | null
  intentIngestOk: boolean
  settlementFileName: string | null
  settlementIngestOk: boolean
  uploadedFileName: string | null
  uploadState: 'idle' | 'uploading' | 'ready'
}

const ZORD_PIPELINE_LABELS = [
  'Batch received',
  'File processed',
  'Disbursement processing',
  'Payment partner',
  'Bank confirmation pending',
  'Batch closed',
] as const

/**
 * Batch Command Center pipeline - driven by bulk ingest / settlement intake and grid summary.
 * `active` is used as the loader step; bank backlog uses `warning` on “Bank confirmation pending”.
 */
export function deriveZordPipelineTimeline(
  summary: BatchSummary,
  intake: ZordPipelineIntake,
): BatchTimelineStep[] {
  const processing = Math.max(0, summary.totalRows - summary.processed)
  const hasBatch = summary.totalRows > 0

  const batchReceived =
    Boolean(intake.intentFileName) ||
    Boolean(intake.uploadedFileName) ||
    intake.intentIngestOk ||
    intake.intakeStep !== 'idle' ||
    Boolean(intake.settlementFileName)

  const fileProcessed =
    intake.intentIngestOk ||
    intake.intakeStep === 'intent_ready' ||
    intake.intakeStep === 'settlement_uploading' ||
    intake.intakeStep === 'closed' ||
    (intake.uploadState === 'ready' && Boolean(intake.uploadedFileName))

  const fileProcessingInFlight =
    intake.intakeStep === 'intent_uploading' ||
    (intake.uploadState === 'uploading' && Boolean(intake.uploadedFileName))

  const intakePathReady =
    intake.intentIngestOk ||
    intake.intakeStep === 'intent_ready' ||
    intake.intakeStep === 'settlement_uploading' ||
    intake.intakeStep === 'closed'

  const intentStillUploading = intake.intakeStep === 'intent_uploading'

  const disbursementDone = hasBatch && intakePathReady && !intentStillUploading && processing === 0
  const disbursementActive = hasBatch && intakePathReady && !intentStillUploading && processing > 0

  const batchClosedSim =
    intake.intakeStep === 'closed' ||
    (disbursementDone && summary.pending === 0 && hasBatch && summary.success >= summary.totalRows)

  const steps: BatchTimelineStep[] = ZORD_PIPELINE_LABELS.map((label) => ({ label, state: 'upcoming' as BatchStepState }))
  const set = (i: number, state: BatchStepState) => {
    steps[i].state = state
  }

  set(0, batchReceived ? 'done' : 'upcoming')

  if (fileProcessed) set(1, 'done')
  else if (fileProcessingInFlight) set(1, 'active')
  else set(1, batchReceived ? 'upcoming' : 'upcoming')

  if (!intakePathReady || intentStillUploading) set(2, 'upcoming')
  else if (disbursementActive) set(2, 'active')
  else if (disbursementDone) set(2, 'done')
  else set(2, 'upcoming')

  if (!disbursementDone) set(3, 'upcoming')
  else set(3, 'done')

  if (!disbursementDone) set(4, 'upcoming')
  else if (intake.intakeStep === 'settlement_uploading') set(4, 'active')
  else if (intake.settlementIngestOk || intake.intakeStep === 'closed') set(4, 'done')
  else if (summary.pending > 0) set(4, 'warning')
  else set(4, 'done')

  if (batchClosedSim) set(5, 'done')
  else set(5, 'upcoming')

  return steps
}

export const PAYMENT_PROOF_PIPELINE_STEPS = [
  { label: 'File received', description: 'Zord has received the payment file.' },
  { label: 'File mapped', description: 'Headers and fields are mapped into Zord’s payment structure.' },
  { label: 'Payment intents created', description: 'Each row is converted into a payment intent.' },
  {
    label: 'Confirmation received',
    description: 'Bank/settlement/status file has been uploaded or connected.',
  },
  { label: 'Matching completed', description: 'Zord has linked payment intents with outcome records.' },
  {
    label: 'Ready for proof / review',
    description: 'Batch is ready for evidence export or issue review.',
  },
] as const

/** Payment proof lifecycle for Batch Command Center (no disbursement language). */
export function derivePaymentProofTimeline(
  summary: BatchSummary,
  intake: ZordPipelineIntake,
): BatchTimelineStep[] {
  const fileReceived =
    Boolean(intake.intentFileName) ||
    Boolean(intake.uploadedFileName) ||
    intake.intentIngestOk ||
    intake.intakeStep !== 'idle' ||
    Boolean(intake.settlementFileName)

  const fileMapped =
    intake.intentIngestOk ||
    intake.intakeStep === 'intent_ready' ||
    intake.intakeStep === 'settlement_uploading' ||
    intake.intakeStep === 'closed' ||
    (intake.uploadState === 'ready' && Boolean(intake.uploadedFileName))

  const mappingInFlight =
    intake.intakeStep === 'intent_uploading' ||
    (intake.uploadState === 'uploading' && Boolean(intake.uploadedFileName))

  const intentsCreated = summary.totalRows > 0 && fileMapped
  const intentsCreating = fileMapped && summary.totalRows === 0 && !mappingInFlight

  const confirmationReceived =
    intake.settlementIngestOk || intake.intakeStep === 'closed' || Boolean(intake.settlementFileName)
  const confirmationActive = intake.intakeStep === 'settlement_uploading'

  const matchingDone =
    confirmationReceived &&
    summary.totalRows > 0 &&
    summary.processed >= summary.totalRows &&
    summary.failed === 0
  const matchingActive =
    confirmationReceived && summary.totalRows > 0 && summary.processed < summary.totalRows

  const readyForReview =
    matchingDone ||
    (intentsCreated && summary.failed > 0) ||
    (intake.intakeStep === 'closed' && summary.totalRows > 0)

  const steps: BatchTimelineStep[] = PAYMENT_PROOF_PIPELINE_STEPS.map((s) => ({
    label: s.label,
    state: 'upcoming' as BatchStepState,
  }))
  const set = (i: number, state: BatchStepState) => {
    steps[i].state = state
  }

  set(0, fileReceived ? 'done' : 'upcoming')
  if (fileMapped) set(1, 'done')
  else if (mappingInFlight) set(1, 'active')
  else set(1, fileReceived ? 'upcoming' : 'upcoming')

  if (intentsCreated) set(2, 'done')
  else if (intentsCreating) set(2, 'active')
  else set(2, fileMapped ? 'upcoming' : 'upcoming')

  if (confirmationReceived) set(3, 'done')
  else if (confirmationActive) set(3, 'active')
  else set(3, intentsCreated ? 'warning' : 'upcoming')

  if (matchingDone) set(4, 'done')
  else if (matchingActive) set(4, 'active')
  else if (confirmationReceived && summary.failed > 0) set(4, 'warning')
  else set(4, 'upcoming')

  if (readyForReview) set(5, summary.failed > 0 && !matchingDone ? 'warning' : 'done')
  else if (matchingActive) set(5, 'active')
  else set(5, 'upcoming')

  return steps
}

export function paymentProofProgressPct(steps: BatchTimelineStep[]): number {
  const n = steps.length
  const done = steps.filter((s) => s.state === 'done').length
  const bump = steps.some((s) => s.state === 'active') ? 0.45 : steps.some((s) => s.state === 'warning') ? 0.25 : 0
  return Math.min(100, ((done + bump) / Math.max(n, 1)) * 100)
}

/** @deprecated Use deriveZordPipelineTimeline for intake-aware pipeline. */
export function deriveTimeline(summary: BatchSummary, fileUploaded: boolean): BatchTimelineStep[] {
  return deriveZordPipelineTimeline(summary, {
    intakeStep: fileUploaded ? 'intent_ready' : 'idle',
    intentFileName: fileUploaded ? 'legacy' : null,
    intentIngestOk: fileUploaded,
    settlementFileName: null,
    settlementIngestOk: false,
    uploadedFileName: null,
    uploadState: fileUploaded ? 'ready' : 'idle',
  })
}

export function computeFailureCounts(rows: BatchRow[]) {
  const map = new Map<string, number>()
  for (const key of FAILURE_REASON_ORDER) map.set(key, 0)
  for (const row of rows) {
    if (row.status === 'Failed' && row.reason !== '-') {
      map.set(row.reason, (map.get(row.reason) ?? 0) + 1)
    }
  }
  return [...map.entries()].map(([reason, count]) => ({ reason, count }))
}

export function progressFromSummary(summary: BatchSummary) {
  if (summary.totalRows <= 0) {
    return { successPct: 0, failedPct: 0, pendingPct: 0, processedPct: 0, processingPct: 0 }
  }
  const successPct = (summary.success / summary.totalRows) * 100
  const failedPct = (summary.failed / summary.totalRows) * 100
  const pendingPct = (summary.pending / summary.totalRows) * 100
  const processedPct = (summary.processed / summary.totalRows) * 100
  const processingPct = Math.max(0, 100 - processedPct)
  return {
    successPct,
    failedPct,
    pendingPct,
    processedPct,
    processingPct,
  }
}

/**
 * Maps zord-intelligence batch_contracts counts into the same `BatchSummary` shape
 * so StatCards / pie / percentages match Intelligence when a batch snapshot is linked.
 * Rows still in the internal processing queue are inferred as `total_count - success - failed - pending`.
 */
export function summaryFromIntelligenceBatchRow(batch: {
  total_count: number
  success_count: number
  failed_count: number
  pending_count: number
}): BatchSummary {
  const totalRows = Math.max(0, Math.floor(Number(batch.total_count) || 0))
  if (totalRows <= 0) {
    return { totalRows: 0, processed: 0, success: 0, failed: 0, pending: 0 }
  }
  const success = Math.min(totalRows, Math.max(0, Math.floor(Number(batch.success_count) || 0)))
  const failed = Math.min(totalRows, Math.max(0, Math.floor(Number(batch.failed_count) || 0)))
  const pending = Math.min(totalRows, Math.max(0, Math.floor(Number(batch.pending_count) || 0)))
  const accounted = success + failed + pending
  const clampedAccounted = Math.min(accounted, totalRows)
  const processed = clampedAccounted
  return { totalRows, processed, success, failed, pending }
}

/** Sum per-batch `BatchSummary` values for a tenant-wide status mix (pie / rollups). */
export function aggregateIntelligenceBatches(
  batches: Array<{
    total_count: number
    success_count: number
    failed_count: number
    pending_count: number
  }>,
): BatchSummary {
  const out: BatchSummary = { totalRows: 0, processed: 0, success: 0, failed: 0, pending: 0 }
  for (const b of batches) {
    const s = summaryFromIntelligenceBatchRow(b)
    out.totalRows += s.totalRows
    out.processed += s.processed
    out.success += s.success
    out.failed += s.failed
    out.pending += s.pending
  }
  return out
}

export function sortRowsByLatest(rows: BatchRow[], sortMode: 'Latest' | 'Oldest') {
  const valueFor = (time: string) => {
    if (!time || time === '-') return -1
    const [h, m, s] = time.split(':').map(Number)
    if ([h, m, s].some(Number.isNaN)) return -1
    return h * 3600 + m * 60 + s
  }
  const sorted = [...rows].sort((a, b) => valueFor(b.time) - valueFor(a.time))
  return sortMode === 'Latest' ? sorted : sorted.reverse()
}

/** Neutral row for missing cells - no random “demo” grid. */
function emptyBatchRowSkeleton(index: number): BatchRow {
  return {
    refId: `R${index + 1}`,
    amount: 0,
    beneficiary: '-',
    status: 'Pending',
    stage: STAGES_BY_STATUS.Pending,
    reason: '-',
    time: '-',
    actionLabel: actionFor('Pending'),
    provider: 'RazorpayX',
    dispatchId: '-',
    bankReference: '-',
    timeline: buildTimeline('Pending', '-'),
  }
}

function parseMatrixToBatchRows(matrix: string[][]): BatchRow[] {
  const rows = matrix.map((r) => r.map((c) => String(c ?? '').trim())).filter((r) => r.some(Boolean))
  if (rows.length <= 1) return []

  const [headerRow, ...dataRows] = rows
  const headers = headerRow.map((h) => h.toLowerCase())

  const pickHeader = (...candidates: string[]) => {
    for (const c of candidates) {
      const exact = headers.findIndex((h) => h === c)
      if (exact >= 0) return exact
    }
    for (const c of candidates) {
      const partial = headers.findIndex((h) => h.includes(c))
      if (partial >= 0) return partial
    }
    return -1
  }

  // Prefer payout/obligation refs over batch refs (YC sample has both).
  const refIdx = pickHeader(
    'client_payout_ref',
    'obligation_id',
    'payout_ref',
    'request_id',
    'requestid',
    'ref_id',
    'ref',
  )
  const invoiceIdx = pickHeader('invoice_id', 'invoice', 'inv')
  const amountIdx = pickHeader('amount.value', 'amount', 'amount_value')
  const beneficiaryIdx = pickHeader('beneficiary.name', 'beneficiary', 'payee', 'account_number')
  const ifscIdx = pickHeader(
    'beneficiary.instrument.ifsc',
    'beneficiary.ifsc',
    'ifsc_code',
    'ifsc',
    'bank_ifsc',
    'beneficiary_ifsc',
  )
  const statusIdx = pickHeader('status')
  const reasonIdx = pickHeader('reason', 'error', 'error_detail')

  return dataRows.slice(0, 1200).map((cells, index) => {
    const base = emptyBatchRowSkeleton(index)

    const rawStatus = statusIdx >= 0 ? cells[statusIdx]?.toLowerCase() : ''
    const status: BatchRowStatus =
      rawStatus.includes('success')
        ? 'Success'
        : rawStatus.includes('fail')
          ? 'Failed'
          : rawStatus.includes('pend')
            ? 'Pending'
            : rawStatus.includes('process')
              ? 'Processing'
              : base.status

    const amountValue = amountIdx >= 0 ? Number(cells[amountIdx]?.replace(/[^0-9.-]/g, '')) : Number.NaN

    return {
      ...base,
      refId: refIdx >= 0 && cells[refIdx] ? cells[refIdx] : base.refId,
      invoiceNo:
        invoiceIdx >= 0 && cells[invoiceIdx]?.trim() ? cells[invoiceIdx].trim() : undefined,
      amount: Number.isFinite(amountValue) && amountValue > 0 ? amountValue : base.amount,
      beneficiary: beneficiaryIdx >= 0 && cells[beneficiaryIdx] ? cells[beneficiaryIdx] : base.beneficiary,
      ifsc: ifscIdx >= 0 && cells[ifscIdx] ? cells[ifscIdx].replace(/\s+/g, '').toUpperCase() : undefined,
      status,
      stage: STAGES_BY_STATUS[status],
      reason: status === 'Failed' ? (reasonIdx >= 0 && cells[reasonIdx] ? cells[reasonIdx] : base.reason) : '-',
      actionLabel: actionFor(status),
      timeline: buildTimeline(status, status === 'Pending' ? '-' : formatClock(index % 900)),
    }
  })
}

export async function parseUploadedSheet(file: File): Promise<BatchRow[]> {
  const lower = file.name.toLowerCase()
  const isCsv = lower.endsWith('.csv')
  const isSheet = lower.endsWith('.xlsx') || lower.endsWith('.xls')

  if (isCsv) {
    const text = await file.text()
    const lines = text
      .split(/\r?\n/)
      .map((line) => line.trim())
      .filter(Boolean)
    if (lines.length <= 1) return []
    const matrix = lines.map((line) => line.split(',').map((cell) => cell.trim()))
    return parseMatrixToBatchRows(matrix)
  }

  if (isSheet) {
    const XLSX = await import('xlsx')
    const ab = await file.arrayBuffer()
    const wb = XLSX.read(ab, { type: 'array' })
    const sheetName = wb.SheetNames[0]
    if (!sheetName) return []
    const sheet = wb.Sheets[sheetName]
    const matrixUnknown = XLSX.utils.sheet_to_json(sheet, { header: 1, defval: '', raw: false }) as unknown[]
    const matrix: string[][] = matrixUnknown.map((row) => {
      if (!Array.isArray(row)) return []
      return row.map((cell) => (cell === null || cell === undefined ? '' : String(cell)))
    })
    return parseMatrixToBatchRows(matrix)
  }

  return []
}

