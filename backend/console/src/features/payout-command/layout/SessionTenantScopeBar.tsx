'use client'

import { useSessionTenant } from '@/services/auth/useSessionTenantId'
import { LiveApiHealthPanel } from './LiveApiHealthPanel'

type SessionTenantScopeBarProps = {
  batchId?: string
  onBatchIdChange?: (value: string) => void
  onAfterFetch?: () => void
  /** Hide Batch-Id field (batch is a filter, not a tenant source). */
  showBatchId?: boolean
}

export function SessionTenantScopeBar({
  batchId = '',
  onBatchIdChange,
  onAfterFetch,
  showBatchId = true,
}: SessionTenantScopeBarProps) {
  const { tenantId, tenantReady, tenantStatus, tenantFetching, refreshTenant } = useSessionTenant()

  const handleRefresh = async () => {
    const result = await refreshTenant()
    if (result.ok) onAfterFetch?.()
  }

  const showWarning = tenantReady && !tenantId.trim()

  return (
    <div className="space-y-3">
      <div
        className="rounded-xl border border-slate-200/90 bg-slate-50/80 px-3.5 py-3 shadow-sm"
        aria-label="Workspace session scope"
      >
        <div className="flex flex-col gap-3 lg:flex-row lg:items-end lg:justify-between">
          <div className="min-w-0 flex-1 space-y-2">
            <p className="text-[11px] font-semibold uppercase tracking-[0.08em] text-slate-500">Workspace scope</p>
            <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
              <span className="text-[12px] font-medium text-slate-600">Workspace ID</span>
              <span className="font-mono text-[13px] text-slate-900" title={tenantId || undefined}>
                {tenantId.trim() || '-'}
              </span>
              {!tenantReady ? <span className="text-[12px] text-slate-500">Loading session…</span> : null}
            </div>
            {tenantStatus ? (
              <p className={`text-[12px] leading-relaxed ${showWarning ? 'text-[#0B1324]' : 'text-slate-600'}`}>
                {tenantStatus}
              </p>
            ) : null}
            {showWarning ? (
              <p className="text-[12px] leading-relaxed text-[#0B1324]">
                Sign in with a workspace. Tenant comes from the session API, not cookies or local storage.
              </p>
            ) : null}
          </div>

          <div className="flex w-full flex-col gap-2 sm:flex-row sm:flex-wrap sm:items-end lg:w-auto lg:min-w-[280px]">
            {showBatchId && onBatchIdChange ? (
              <label className="flex min-w-[200px] flex-1 flex-col gap-1">
                <span className="text-[11px] font-semibold uppercase tracking-[0.08em] text-slate-500">Batch-Id</span>
                <input
                  value={batchId}
                  onChange={(e) => onBatchIdChange(e.target.value)}
                  placeholder="Optional filter for live API checks"
                  className="h-9 rounded-lg border border-slate-200 bg-white px-2.5 font-mono text-[13px] text-slate-900 outline-none focus:border-[#0B1324]/20/55 focus:ring-2 focus:ring-[#0B1324]/20"
                />
              </label>
            ) : null}
            <button
              type="button"
              onClick={() => void handleRefresh()}
              disabled={tenantFetching}
              className="inline-flex h-9 shrink-0 items-center justify-center rounded-lg bg-slate-900 px-4 text-[13px] font-semibold text-white transition hover:bg-slate-800 disabled:cursor-not-allowed disabled:opacity-50"
            >
              {tenantFetching ? 'Refreshing…' : 'Refresh session'}
            </button>
          </div>
        </div>
      </div>
      <LiveApiHealthPanel tenantReady={tenantReady} batchId={batchId} />
    </div>
  )
}
