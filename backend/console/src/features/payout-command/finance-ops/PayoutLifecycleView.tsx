'use client'

import { useState } from 'react'
import Link from 'next/link'
import { formatPaise } from './reasonCopy'
import { StatusBadge } from './razorpayChrome'
import { payoutStatusTone, type RazorpayPayoutStatus } from './razorpayPayoutStatus'
import { PaymentProviderBadge } from './PaymentProviderBadge'
import { reconToneClass, type PayoutLifecycle } from './payoutLifecycleModel'
import { FinanceTimelineLadder } from './FinanceTimelineLadder'
import type { FinanceEntityTimeline } from '@/services/payout-command/prod-api/financeTypes'

export type LifecycleTab =
  | 'overview'
  | 'events'
  | 'provider'
  | 'bank'
  | 'settlement'
  | 'ledger'
  | 'evidence'

const TABS: { id: LifecycleTab; label: string }[] = [
  { id: 'overview', label: 'Overview' },
  { id: 'events', label: 'Events' },
  { id: 'provider', label: 'Provider' },
  { id: 'bank', label: 'Bank' },
  { id: 'settlement', label: 'Settlement' },
  { id: 'ledger', label: 'Ledger' },
  { id: 'evidence', label: 'Evidence' },
]

function asPayoutStatus(status: string): RazorpayPayoutStatus {
  const s = status.toLowerCase()
  if (
    s === 'pending' ||
    s === 'scheduled' ||
    s === 'queued' ||
    s === 'processing' ||
    s === 'processed' ||
    s === 'reversed' ||
    s === 'cancelled' ||
    s === 'rejected' ||
    s === 'failed'
  ) {
    return s
  }
  return 'processing'
}

export function PayoutLifecycleView({
  life,
  variant = 'drawer',
  initialTab = 'events',
  traceHref,
  capturedTimeline,
  timelineLoading = false,
  evidenceRefs = null,
  providerRecord = null,
}: {
  life: PayoutLifecycle
  variant?: 'drawer' | 'page'
  initialTab?: LifecycleTab
  traceHref?: string
  capturedTimeline?: FinanceEntityTimeline | null
  timelineLoading?: boolean
  evidenceRefs?: Record<string, unknown> | null
  providerRecord?: Record<string, unknown> | null
}) {
  const [tab, setTab] = useState<LifecycleTab>(initialTab)
  const compact = variant === 'drawer'

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center gap-2">
        <StatusBadge tone={payoutStatusTone(asPayoutStatus(life.providerStatus))}>
          {life.providerStatus}
        </StatusBadge>
        <span
          className={`inline-flex h-6 items-center rounded-[4px] px-2 text-[11px] font-semibold ${reconToneClass(life.reconResult)}`}
        >
          {life.reconResult}
        </span>
        <PaymentProviderBadge provider={life.providerName} />
        {life.lifecyclePassed ? (
          <span className="text-[12px] font-medium text-[#147A3F]">Lifecycle · Passed ✓</span>
        ) : String(life.providerStatus || '').toLowerCase() === 'failed' ? (
          <span className="text-[12px] font-medium text-[#C0372A]">Lifecycle · Stopped at failure</span>
        ) : life.exposureMinor > 0 ? (
          <span className="text-[12px] font-medium text-[#B36B00]">
            Exposure {formatPaise(life.exposureMinor, 2)}
          </span>
        ) : (
          <span className="text-[12px] font-medium text-[#2B6CB0]">In flight</span>
        )}
      </div>

      {traceHref && compact ? (
        <Link href={traceHref} className="inline-flex text-[13px] font-medium text-[#528FF0] hover:underline">
          Open full trace →
        </Link>
      ) : null}

      <section className="rounded-[8px] border border-[#E6E8EB] bg-[#FAFBFC] px-4 py-3">
        <p className="text-[10px] font-semibold uppercase tracking-[0.08em] text-[#94A3B8]">Where is the money?</p>
        <ol className={`mt-3 ${compact ? 'space-y-2' : 'grid gap-2 sm:grid-cols-5'}`}>
          {life.money.nodes.map((node, i) => (
            <li key={node.id} className="flex items-start gap-2">
              {!compact && i > 0 ? <span className="hidden pt-2 text-[#CBD5E1] sm:inline">→</span> : null}
              <div className="min-w-0">
                <p className="text-[12px] font-semibold text-[#0F172A]">{node.label}</p>
                <p className="truncate font-mono text-[11px] text-[#64748B]">{node.sub}</p>
              </div>
            </li>
          ))}
        </ol>
        <p
          className={`mt-3 text-[12px] font-medium ${
            life.money.outcome === 'accounted'
              ? 'text-[#147A3F]'
              : life.money.outcome === 'unaccounted'
                ? 'text-[#C0372A]'
                : 'text-[#B36B00]'
          }`}
        >
          {life.money.outcome === 'accounted' ? '✓ ' : life.money.outcome === 'unaccounted' ? '⚠ ' : ''}
          {life.money.caption}
        </p>
      </section>

      <div className="flex gap-4 overflow-x-auto border-b border-[#E6E8EB]">
        {TABS.map((item) => {
          const on = tab === item.id
          return (
            <button
              key={item.id}
              type="button"
              onClick={() => setTab(item.id)}
              className={`-mb-px shrink-0 border-b-2 pb-2 text-[13px] ${
                on
                  ? 'border-[#1A1A1A] font-semibold text-[#1A1A1A]'
                  : 'border-transparent font-medium text-[#6B6B6B] hover:text-[#1A1A1A]'
              }`}
            >
              {item.label}
            </button>
          )
        })}
      </div>

      {tab === 'overview' ? (
        <div className="space-y-4">
          <dl className="grid grid-cols-2 gap-3">
            <OverviewStat label="Amount" value={formatPaise(life.amountMinor, 2)} />
            <OverviewStat label="Mode" value={life.mode || '—'} />
            <OverviewStat label="Provider" value={life.providerStatus || '—'} />
            <OverviewStat label="Reconciliation" value={life.reconResult || 'not run'} />
            <OverviewStat label="UTR" value={life.utr || 'null'} mono />
            <OverviewStat
              label="Exposure"
              value={life.exposureMinor ? formatPaise(life.exposureMinor, 2) : '₹0.00'}
            />
          </dl>
          {capturedTimeline?.steps.find((s) => s.kind === 'ROUTING_RAIL_SELECTION' && s.captured) ? (
            <div className="rounded-[8px] border border-[#E6E8EB] p-4">
              <p className="text-[11px] font-semibold uppercase tracking-[0.08em] text-[#94A3B8]">Route</p>
              <p className="mt-2 text-[14px] font-semibold text-[#0F172A]">{life.mode || 'captured rail'}</p>
            </div>
          ) : (
            <p className="text-[13px] text-[#94A3B8]">Routing decision was not captured.</p>
          )}
        </div>
      ) : null}

      {tab === 'events' ? (
        timelineLoading ? (
          <p className="text-[13px] text-[#94A3B8]">Loading captured timeline…</p>
        ) : (
          <FinanceTimelineLadder timeline={capturedTimeline ?? null} />
        )
      ) : null}

      {tab === 'provider' ? (
        providerRecord ? (
          <JsonBlock title="Razorpay / provider record" value={JSON.stringify(providerRecord, null, 2)} />
        ) : (
          <p className="text-[13px] text-[#94A3B8]">Provider record was not captured for this payout.</p>
        )
      ) : null}
      {tab === 'bank' ? (
        capturedTimeline?.steps.find((s) => s.kind === 'BANK_SIDE_CASH_MOVEMENT' && s.captured) ? (
          <JsonBlock
            title="Bank observation"
            value={JSON.stringify(
              capturedTimeline.steps.find((s) => s.kind === 'BANK_SIDE_CASH_MOVEMENT'),
              null,
              2,
            )}
          />
        ) : (
          <p className="text-[13px] text-[#94A3B8]">Bank-side cash movement was not captured.</p>
        )
      ) : null}
      {tab === 'settlement' ? (
        capturedTimeline?.steps.find((s) => s.kind === 'SETTLEMENT_ACCOUNTING_RECORD' && s.captured) ? (
          <JsonBlock
            title="Settlement"
            value={JSON.stringify(
              capturedTimeline.steps.find((s) => s.kind === 'SETTLEMENT_ACCOUNTING_RECORD'),
              null,
              2,
            )}
          />
        ) : (
          <p className="text-[13px] text-[#94A3B8]">Settlement accounting record was not captured.</p>
        )
      ) : null}
      {tab === 'ledger' ? (
        capturedTimeline?.reconciliation ? (
          <JsonBlock title="Reconciliation decision" value={JSON.stringify(capturedTimeline.reconciliation, null, 2)} />
        ) : (
          <p className="text-[13px] text-[#94A3B8]">Reconciliation has not been run for this payout.</p>
        )
      ) : null}

      {tab === 'evidence' ? (
        evidenceRefs && Object.keys(evidenceRefs).length > 0 ? (
          <JsonBlock title="Evidence refs" value={JSON.stringify(evidenceRefs, null, 2)} />
        ) : (
          <p className="text-[13px] text-[#94A3B8]">Evidence refs were not captured.</p>
        )
      ) : null}
    </div>
  )
}

function OverviewStat({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="rounded-[8px] border border-[#E6E8EB] bg-[#FAFBFC] p-3">
      <p className="text-[10px] font-semibold uppercase tracking-[0.08em] text-[#94A3B8]">{label}</p>
      <p className={`mt-1 text-[13px] font-semibold text-[#0F172A] ${mono ? 'break-all font-mono' : 'tabular-nums'}`}>
        {value}
      </p>
    </div>
  )
}

function JsonBlock({ title, value }: { title: string; value: string }) {
  return (
    <section>
      <p className="text-[11px] font-semibold uppercase tracking-[0.08em] text-[#94A3B8]">{title}</p>
      <pre className="mt-2 max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-[8px] border border-[#EEF0F3] bg-[#FAFBFC] p-3 font-mono text-[11px] text-[#334155]">
        {value}
      </pre>
    </section>
  )
}
