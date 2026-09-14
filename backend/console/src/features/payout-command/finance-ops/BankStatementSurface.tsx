'use client'

import { useCallback, useEffect, useMemo, useState } from 'react'
import { getFinanceBankTransactions } from '@/services/payout-command/prod-api/financeApi'
import type { FinanceBankTxn } from '@/services/payout-command/prod-api/financeTypes'
import {
  PageHeader,
  PaymentsEmptyState,
  RZ_MUTED,
  RZ_PAGE,
  RZ_WRAP,
  StatusBadge,
} from './razorpayChrome'
import { formatPaise } from './reasonCopy'

const RANGE_OPTIONS = [{ value: 'all', label: 'All time' }]

const TABS = [
  { id: 'all', label: 'All' },
  { id: 'MATCHED', label: 'Matched' },
  { id: 'VARIANCE', label: 'Variance' },
  { id: 'UNRESOLVED', label: 'Unresolved' },
] as const

type Tab = (typeof TABS)[number]['id']

function resultTone(result?: string): 'captured' | 'pending' | 'failed' | 'created' {
  const s = String(result || '').toUpperCase()
  if (s === 'MATCHED') return 'captured'
  if (s === 'VARIANCE' || s === 'CONFLICTED') return 'failed'
  if (s === 'UNRESOLVED' || s === 'AMBIGUOUS') return 'pending'
  return 'created'
}

function formatDate(value?: string) {
  if (!value) return '—'
  const d = new Date(value)
  if (Number.isNaN(d.getTime()) || d.getFullYear() < 1971) return '—'
  return d.toLocaleDateString('en-IN', { day: '2-digit', month: 'short', year: 'numeric' })
}

export function BankStatementSurface() {
  const [tab, setTab] = useState<Tab>('all')
  const [search, setSearch] = useState('')
  const [rows, setRows] = useState<FinanceBankTxn[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    const res = await getFinanceBankTransactions()
    if (!res.ok || !res.data) {
      setError(res.status === 401 ? 'Sign in to load bank statements.' : 'Could not load bank transactions.')
      setRows([])
      setTotal(0)
      setLoading(false)
      return
    }
    setRows(res.data.bank_transactions ?? [])
    setTotal(res.data.total ?? res.data.bank_transactions?.length ?? 0)
    setLoading(false)
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    return rows.filter((row) => {
      const result = String(row.recon_result || '').toUpperCase()
      if (tab !== 'all' && result !== tab) return false
      if (!q) return true
      return [row.utr, row.bank_txn_id, row.id, row.description, row.recon_reason]
        .filter(Boolean)
        .some((v) => String(v).toLowerCase().includes(q))
    })
  }, [rows, search, tab])

  return (
    <div className={RZ_PAGE}>
      <div className={RZ_WRAP}>
        <PageHeader
          title="Bank statements"
          range="all"
          onRangeChange={() => undefined}
          rangeOptions={RANGE_OPTIONS}
          docsHref="/proof"
        />
        <p className={`mt-2 ${RZ_MUTED}`}>
          Statement rows with the last recon result. MATCHED is only shown when the engine scored it; otherwise the
          row stays unresolved.
        </p>

        {loading ? (
          <p className={`mt-8 ${RZ_MUTED}`}>Loading bank transactions…</p>
        ) : error ? (
          <p className="mt-8 text-[13px] text-[#B91C1C]">{error}</p>
        ) : rows.length === 0 ? (
          <PaymentsEmptyState
            title="No bank rows"
            body="Upload a bank statement from Uploads, then run reconciliation to score MATCHED or VARIANCE."
          />
        ) : (
          <>
            <div className="mt-5 flex flex-wrap items-center justify-between gap-3">
              <div className="flex gap-4 overflow-x-auto">
                {TABS.map((t) => {
                  const on = t.id === tab
                  return (
                    <button
                      key={t.id}
                      type="button"
                      onClick={() => setTab(t.id)}
                      className={`-mb-px whitespace-nowrap border-b-2 pb-2 text-[13px] ${
                        on
                          ? 'border-[#1A1A1A] font-semibold text-[#1A1A1A]'
                          : 'border-transparent font-medium text-[#6B6B6B]'
                      }`}
                    >
                      {t.label}
                    </button>
                  )
                })}
              </div>
              <input
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="Search UTR or description"
                className="h-9 w-full max-w-[280px] rounded-[6px] border border-[#E6E8EB] px-3 text-[13px] outline-none"
              />
            </div>
            <p className={`mt-3 ${RZ_MUTED}`}>
              Showing {filtered.length} of {total} bank rows
            </p>
            <div className="mt-3 overflow-x-auto rounded-[8px] border border-[#E6E8EB] bg-white">
              <table className="w-full min-w-[960px] text-left text-[13px]">
                <thead className="bg-[#FAFBFC] text-[11px] font-semibold uppercase tracking-[0.06em] text-[#8F8F8F]">
                  <tr>
                    <th className="px-5 py-3">Date</th>
                    <th className="px-4 py-3">UTR</th>
                    <th className="px-4 py-3">Description</th>
                    <th className="px-4 py-3 text-right">Credit</th>
                    <th className="px-4 py-3 text-right">Debit</th>
                    <th className="px-5 py-3">Recon</th>
                  </tr>
                </thead>
                <tbody>
                  {filtered.map((row) => (
                    <tr key={row.id || row.bank_txn_id} className="border-t border-[#F3F4F6]">
                      <td className="px-5 py-3 text-[#6B6B6B]">{formatDate(row.value_date)}</td>
                      <td className="px-4 py-3 font-mono text-[12px] text-[#1A1A1A]">{row.utr || '—'}</td>
                      <td className="px-4 py-3 text-[#6B6B6B]">{row.description || row.bank_txn_id || row.id}</td>
                      <td className="px-4 py-3 text-right tabular-nums">
                        {row.credit_minor ? formatPaise(row.credit_minor, 2) : '—'}
                      </td>
                      <td className="px-4 py-3 text-right tabular-nums">
                        {row.debit_minor ? formatPaise(row.debit_minor, 2) : '—'}
                      </td>
                      <td className="px-5 py-3">
                        <StatusBadge tone={resultTone(row.recon_result)}>{row.recon_result || 'UNRESOLVED'}</StatusBadge>
                        {row.recon_reason ? (
                          <p className={`mt-1 font-mono text-[11px] ${RZ_MUTED}`}>{row.recon_reason}</p>
                        ) : null}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </>
        )}
      </div>
    </div>
  )
}
