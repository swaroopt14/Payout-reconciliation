export type FinanceReconResult =
  | 'MATCHED'
  | 'AMBIGUOUS'
  | 'UNRESOLVED'
  | 'CONFLICTED'
  | 'VARIANCE'
  | 'ORPHAN'
  | string

export type FinanceException = {
  id: string
  run_id?: string
  entity_type: string
  entity_id: string
  status?: string
  /** Razorpay / provider truth. Never set this to UNRESOLVED. */
  provider_status?: string
  reconciliation_result: FinanceReconResult
  reason: string
  expected_amount: number
  observed_amount: number
  variance_amount: number
  confidence: number
  evidence_ids?: string[]
  created_at?: string
}

export type FinancePayoutKpis = {
  scored_count: number
  processed_count: number
  processed_amount_minor: number
  review_count: number
  review_amount_minor: number
  failed_count: number
  failed_amount_minor: number
  total_amount_minor: number
}

export type FinanceSummary = {
  entity_counts?: Record<string, number>
  result_counts?: Record<string, number>
  exposure_minor: number
  exposure_by_reason?: Array<{ reason: string; count: number; exposure_minor: number }>
  currency?: string
  scored_count: number
  matched_count: number
  payout_kpis?: FinancePayoutKpis
}

export type FinanceCashPosition = {
  gross_captured_minor: number
  settlement_expected_net_minor: number
  bank_credited_proven_minor: number
  in_flight_minor: number
  unresolved_exposure_minor: number
  currency?: string
  as_of?: string
}

export type FinanceCashScheduleDay = {
  date: string
  expected_credit_minor: number
  expected_debit_minor: number
  count: number
}

export type FinanceCashSchedule = {
  as_of?: string
  horizon_days: number
  kind?: string
  days: FinanceCashScheduleDay[]
  unknown_timing_minor: number
  already_received_minor: number
  limitations?: string[]
}

export type FinanceObservation = {
  source: string
  provider_status: string
  canonical_status: string
  source_event_id?: string
  source_hash?: string
  observed_at?: string
}

export type FinancePayment = {
  status: string
  provider_status: string
  payment_id: string
  amount_minor: number
  currency: string
  captured?: boolean
  method?: string
  order_id?: string
  provider?: string
  provider_created_at?: string
  notes?: Record<string, string>
  sources?: string[]
  observations?: FinanceObservation[]
  reconciliation?: {
    result: FinanceReconResult
    reason: string
    expected_amount: number
    observed_amount: number
    variance_amount: number
    confidence: number
    bank_credit_proven?: boolean
  } | null
  evidence_refs?: Record<string, unknown>
  financial_movement?: {
    payment: number | null
    settlement: number | null
    bank: number | null
    refund: number | null
  }
}

export type FinancePayout = {
  status: string
  provider_status: string
  payout_id: string
  amount_minor: number
  currency: string
  utr?: string | null
  mode?: string
  purpose?: string
  status_reason?: string
  provider_created_at?: string
  observations?: FinanceObservation[]
  reconciliation?: {
    result: FinanceReconResult
    reason: string
    expected_amount: number
    observed_amount: number
    variance_amount: number
    confidence: number
    bank_credit_proven?: boolean
  } | null
  evidence_refs?: Record<string, unknown>
}

export type FinanceRefund = {
  refund_id: string
  payment_id: string
  amount_minor: number
  currency?: string
  provider_status?: string
}

export type FinanceSettlementLine = {
  settlement_id?: string
  payment_id?: string
  entity_id?: string
  line_type?: string
  amount_minor?: number
  fee_minor?: number
  tax_minor?: number
  on_hold?: boolean
  utr?: string
}

/** Razorpay settlement header (list view). Amounts in paise. */
export type RazorpaySettlement = {
  id: string
  entity?: string
  amount: number
  amount_gross?: number
  fees?: number
  tax?: number
  status: 'created' | 'processed' | 'failed' | 'initiated' | string
  utr?: string | null
  created_at: number
  currency?: string
  settlement_schedule?: string
  items_count?: number
  batch_label?: string
  matched_count?: number
  unresolved_count?: number
  failed_count?: number
}

export type RazorpaySettlementOverview = {
  previous_settlement?: Partial<RazorpaySettlement> | null
  today_settlement?: Partial<RazorpaySettlement> | null
  next_settlement?: Partial<RazorpaySettlement> | null
  available_balance?: number
  schedule?: string
  schedule_active?: boolean
  payout_kpis?: {
    processed_count: number
    processed_amount_minor: number
    review_count: number
    review_amount_minor: number
    failed_count: number
    failed_amount_minor: number
    total_amount_minor: number
  }
}

export type RazorpaySettlementListResponse = {
  entity?: string
  count: number
  items: RazorpaySettlement[]
  overview?: RazorpaySettlementOverview
}

/** Line from GET /v1/settlements/recon/combined */
export type RazorpaySettlementReconLine = {
  entity_id: string
  type: 'payment' | 'refund' | 'transfer' | 'adjustment' | string
  debit?: number
  credit?: number
  amount: number
  fee?: number
  tax?: number
  on_hold?: boolean
  settled?: boolean
  created_at?: number
  settled_at?: number | null
  settlement_id?: string
  settlement_utr?: string | null
  payment_id?: string
  order_id?: string
  method?: string
  card_network?: string | null
  card_issuer?: string | null
  card_type?: string | null
  dispute_id?: string | null
  description?: string
  notes?: Record<string, string>
  currency?: string
  /** Smoke / finance overlay — not Razorpay settlement status. */
  provider_status?: string
  reconciliation_result?: string
  reason?: string | null
  variance_amount?: number
  utr?: string | null
  finance_bucket?: 'matched' | 'unresolved' | 'failed' | string
}

export type RazorpaySettlementReconResponse = {
  entity?: string
  count: number
  settlement_id?: string
  items: RazorpaySettlementReconLine[]
  error?: string
}

export type FinanceInvestigation = {
  id: string
  exception_id?: string
  entity_type?: string
  entity_id: string
  status: string
  root_cause: string
  recommendation?: string
  confidence: number
  financial_impact: number
  evidence_ids?: string[]
  hypotheses?: Array<{ claim: string; verdict: string }>
  issue?: string
}

export type FinanceReconRow = {
  payment_id: string
  /** Prefer when entity is a RazorpayX payout (`pout_...`). Falls back to payment_id in UI. */
  payout_id?: string
  settlement: boolean | null
  bank: boolean | null
  result: FinanceReconResult
  variance_amount: number
  reason?: string
  /** Razorpay payout lifecycle status when known. */
  status?: string
  utr?: string | null
  error_code?: string
  error_description?: string
  signal_source?: string
  evidence?: string
  next_steps?: string
  contact?: string
  amount_minor?: number
  /** Razorpay payout API fields. */
  entity?: string
  fund_account_id?: string
  currency?: string
  fees?: number
  tax?: number
  mode?: string
  purpose?: string
  reference_id?: string
  narration?: string
  batch_id?: string | null
  created_at?: number
  notes?: Record<string, string>
  /** Razorpay payout status_details object. */
  status_details?: {
    description: string
    source: string
    reason: string
  }
  /** Payment processor / provider (razorpay, paytm, …). */
  payment_provider?: string
  /** Finance-control exception class. Not a Razorpay payout status. */
  exception_type?: string | null
}

export type FinanceEvaluation = {
  dataset_records: number
  reconciliation_rate: number
  exception_detection_rate: number
  exception_resolution_rate: number
  false_resolution_rate: number
  financial_accuracy: number
  evidence_grounding: number
}

export type FinanceTimelineCapture = {
  captured_at: string
  source: string
  source_event_id?: string
  source_hash?: string
  provider_status?: string
  utr?: string
}

export type FinanceTimelineStep = {
  seq: number
  kind: string
  label: string
  captured: boolean
  captured_at: string | null
  source?: string
  source_event_id?: string
  source_hash?: string
  provider_status?: string
  provider_at?: string | null
  detail?: Record<string, unknown>
  events?: FinanceTimelineCapture[]
  note?: string
}

export type FinanceEntityTimeline = {
  entity_type: 'payout' | 'payment' | string
  entity_id: string
  provider_status: string
  recon_run: boolean
  reconciliation: {
    result: FinanceReconResult
    reason: string
    expected_amount: number
    observed_amount: number
    variance_amount: number
    confidence: number
    bank_credit_proven?: boolean
  } | null
  steps: FinanceTimelineStep[]
}
