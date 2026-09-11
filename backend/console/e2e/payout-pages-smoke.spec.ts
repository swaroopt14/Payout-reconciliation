import { test, expect, type BrowserContext, type Page } from '@playwright/test'

const BASE_URL = process.env.PLAYWRIGHT_BASE_URL || 'http://localhost:3000'
const SESSION_TENANT = 'e2e-session-tenant-111'
const BATCH_ID = 'e2e-batch-222'

const EVIDENCE_BATCH = 'e2e-evidence-batch'
const INTENT_A = 'e2e-intent-aaa'
const INTENT_B = 'e2e-intent-bbb'
const PACK_BATCH = 'pack-batch-001'
const PACK_INTENT_A = 'pack-intent-a'
const PACK_INTENT_B = 'pack-intent-b'

/** India finance console pages (legacy `/payout-command-view/today?dock=` redirects here). */
const CONSOLE_PAGES: { path: string; title: string }[] = [
  { path: '/transactions?demo=sandbox', title: 'Transactions' },
  { path: '/exceptions?demo=sandbox', title: 'Exceptions' },
  { path: '/proof?demo=sandbox', title: 'Evidence' },
  { path: '/ask?demo=sandbox', title: 'Ask Zord' },
]

const STANDALONE_ROUTES = [
  '/payout-command-view/batch-command-center',
  '/payout-command-view/connector-intelligence',
  `/payout-command-view/evidence-pack/${PACK_BATCH}?tab=graph&batch_id=${encodeURIComponent(EVIDENCE_BATCH)}`,
  '/payout-command-view/settings/account',
  '/payout-command-view/settings/api-keys',
]

type ProdCapture = { pathname: string; searchParams: URLSearchParams }

function captureProdGet(url: string): ProdCapture | null {
  try {
    const u = new URL(url)
    if (!u.pathname.startsWith('/api/prod/')) return null
    return { pathname: u.pathname, searchParams: u.searchParams }
  } catch {
    return null
  }
}

async function installPayoutSessionCookies(context: BrowserContext) {
  const parsed = new URL(BASE_URL)
  const port = parsed.port ? `:${parsed.port}` : ''
  const origins = new Set<string>([
    `${parsed.protocol}//${parsed.hostname}${port}`,
    `${parsed.protocol}//localhost${port}`,
    `${parsed.protocol}//127.0.0.1${port}`,
  ])
  const cookies = [...origins].flatMap((url) => ([
    { name: 'zord_access_token', value: 'e2e-playwright-access', url },
    { name: 'zord_refresh_token', value: 'e2e-playwright-refresh', url },
    { name: 'zord_role', value: 'CUSTOMER_USER', url },
    { name: 'zord_session_present', value: '1', url },
  ]))
  await context.addCookies(cookies)
}

function packSummary(
  packId: string,
  opts: {
    intentId?: string
    batchId?: string
    mode: string
    ref?: string
    leafCount?: number
    requiredLeafCount?: number
  },
) {
  const hasSettlement = (opts.leafCount ?? 4) > 0
  const hasAttachment = opts.intentId === INTENT_A ? false : true
  return {
    evidence_pack_id: packId,
    tenant_id: SESSION_TENANT,
    intent_id: opts.intentId,
    batch_id: opts.batchId,
    client_reference: opts.ref ?? packId,
    client_payout_ref: opts.ref ?? packId,
    mode: opts.mode,
    pack_status: 'READY',
    merkle_root: 'a'.repeat(64),
    ruleset_version: '1',
    created_at: '2026-05-01T12:00:00Z',
    proof_status: 'CERTIFIED',
    proof_score: 100,
    leaf_count: opts.leafCount ?? 4,
    required_leaf_count: opts.requiredLeafCount,
    artifact_count: opts.leafCount ?? 4,
    pack_completeness_score: 1,
    settlement_leaf_present_flag: hasSettlement,
    attachment_decision_leaf_present_flag: hasAttachment,
    governance_decision: 'Pass',
    settlement_record_received: '2026-05-01T12:00:02Z',
    canonical_settlement_created: '2026-05-01T12:00:03Z',
    bank_reference: opts.intentId === INTENT_A ? 'UTR-CONFLICT-A' : 'UTR-OK-B',
    attachment_decision: 'MATCH_EXACT',
    match_confidence: 0.9675,
    value_date_check: true,
    amount_match: true,
    verification_status: false,
    proof_components: {
      payment_instruction_available: true,
      settlement_record_available: true,
      match_decision_available: hasAttachment,
      governance_decision_available: true,
      replay_check_passed: true,
    },
  }
}

function packFull(packId: string, intentId: string, mode: string) {
  const hasAttachment = intentId === INTENT_A ? false : true
  return {
    evidence_pack_id: packId,
    tenant_id: SESSION_TENANT,
    intent_id: intentId,
    contract_id: 'contract-smoke',
    mode,
    pack_status: 'READY',
    items: [
      { type: 'CANONICAL_INTENT_HASH', ref: intentId, schema_version: 'v1', hash: 'h1', leaf_hash: 'lh1' },
      { type: 'RAW_SETTLEMENT_LINE', ref: `line-${intentId}`, schema_version: 'v1', hash: 'h2', leaf_hash: 'lh2' },
      { type: 'CANONICAL_SETTLEMENT_OBSERVATION', ref: `set-${intentId}`, schema_version: 'v1', hash: 'h3', leaf_hash: 'lh3' },
      { type: 'ATTACHMENT_DECISION', ref: `att-${intentId}`, schema_version: 'v1', hash: 'h4', leaf_hash: 'lh4' },
      { type: 'VARIANCE_DECISION', ref: `var-${intentId}`, schema_version: 'v1', hash: 'h5', leaf_hash: 'lh5' },
      { type: 'ENVELOPE_HASH', ref: `env-${intentId}`, schema_version: 'v1', hash: 'h6', leaf_hash: 'lh6' },
      { type: 'GOVERNANCE_DECISION_AT_CANONICAL', ref: intentId, schema_version: 'v1', hash: 'h7', leaf_hash: 'lh7' },
      { type: 'RAW_SETTLEMENT_FILE', ref: `raw-${intentId}`, schema_version: 'v1', hash: 'h8', leaf_hash: 'lh8' },
      { type: 'FINAL_EVIDENCE_VIEW', ref: packId, schema_version: 'v1', hash: 'h9', leaf_hash: 'lh9' },
    ],
    merkle_root: 'b'.repeat(64),
    ruleset_version: '1',
    schema_versions: {
      attachment_schema: 'v1',
      contract_schema: 'v1',
      intent_schema: 'v1',
      outcome_schema: 'v1',
    },
    signatures: [
      {
        signer: 'zord_evidence',
        alg: 'ed25519',
        sig: 'sig',
        signed_at: '2026-05-01T12:00:10Z',
      },
    ],
    pack_completeness_score: 1,
    leaf_count: 9,
    required_leaf_count: 5,
    settlement_leaf_present_flag: true,
    attachment_decision_leaf_present_flag: hasAttachment,
    payment_instruction_received: '2026-05-01T12:00:00Z',
    canonical_intent_created: '2026-05-01T12:00:01Z',
    mapping_profile_used: 'auto-generic-test-v1',
    required_fields_status: true,
    tokenization_status: true,
    governance_decision: 'Fail',
    settlement_record_received: '2026-05-01T12:00:02Z',
    canonical_settlement_created: '2026-05-01T12:00:03Z',
    bank_reference: 'UTR172777748433',
    client_reference: intentId === INTENT_A ? 'ZORD_PAY_CONFLICT_A' : 'ZORD_PAY_OK_B',
    attachment_decision: 'MATCH_EXACT',
    match_confidence: 0.9675,
    value_date_check: true,
    amount_match: false,
    created_at: '2026-05-01T12:00:00Z',
    proof_status: 'CERTIFIED',
    proof_score: 100,
    proof_score_breakdown: {
      score: 100,
      components: [
        { check: 'Original Payment Instruction', weight: 20, passed: true, deduction: 0 },
        { check: 'Settlement / Bank Record', weight: 20, passed: true, deduction: 0 },
        { check: 'Match Decision', weight: 20, passed: true, deduction: 0 },
      ],
      deductions: null,
    },
    generated_by: 'system',
    verification_status: false,
    export_count: 0,
    proof_components: {
      payment_instruction_available: true,
      settlement_record_available: true,
      match_decision_available: hasAttachment,
      governance_decision_available: true,
      replay_check_passed: true,
    },
    cryptographic_signatures: {
      raw_intent_hash: 'raw-intent-hash',
      raw_settlement_hash: 'raw-settlement-hash',
      canonical_settlement_hash: 'canonical-settlement-hash',
      attachment_decision_hash: 'attachment-decision-hash',
      governance_decision_hash: 'governance-decision-hash',
      final_evidence_view_hash: 'final-evidence-view-hash',
    },
  }
}

function smokeBatchRows() {
  const partners = ['razorpay', 'cashfree']
  return Array.from({ length: 10 }, (_, i) => {
    const n = i + 1
    const batchId = `smoke-batch-${String(n).padStart(2, '0')}`
    const intended = 850_000 + n * 520_000
    const leakagePct = Number((0.04 + (i % 8) * 0.012).toFixed(4))
    const variance = Math.round(intended * (0.015 + (i % 4) * 0.005)) * (i % 2 === 0 ? -1 : 1)
    const sparse = i === 9
    return {
      batch_id: batchId,
      tenant_id: SESSION_TENANT,
      finality_status: i % 3 === 0 ? 'OPEN' : i % 3 === 1 ? 'PARTIALLY_SETTLED' : 'FULLY_SETTLED',
      total_count: 10 + ((i * 5 + 7) % 22),
      success_count: 8 + (i % 5),
      failed_count: 1,
      pending_count: 1,
      source_reference: partners[i % partners.length],
      ...(sparse
        ? {}
        : {
            total_intended_amount_minor: intended,
            total_variance_minor: variance,
            reversal_exposure_minor: Math.round(intended * 0.006),
            leakage_percentage: leakagePct,
          }),
    }
  })
}

function emptyProdBody(path: string): unknown {
  if (path.endsWith('/intents/batch-ids')) {
    return { items: [{ batch_id: BATCH_ID, intent_count: 0 }] }
  }
  if (path.endsWith('/operations/summary')) {
    return {
      data_available: true,
      tenant_id: SESSION_TENANT,
      computed_at: '2026-06-02T07:00:00Z',
      settlement_confirmation_coverage_pct: 87.4,
      confirmed_matched_value_minor: 42_000_000,
      total_intended_amount_minor: 48_000_000,
      open_exception_queue_count: 12,
      open_exception_queue_value_minor: 18_500_000,
      batch_close_readiness: {
        blocked_batch_count: 3,
        close_ready_batch_count: 5,
        blocked_batch_ids: ['batch_blocked_01'],
        close_ready_batch_ids: ['batch_ready_01'],
      },
    }
  }
  if (path.endsWith('/exceptions/summary')) {
    return {
      data_available: true,
      tenant_id: SESSION_TENANT,
      open_financial_exception_count: 12,
      open_financial_exception_value_minor: 18_500_000,
    }
  }
  if (path.endsWith('/home/disbursement-trend')) {
    return {
      data_available: true,
      tenant_id: SESSION_TENANT,
      range: 'month',
      buckets: [
        {
          label: 'Week 1',
          total_amount: 1_200_000,
          confirmed_amount: 1_050_000,
          review_amount: 80_000,
          intent_count: 42,
        },
        {
          label: 'Week 2',
          total_amount: 1_400_000,
          confirmed_amount: 1_220_000,
          review_amount: 95_000,
          intent_count: 48,
        },
      ],
    }
  }
  if (path.endsWith('/intelligence/batches')) {
    return {
      tenant_id: SESSION_TENANT,
      intelligence_mode: 'GRADE_A',
      status_filter: '',
      batches: smokeBatchRows(),
    }
  }
  if (path.includes('/intelligence/batches/')) {
    return {
      tenant_id: SESSION_TENANT,
      intelligence_mode: 'GRADE_A',
      batch: {
        batch_id: BATCH_ID,
        tenant_id: SESSION_TENANT,
        total_count: 20,
        success_count: 18,
        failed_count: 1,
        pending_count: 1,
        total_confirmed_amount_minor: 52_653.42,
        total_variance_minor: -388.32,
        missing_ref_count: 0,
        settlement_ref_count: 20,
        ambiguity_score: 0.75,
      },
      batch_health: {
        total_confirmed_amount_minor: 52_653.42,
        total_variance_minor: -388.32,
        total_intended_amount_minor: 53_041.74,
        ambiguity_score: 0.75,
        finality_status: 'FULLY_SETTLED',
      },
    }
  }
  if (path.includes('/intelligence/batch_contract/')) {
    return {
      tenant_id: SESSION_TENANT,
      intelligence_mode: 'GRADE_A',
      batch_id: BATCH_ID,
      bank_reference_coverage: '100.00%',
      settlement_ref_count: 20,
      bank_ref_present_count: 20,
      client_ref_present_count: 20,
      client_reference_coverage: '100.00%',
      variance_amount: -388.32,
      orphan_amount: 22_381.29,
      unmatch_amount: 0,
      total_confirmed_amount: 52_653.42,
      match_confidence: 0.75,
      missing_reference_rate: '0.00%',
    }
  }
  if (path.endsWith('/intents/payment-intents') || path.endsWith('/intents/dlq-items')) {
    return { items: [] }
  }
  if (path.endsWith('/evidence/packs')) {
    return { packs: [], total: 0 }
  }
  if (/\/evidence\/batch\/[^/]+\/intents$/.test(path)) {
    return { packs: [], total: 0 }
  }
  if (path.endsWith('/intelligence/timeseries/leakage')) {
    return {
      data_available: true,
      tenant_id: SESSION_TENANT,
      computed_at: '2026-06-09T16:06:41.126247Z',
      window_start: '2026-06-09T00:00:00Z',
      window_end: '2026-06-09T00:00:00Z',
      granularity: 'day',
      series: [
        {
          date: '2026-06-09',
          current_leakage_minor: 5_714_479.85,
          predicted_leakage_minor: 13_498_147,
        },
      ],
    }
  }
  if (path.endsWith('/intelligence/leakage')) {
    return {
      data_available: true,
      tenant_id: SESSION_TENANT,
      computed_at: '2026-06-02T07:00:00Z',
      total_amount_minor: 200_000,
      total_intended_amount_minor: 5_000_000,
      unmatched_amount_minor: 120_000,
      under_settlement_amount_minor: 80_000,
      orphan_amount_minor: 0,
      reversal_exposure_minor: 0,
      total_observed_settled_amount_minor: 4_200_000,
      ambiguous_value_at_risk_minor: 250_000,
      risk_adjusted_leakage_minor: 262_500,
      leakage_percentage: 0.04,
      risk_tier: 'MEDIUM',
      exposure_bands: [
        { band: 'Unmatched Payment Value', amount_minor: 120_000, share_pct: 60 },
        { band: 'Short-Settled Value', amount_minor: 80_000, share_pct: 40 },
        { band: 'Unlinked Settlement Value', amount_minor: 0, share_pct: 0 },
        { band: 'Reversal Exposure', amount_minor: 0, share_pct: 0 },
      ],
      segment_roll_rates: [
        { from_band: 'settled', to_band: 'unmatched', roll_pct: 4.2 },
        { from_band: 'settled', to_band: 'short_settled', roll_pct: 2.1 },
        { from_band: 'short_settled', to_band: 'orphan', roll_pct: 0.8 },
        { from_band: 'orphan', to_band: 'reversal', roll_pct: 0.4 },
      ],
    }
  }
  if (path.endsWith('/intelligence/ambiguity/heatmap')) {
    return {
      data_available: true,
      tenant_id: SESSION_TENANT,
      intelligence_mode: 'GRADE_A',
      batches: [
        {
          batch_id: 'smoke-batch-01',
          total_count: 18,
          exact_match_count: 12,
          high_confidence_count: 4,
          ambiguous_count: 1,
          unresolved_count: 1,
          conflicted_count: 0,
          aggregate_score: 0.88,
          finality_status: 'SETTLED',
        },
        {
          batch_id: 'smoke-batch-02',
          total_count: 22,
          exact_match_count: 10,
          high_confidence_count: 5,
          ambiguous_count: 4,
          unresolved_count: 2,
          conflicted_count: 1,
          aggregate_score: 0.79,
          finality_status: 'REQUIRES_REVIEW',
        },
        {
          batch_id: 'smoke-batch-03',
          total_count: 15,
          exact_match_count: 6,
          high_confidence_count: 3,
          ambiguous_count: 5,
          unresolved_count: 1,
          conflicted_count: 0,
          aggregate_score: 0.71,
          finality_status: 'PROCESSING',
        },
      ],
    }
  }
  if (path.endsWith('/intelligence/ambiguity')) {
    return {
      data_available: true,
      tenant_id: SESSION_TENANT,
      computed_at: '2026-06-02T07:00:00Z',
      value_at_risk_minor: 250_000,
      avg_attachment_confidence: 0.82,
      provider_ref_missing_rate: 0.16,
      ambiguous_intent_count: 12,
      ambiguity_rate: 0.082,
      ambiguous_amount_minor: 410_000,
      total_variance_minor: 75_000,
      reversal_exposure_minor: 22_000,
      unresolved_amount_minor: 18_000,
      unresolved_count: 12,
      total_intended_amount_minor: 34_200_000,
      total_observed_settled_amount_minor: 26_000_000,
      clearing_pct: 82,
      intelligence_headline: '12 intents need match review in the latest ambiguity window.',
      intelligence_body: 'Missing reference cluster on ICICI rail is the top driver.',
      ambiguity_mix_segments: [
        { name: 'High Confidence', pct: 68 },
        { name: 'Low Confidence', pct: 8 },
        { name: 'Ambiguous', pct: 8 },
        { name: 'Missing Refs', pct: 16 },
      ],
      signal_clarity_subtitle: '₹34.2Cr book across 780 intents · ₹8.4Cr needing review',
      signal_clarity_roll_rates: [
        { from_band: 'Current', to_band: 'SMA-0', roll_pct: 9 },
        { from_band: 'SMA-0', to_band: 'SMA-1', roll_pct: 18 },
        { from_band: 'SMA-1', to_band: 'SMA-2', roll_pct: 31 },
        { from_band: 'SMA-2', to_band: 'NPA', roll_pct: 22 },
      ],
      signal_clarity_bands: [
        {
          band: 'Current',
          amount_minor: 26_000_000,
          item_count: 645,
          share_pct: 76,
          tone: 'green',
        },
        {
          band: 'SMA-0',
          amount_minor: 4_100_000,
          item_count: 67,
          share_pct: 12,
          roll_pct: 9,
          tone: 'lime',
        },
        {
          band: 'SMA-1',
          amount_minor: 2_200_000,
          item_count: 38,
          share_pct: 6.4,
          roll_pct: 18,
          tone: 'amber',
        },
        {
          band: 'SMA-2',
          amount_minor: 1_500_000,
          item_count: 18,
          share_pct: 4.4,
          roll_pct: 31,
          tone: 'orange',
        },
        {
          band: 'NPA-2',
          amount_minor: 400_000,
          item_count: 12,
          share_pct: 1.2,
          roll_pct: 22,
          tone: 'red',
        },
      ],
    }
  }
  if (path.endsWith('/intelligence/recommendations')) {
    return {
      data_available: true,
      tenant_id: SESSION_TENANT,
      computed_at: '2026-06-02T07:00:00Z',
      total_actions: 3,
      accepted_actions: 1,
      resolved_actions: 1,
      action_acceptance_rate: 0.33,
      action_resolution_rate: 0.33,
      recommendation_impact_estimate_minor: 300_000,
    }
  }
  if (path.endsWith('/intelligence/pattern/history')) {
    return {
      count: 1,
      intelligence_mode: 'GRADE_A',
      snapshot_type: 'PATTERN',
      snapshots: [
        {
          created_at: '2026-06-02T07:00:00Z',
          snapshot_json: {
            weakest_source_system: 'manual_excel',
            weakest_source_missing_ref_rate: 0.42,
            weakest_provider_id: 'cashfree',
            network_success_pct: '88.2%',
            network_latency_index: 80,
          },
        },
      ],
    }
  }
  if (path.endsWith('/intelligence/patterns')) {
    return {
      data_available: true,
      tenant_id: SESSION_TENANT,
      computed_at: '2026-06-02T07:00:00Z',
      decision_success_rate: '64.95%',
      by_provider: {
        cashfree: {
          total_decisions: 25,
          successful_decision_count: 22,
          decision_success_rate: '88.00%',
          ambiguity_rate: '0.00%',
          unresolved_decisions: 0,
          orphan_rate: '20.00%',
        },
        razorpay: {
          total_decisions: 17,
          successful_decision_count: 15,
          decision_success_rate: '88.24%',
          ambiguity_rate: '0.00%',
          unresolved_decisions: 0,
          orphan_rate: '5.88%',
        },
      },
      batch_anomaly_score: 0.31,
      anomaly_level: 'MEDIUM',
      batch_risk_score: 0.68,
      risk_tier: 'MEDIUM',
      finality_status: 'FULLY_SETTLED',
      total_count: 100,
      success_count: 82,
      failed_count: 4,
      pending_count: 14,
      ambiguous_count: 46,
      unresolved_count: 9,
      risk_driver_breakdown: [
        { label: 'High ambiguity rate', count: 46, share_pct: 72 },
        { label: 'Missing references', count: 12, share_pct: 19 },
        { label: 'Unresolved decisions', count: 9, share_pct: 14 },
        { label: 'Reversal exposure', count: 5, share_pct: 8 },
      ],
      network_health_trend: [
        { label: '28 May', success_pct: '82.0%', latency_index: 72 },
        { label: '29 May', success_pct: '84.5%', latency_index: 74 },
        { label: '30 May', success_pct: '86.0%', latency_index: 76 },
        { label: '31 May', success_pct: '88.0%', latency_index: 78 },
        { label: '01 Jun', success_pct: '88.2%', latency_index: 80 },
      ],
      summary_stats: {
        flagged_decision_count: 72,
        match_confidence_pct: 64,
        total_decision_count: 612,
      },
    }
  }
  if (path.endsWith('/intelligence/pattern')) {
    return {
      data_available: true,
      tenant_id: SESSION_TENANT,
      snapshot_type: 'PATTERN',
      snapshot_id: 'snap-pattern-e2e',
      scope_type: 'BATCH',
      scope_ref: BATCH_ID,
      window_start: '2026-06-01T00:00:00Z',
      window_end: '2026-06-02T07:00:00Z',
      computed_at: '2026-06-02T07:00:00Z',
      model_version: 'deterministic-v1',
      intelligence_mode: 'GRADE_A',
      data: {
        batch_id: BATCH_ID,
        risk_tier: 'HIGH',
        anomaly_level: 'ELEVATED',
        finality_status: 'PARTIALLY_SETTLED',
        batch_anomaly_score: 0.71,
        batch_quality_score: 0.62,
        batch_risk_score: 0.68,
        total_count: 120,
        success_count: 88,
        failed_count: 6,
        pending_count: 26,
        ambiguity_score: 0.24,
        ambiguous_count: 14,
        unresolved_count: 9,
        conflicted_count: 3,
        exact_match_count: 52,
        high_confidence_count: 36,
        prepare_and_sign_recommended: true,
        recommended_action: 'Review ambiguous batch before dispatch',
        duplicate_risk_rate: 0.12,
        duplicate_risk_exposure_minor: 420_000,
        unexplained_variance_amount_minor: 75_000,
        whitelisted_deduction_amount_minor: 12_000,
        over_settlement_amount_minor: 8_000,
        missing_leaf_rate: 0.09,
        weak_evidence_rate: 0.11,
        evidence_pack_coverage: 0.81,
        tenant_manual_review_rate: 0.22,
        settlement_delay_p50_days: 1.2,
        settlement_delay_p95_days: 2,
        computed_at: '2026-06-02T07:00:00Z',
        weakest_source_system: 'manual_excel',
        weakest_source_missing_ref_rate: 0.42,
        weakest_source_manual_review_rate: 0.31,
        weakest_provider_id: 'cashfree',
        risk_signals: [
          { signal: 'missing_client_ref_rate', severity: 'HIGH', value: 0.42, threshold: 0.2, contribution: 0.35 },
        ],
        top_manual_review_reasons: [
          { reason_code: 'MISSING_CLIENT_REF', count: 12, rate: 0.18 },
        ],
        source_quality_patterns: [
          {
            severity: 'HIGH',
            source_system: 'manual_excel',
            manual_review_rate: 0.31,
            missing_client_ref_rate: 0.42,
            low_matchability_rate: 0.4,
            duplicate_risk_rate: 0.12,
            manual_review_amount_minor: 500_000,
          },
        ],
        provider_quality_patterns: [
          {
            severity: 'CRITICAL',
            provider_id: 'cashfree',
            orphan_rate: 0.24,
            ambiguity_rate: 0.05,
            avg_carrier_richness: 0.42,
            avg_parse_confidence: 0.59,
            settlement_delay_p95_days: 2,
          },
        ],
      },
    }
  }
  if (path.endsWith('/ambiguity/velocity') || path.endsWith('/dashboard/bubble-map')) {
    return {
      data_available: true,
      tenant_id: SESSION_TENANT,
      intelligence_mode: 'GRADE_A',
      count: 3,
      batches: [
        { batch_id: 'batch_live_001', amount_value: 2_000_000, amount_at_risk: 245_000 },
        { batch_id: 'batch_002', amount_value: 750_000, amount_at_risk: 12_000 },
        { batch_id: 'batch_003', amount_value: 5_000_000, amount_at_risk: 115_000 },
      ],
    }
  }
  if (path.endsWith('/settlement/observations/batches')) {
    return {
      items: [{ client_batch_id: BATCH_ID }],
      pagination: { page: 1, page_size: 20, total: 1 },
    }
  }
  if (path.endsWith('/settlement/errors')) {
    return {
      items: [
        { source_row_ref: '2', error_stage: 'PARSING', reason_code: 'EMPTY_RAW_ROW', severity: 'LOW' },
      ],
      pagination: { page: 1, page_size: 4, total: 4 },
    }
  }
  if (path.endsWith('/intelligence/defensibility')) {
    return {
      data_available: true,
      tenant_id: SESSION_TENANT,
      computed_at: '2026-06-02T07:00:00Z',
      defensibility_score: 58,
      defensibility_tier: 'STRONG',
      evidence_pack_rate: 0.75,
      audit_ready_pct: 0.72,
      weak_evidence_count: 4,
      governance_coverage_pct: 0.85,
      replayability_pct: 0.9,
      dispute_ready_pct: 0.65,
    }
  }
  if (path.includes('/intelligence/')) {
    return { data_available: false, tenant_id: SESSION_TENANT }
  }
  if (path.endsWith('/ingest-status')) {
    return { status: 'unknown', sources: [] }
  }
  if (path.endsWith('/systems/sync-status')) {
    return { connectors: [], systems: [] }
  }
  return {}
}

function evidenceFixtureBody(path: string, search: URLSearchParams): unknown {
  if (/\/evidence\/batch\/[^/]+\/lineage-graph$/.test(path)) {
    const batchId = path.split('/').slice(-2, -1)[0] ?? EVIDENCE_BATCH
    const root = `${batchId.replace(/[^a-zA-Z0-9]/g, '').toLowerCase()}batchroot`
      .padEnd(64, 'b')
      .slice(0, 64)
    return {
      evidence_pack_id: PACK_BATCH,
      tenant_id: SESSION_TENANT,
      intent_id: '',
      merkle_root: root,
      nodes: [
        {
          id: `${batchId}-settlement-source`,
          label: 'Original Settlement File',
          node_type: 'SOURCE',
          leaf_hash: `${root.slice(0, 48)}aaaaaaaaaaaaaaaa`,
          item_ref: batchId,
          schema_version: 'v1',
        },
        {
          id: `${batchId}-canonical-batch`,
          label: 'Canonical Batch',
          node_type: 'TRANSFORM',
          leaf_hash: `${root.slice(0, 48)}bbbbbbbbbbbbbbbb`,
          item_ref: batchId,
          schema_version: 'v1',
        },
        {
          id: `${batchId}-batch-summary`,
          label: 'Evidence Summary',
          node_type: 'SEAL',
          leaf_hash: `${root.slice(0, 48)}cccccccccccccccc`,
          item_ref: PACK_BATCH,
          schema_version: 'v1',
        },
        {
          id: 'merkle_root',
          label: 'Proof Root',
          node_type: 'SEAL',
          leaf_hash: root,
        },
      ],
      edges: [
        { from: `${batchId}-settlement-source`, to: `${batchId}-canonical-batch`, label: 'canonicalise batch' },
        { from: `${batchId}-canonical-batch`, to: `${batchId}-batch-summary`, label: 'summarise' },
        { from: `${batchId}-batch-summary`, to: 'merkle_root', label: 'seal' },
      ],
    }
  }
  if (/\/evidence\/packs\/[^/]+\/lineage-graph$/.test(path)) {
    const packId = path.split('/').slice(-2, -1)[0] ?? PACK_BATCH
    const root = `${packId.replace(/[^a-zA-Z0-9]/g, '').toLowerCase()}root`.padEnd(64, 'a').slice(0, 64)
    return {
      evidence_pack_id: packId,
      tenant_id: SESSION_TENANT,
      intent_id: packId === PACK_BATCH ? '' : packId === PACK_INTENT_A ? INTENT_A : INTENT_B,
      merkle_root: root,
      nodes: [
        {
          id: `${packId}-source`,
          label: 'Original Payment File',
          node_type: 'SOURCE',
          leaf_hash: `${root.slice(0, 48)}1111111111111111`,
          item_ref: `src-${packId}`,
          schema_version: 'v1',
        },
        {
          id: `${packId}-transform`,
          label: 'Structured Payment Intent',
          node_type: 'TRANSFORM',
          leaf_hash: `${root.slice(0, 48)}2222222222222222`,
          item_ref: `intent-${packId}`,
          schema_version: 'v1',
        },
        {
          id: `${packId}-summary`,
          label: 'Evidence Summary',
          node_type: 'SEAL',
          leaf_hash: `${root.slice(0, 48)}3333333333333333`,
          item_ref: packId,
          schema_version: 'v1',
        },
        {
          id: 'merkle_root',
          label: 'Proof Root',
          node_type: 'SEAL',
          leaf_hash: root,
        },
      ],
      edges: [
        { from: `${packId}-source`, to: `${packId}-transform`, label: 'canonicalise' },
        { from: `${packId}-transform`, to: `${packId}-summary`, label: 'seal' },
        { from: `${packId}-summary`, to: 'merkle_root', label: 'root' },
      ],
    }
  }
  if (/\/evidence\/batch\/[^/]+\/intents$/.test(path)) {
    const batchId = path.split('/').slice(-2, -1)[0] ?? EVIDENCE_BATCH
    if (batchId === EVIDENCE_BATCH) {
      return {
        packs: [
          packSummary(PACK_INTENT_A, {
            intentId: INTENT_A,
            batchId: EVIDENCE_BATCH,
            mode: 'INTELLIGENCE_ATTACH',
            ref: 'ZORD_PAY_A',
            leafCount: 9,
            requiredLeafCount: 5,
          }),
          packSummary(PACK_INTENT_B, {
            intentId: INTENT_B,
            batchId: EVIDENCE_BATCH,
            mode: 'INTELLIGENCE_ATTACH',
            ref: 'ZORD_PAY_B',
            leafCount: 9,
            requiredLeafCount: 9,
          }),
        ],
        total: 2,
      }
    }
    return { packs: [], total: 0 }
  }
  if (/\/evidence\/packs\/[^/]+$/.test(path) && !path.endsWith('/verify') && !path.endsWith('/timeline')) {
    const packId = path.split('/').pop() ?? PACK_BATCH
    const intent =
      packId === PACK_INTENT_A ? INTENT_A : packId === PACK_INTENT_B ? INTENT_B : INTENT_A
    const mode =
      packId === PACK_BATCH ? 'BATCH_PROOF' : 'INTELLIGENCE_INTENT'
    return packFull(packId, intent, mode)
  }
  if (!path.endsWith('/evidence/packs')) return emptyProdBody(path)

  const intentId = search.get('intent_id')
  const batchId = search.get('batch_id')
  if (intentId === INTENT_A) {
    return {
      packs: [
        packSummary(PACK_INTENT_A, {
          intentId: INTENT_A,
          mode: 'INTELLIGENCE_INTENT',
          ref: 'PAY-A',
          leafCount: 9,
          requiredLeafCount: 5,
        }),
      ],
      total: 1,
    }
  }
  if (intentId === INTENT_B) {
    return {
      packs: [
        packSummary(PACK_INTENT_B, {
          intentId: INTENT_B,
          mode: 'INTELLIGENCE_INTENT',
          ref: 'PAY-B',
          leafCount: 9,
          requiredLeafCount: 9,
        }),
      ],
      total: 1,
    }
  }
  if (batchId === EVIDENCE_BATCH) {
    return {
      packs: [
        packSummary(PACK_BATCH, {
          mode: 'BATCH_PROOF',
          ref: 'BATCH-REF',
          batchId: EVIDENCE_BATCH,
          leafCount: 6,
          requiredLeafCount: 6,
        }),
      ],
      total: 1,
    }
  }
  return { packs: [], total: 0 }
}

function installAuthRoutes(page: Page) {
  return Promise.all([
    page.route('**/api/auth/me', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          session: { tenant_id: SESSION_TENANT },
          user: { tenant_id: SESSION_TENANT },
        }),
      })
    }),
    page.route('**/api/sandbox/workspace-api-keys', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ tenant_id: SESSION_TENANT }),
      })
    }),
  ])
}

function installEmptyProdMocks(page: Page) {
  return page.route('**/api/prod/**', async (route) => {
    const method = route.request().method()
    const url = new URL(route.request().url())
    const path = url.pathname
    if (method === 'POST' && /\/evidence\/packs\/[^/]+\/verify$/.test(new URL(route.request().url()).pathname)) {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          status: 'VERIFIED',
          evidence_pack_id: PACK_BATCH,
          checked_at: new Date().toISOString(),
          stored_root: 'a'.repeat(64),
          computed_root: 'a'.repeat(64),
          explanation: 'Merkle root reproduced exactly from live database entries.',
        }),
      })
      return
    }
    if (method !== 'GET') {
      await route.continue()
      return
    }
    if (/\/evidence\/packs\/[^/]+\/export$/.test(path)) {
      const packId = path.split('/').slice(-2, -1)[0] ?? PACK_BATCH
      const format = (url.searchParams.get('format') || 'json').toLowerCase()
      await route.fulfill({
        status: 200,
        contentType: format === 'pdf' ? 'application/pdf' : 'application/json',
        headers: {
          'content-disposition': `attachment; filename="evidence_pack_${packId}.${format === 'pdf' ? 'pdf' : 'json'}"`,
        },
        body:
          format === 'pdf'
            ? '%PDF-1.4\n%mock evidence export\n'
            : JSON.stringify({ evidence_pack_id: packId, export: 'mock' }),
      })
      return
    }
    let body: unknown = emptyProdBody(path)
    if (path.endsWith('/settlement/observations/batches') && url.searchParams.get('client_batch_id')) {
      const clientBatchId = url.searchParams.get('client_batch_id') ?? BATCH_ID
      body = {
        items: [
          {
            settlement_observation_id: 'obs-1',
            client_batch_id: clientBatchId,
            provider_reference: 'razorpay',
            source_system: 'razorpay',
            amount: 2500,
            settlement_status: 'SETTLED',
            source_row_ref: '1',
            client_reference_candidate: 'PAY-001',
            bank_reference: 'UTR123',
          },
        ],
        pagination: { page: 1, page_size: 20, total: 20 },
      }
    }
    if (/\/evidence\/packs\/[^/]+\/timeline$/.test(path)) {
      body = {
        evidence_pack_id: path.split('/').slice(-2, -1)[0],
        intent_id: INTENT_A,
        timeline: [
          { timestamp: '2026-05-01T12:00:00Z', event: 'Payment instruction received', node_id: 'n1' },
        ],
      }
    }
    if (/\/evidence\/packs\/[^/]+$/.test(path) && !path.endsWith('/verify') && !path.endsWith('/timeline')) {
      const packId = path.split('/').pop() ?? PACK_BATCH
      body = packFull(packId, INTENT_A, 'BATCH_PROOF')
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(body),
    })
  })
}

function installEvidenceFixtureMocks(page: Page) {
  return page.route('**/api/prod/**', async (route) => {
    const method = route.request().method()
    const url = new URL(route.request().url())
    const path = url.pathname

    if (method === 'POST' && /\/evidence\/packs\/[^/]+\/verify$/.test(path)) {
      const packId = path.split('/').slice(-2, -1)[0] ?? PACK_BATCH
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          status: 'VERIFIED',
          evidence_pack_id: packId,
          checked_at: new Date().toISOString(),
          stored_root: 'c'.repeat(64),
          computed_root: 'c'.repeat(64),
          explanation: 'Merkle root reproduced exactly from live database entries.',
        }),
      })
      return
    }

    if (method !== 'GET') {
      await route.continue()
      return
    }

    if (/\/evidence\/packs\/[^/]+\/export$/.test(path)) {
      const packId = path.split('/').slice(-2, -1)[0] ?? PACK_BATCH
      const format = (url.searchParams.get('format') || 'json').toLowerCase()
      await route.fulfill({
        status: 200,
        contentType: format === 'pdf' ? 'application/pdf' : 'application/json',
        headers: {
          'content-disposition': `attachment; filename="evidence_pack_${packId}.${format === 'pdf' ? 'pdf' : 'json'}"`,
        },
        body:
          format === 'pdf'
            ? '%PDF-1.4\n%mock evidence export\n'
            : JSON.stringify({ evidence_pack_id: packId, export: 'fixture' }),
      })
      return
    }

    if (path.endsWith('/intelligence/batches')) {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          tenant_id: SESSION_TENANT,
          batches: [{ batch_id: EVIDENCE_BATCH, finality_status: 'OPEN', total_count: 2 }],
        }),
      })
      return
    }
    if (path.endsWith('/intents/payment-intents')) {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          items: [
            { intent_id: INTENT_A, client_payout_ref: 'PAY-A', batch_id: EVIDENCE_BATCH },
            { intent_id: INTENT_B, client_payout_ref: 'PAY-B', batch_id: EVIDENCE_BATCH },
          ],
        }),
      })
      return
    }
    if (path.includes('/evidence/') || path.includes('/intelligence/')) {
      const body = path.includes('/evidence/')
        ? evidenceFixtureBody(path, url.searchParams)
        : { data_available: false, tenant_id: SESSION_TENANT }
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(body),
      })
      return
    }

    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(emptyProdBody(path)),
    })
  })
}

async function preparePage(page: Page, context: BrowserContext, prodMock: (page: Page) => Promise<void>) {
  await installPayoutSessionCookies(context)
  await installAuthRoutes(page)
  await prodMock(page)
  await page.goto(`${BASE_URL}/overview?demo=sandbox`)
  await expect(page.getByText('Financial control overview').first()).toBeVisible({
    timeout: 20_000,
  })
}

async function expectNoRuntimeOverlay(page: Page) {
  await expect(page.getByText('Application error')).toHaveCount(0)
  await expect(page.getByText('Unhandled Runtime Error')).toHaveCount(0)
}

test.describe('payout console pages smoke (empty prod → preview fallbacks)', () => {
  test.beforeEach(async ({ page, context }) => {
    await preparePage(page, context, installEmptyProdMocks)
  })

  for (const { path, title } of CONSOLE_PAGES) {
    test(`${path.split('?')[0]} renders ${title}`, async ({ page }) => {
      const pageErrors: string[] = []
      page.on('pageerror', (err) => pageErrors.push(err.message))

      await page.goto(path)
      await expect(page.getByRole('heading', { name: title, level: 1 }).first()).toBeVisible({ timeout: 25_000 })
      await expectNoRuntimeOverlay(page)
      expect(pageErrors, `page errors on ${path}`).toEqual([])
    })
  }

  test('overview financial control cards render', async ({ page }) => {
    await page.goto('/overview?demo=sandbox')
    await expect(page.getByText('Financial control overview')).toBeVisible({ timeout: 20_000 })
    await expect(page.getByText('Current balance').first()).toBeVisible()
    await expectNoRuntimeOverlay(page)
  })

  test('legacy payout-command-view today redirects to overview', async ({ page }) => {
    await page.goto('/payout-command-view/today?dock=home')
    await expect(page).toHaveURL(/\/overview/, { timeout: 15_000 })
    await expect(page.getByText('Financial control overview')).toBeVisible({ timeout: 20_000 })
  })

  test('batch command center route opens directly', async ({ page }) => {
    await page.goto('/payout-command-view/batch-command-center')
    await expect(page.getByTestId('batch-review-page')).toBeVisible({ timeout: 20_000 })
  })

  test('evidence shows empty pack state when no live packs', async ({ page }) => {
    await page.goto(`/proof?demo=sandbox&batch_id=${encodeURIComponent(BATCH_ID)}`)
    await expect(page.getByRole('heading', { name: 'Evidence', level: 1 })).toBeVisible({
      timeout: 25_000,
    })
    await expect(
      page.getByText(/No evidence packs|pack not found|Select a batch|awaiting|dispatch/i).first(),
    ).toBeVisible({ timeout: 15_000 })
  })

  for (const path of STANDALONE_ROUTES) {
    test(`standalone ${path.split('?')[0]} loads`, async ({ page }) => {
      const pageErrors: string[] = []
      page.on('pageerror', (err) => pageErrors.push(err.message))
      await page.goto(path)
      await page.waitForLoadState('networkidle', { timeout: 25_000 }).catch(() => {})
      await expectNoRuntimeOverlay(page)
      expect(pageErrors, `errors on ${path}`).toEqual([])
    })
  }

  test('batch command center shows Payment Batch Review heading', async ({ page }) => {
    await page.goto('/payout-command-view/batch-command-center')
    await expect(page.getByTestId('batch-review-page')).toBeVisible({ timeout: 25_000 })
    await expect(page.getByRole('heading', { name: 'Payment Batch Review', level: 1 })).toBeVisible({
      timeout: 25_000,
    })
    await expect(page.getByText('File processing status')).toHaveCount(0)
    await expect(page.getByText('Batch Progress')).toHaveCount(0)
    await expectNoRuntimeOverlay(page)
  })
})

test.describe('evidence batch → intent → pack wiring', () => {
  test.beforeEach(async ({ page, context }) => {
    await preparePage(page, context, installEvidenceFixtureMocks)
  })

  test('evidence proof page loads for a batch', async ({ page }) => {
    await page.goto(`${BASE_URL}/proof?demo=sandbox&batch_id=${encodeURIComponent(EVIDENCE_BATCH)}`)
    await expect(page.getByRole('heading', { name: 'Evidence', level: 1 })).toBeVisible({
      timeout: 25_000,
    })
    await expectNoRuntimeOverlay(page)
  })

  test('fan-out API calls on evidence pack detail', async ({ page, context }) => {
    const captures: ProdCapture[] = []
    page.on('request', (req) => {
      if (req.method() !== 'GET') return
      const cap = captureProdGet(req.url())
      if (cap) captures.push(cap)
    })

    await installPayoutSessionCookies(context)
    await page.goto(`${BASE_URL}/payout-command-view/evidence-pack/${encodeURIComponent(PACK_BATCH)}?tab=graph&batch_id=${encodeURIComponent(EVIDENCE_BATCH)}`)
    await expect(page.getByRole('button', { name: /Batch graph/i })).toBeVisible({ timeout: 20_000 })
    await expect(page.getByRole('button', { name: /Intent proofs/i })).toBeVisible({ timeout: 20_000 })
    await expect(page.getByText('Verify proof integrity')).toBeVisible({ timeout: 20_000 })
    await expect(page.getByRole('link', { name: 'Summary' })).toHaveCount(0)

    await page.getByRole('button', { name: /Intent proofs/i }).click()
    await expect(page.getByText('Payment proofs')).toBeVisible({ timeout: 20_000 })
    await expect(page.getByRole('link', { name: 'Summary' })).toBeVisible({ timeout: 20_000 })
    await expect(page.getByText('Verify proof integrity')).toBeVisible({ timeout: 20_000 })
    await page.getByRole('link', { name: 'Summary' }).click()
    await expect(page.getByText('Match confidence')).toBeVisible({ timeout: 20_000 })

    const lineageCalls = captures.filter((c) => /\/evidence\/packs\/[^/]+\/lineage-graph$/.test(c.pathname))
    expect(lineageCalls.length).toBeGreaterThan(0)
    const batchLineageCalls = captures.filter((c) =>
      c.pathname.endsWith(`/evidence/batch/${encodeURIComponent(EVIDENCE_BATCH)}/lineage-graph`),
    )
    expect(batchLineageCalls.length).toBeGreaterThan(0)

    await installPayoutSessionCookies(context)
    await page.goto(`${BASE_URL}/payout-command-view/evidence-pack/${encodeURIComponent(PACK_INTENT_A)}?tab=summary&batch_id=${encodeURIComponent(EVIDENCE_BATCH)}`)
    await expect(page.getByText('Match confidence')).toBeVisible({ timeout: 20_000 })
    await expect(page.getByText('96.75%')).toBeVisible({ timeout: 20_000 })
    await expect(page.getByText('Governance decision')).toBeVisible({ timeout: 20_000 })
    await expect(page.getByText('Fail')).toBeVisible({ timeout: 20_000 })
    await expect(page.getByText('To complete this proof:')).toBeVisible({ timeout: 20_000 })
    await expect(page.getByText('Confirm match decision')).toBeVisible({ timeout: 20_000 })
  })

  test('evidence graph export buttons request the wired export endpoints', async ({ page }) => {
    await installPayoutSessionCookies(page.context())
    await page.goto(
      `${BASE_URL}/payout-command-view/evidence-pack/${encodeURIComponent(PACK_BATCH)}?tab=graph&batch_id=${encodeURIComponent(EVIDENCE_BATCH)}`,
    )
    await expect(page.getByRole('button', { name: 'Export PDF' })).toBeVisible({ timeout: 20_000 })
    await expect(page.getByRole('button', { name: /Export JSON/i })).toBeVisible({ timeout: 20_000 })

    const requests: string[] = []
    page.on('request', (req) => {
      if (req.url().includes('/api/prod/evidence/batch/')) requests.push(req.url())
    })

    const [pdfDownload] = await Promise.all([
      page.waitForEvent('download'),
      page.getByRole('button', { name: 'Export PDF' }).click(),
    ])
    expect(pdfDownload.suggestedFilename()).toBe('evidence_batch_e2e-evidence-batch_intents.pdf')
    await expect(page.getByRole('button', { name: /Export JSON/i })).toBeEnabled({ timeout: 20_000 })

    const [jsonDownload] = await Promise.all([
      page.waitForEvent('download'),
      page.getByRole('button', { name: /Export JSON/i }).click(),
    ])
    expect(jsonDownload.suggestedFilename()).toBe('evidence_batch_e2e-evidence-batch_intents.json')
    expect(requests.some((url) => url.includes('/api/prod/evidence/batch/e2e-evidence-batch/intents'))).toBe(true)
  })
})
