'use client'

import { useCallback, useEffect, useState, type Dispatch, type SetStateAction } from 'react'
import { useSessionTenant } from '@/services/auth/useSessionTenantId'
import { fetchJournalSidebarBatches } from '../journalBatchCache'
import { JOURNAL_BATCH_IDS_BFF_PATH, LIVE_JOURNAL_POLL_MS } from '../journalConstants'
import type { JournalBatchRecord } from '@/services/payout-command/prod-api/mapIntentEngineBatch'
import { getIntentJournalBatchIdsForSession } from '@/services/payout-command/prod-api/intentJournalApi'

export type JournalSidebarFetchMeta = {
  ok: boolean
  status: number
  sidebarCount: number
  bffPath: string
}

export function useJournalSidebarBatches(options: {
  enabled: boolean
  initialBatchId?: string
  selectedBatchId: string
  setSelectedBatchId: Dispatch<SetStateAction<string>>
  pollMs?: number
}) {
  const { enabled, initialBatchId, selectedBatchId, setSelectedBatchId, pollMs = LIVE_JOURNAL_POLL_MS } = options
  const { tenantId, tenantReady } = useSessionTenant()

  const [batches, setBatches] = useState<JournalBatchRecord[]>([])
  const [feedLoaded, setFeedLoaded] = useState(false)
  const [feedError, setFeedError] = useState<string | null>(null)
  const [feedMeta, setFeedMeta] = useState<JournalSidebarFetchMeta | null>(null)
  const [syncAt, setSyncAt] = useState<Date | null>(null)

  const refresh = useCallback(async () => {
    if (!tenantId.trim()) {
      setBatches([])
      setFeedError('No tenant on session - sign in with a workspace that has a tenant_id.')
      return
    }
    const fetchRes = await getIntentJournalBatchIdsForSession()
    setFeedMeta({
      ok: fetchRes.ok,
      status: fetchRes.status,
      sidebarCount: fetchRes.data?.items?.length ?? 0,
      bffPath: JOURNAL_BATCH_IDS_BFF_PATH,
    })

    if (!fetchRes.ok || !fetchRes.data) {
      const statusHint =
        fetchRes.status === 401
          ? ' Sign in required.'
          : fetchRes.status === 403
            ? ' Session tenant does not match this resource. Sign in again.'
            : fetchRes.status === 502
              ? ' Intent-engine unreachable.'
              : ''
      setFeedError(`Could not load batches for your session (intent-engine BFF).${statusHint}`)
      setBatches([])
      return
    }

    setFeedError(null)
    const batchRows = await fetchJournalSidebarBatches(tenantId)
    setBatches(batchRows)
    setSelectedBatchId((prev) => {
      if (batchRows.length === 0) return ''
      if (initialBatchId && batchRows.some((b) => b.batchId === initialBatchId)) return initialBatchId
      if (prev && batchRows.some((b) => b.batchId === prev)) return prev
      return batchRows[0]!.batchId
    })
    setSyncAt(new Date())
  }, [tenantId, initialBatchId, setSelectedBatchId])

  useEffect(() => {
    if (!enabled || !tenantReady || !tenantId.trim()) {
      setBatches([])
      setFeedLoaded(false)
      return
    }

    let cancelled = false
    const tick = async () => {
      if (cancelled) return
      try {
        await refresh()
      } catch {
        if (!cancelled) setFeedError('Could not refresh journal batches.')
      }
      if (!cancelled) setFeedLoaded(true)
    }

    void tick()
    const id = window.setInterval(() => void tick(), pollMs)
    return () => {
      cancelled = true
      window.clearInterval(id)
    }
  }, [enabled, tenantReady, tenantId, refresh, pollMs])

  useEffect(() => {
    if (initialBatchId) setSelectedBatchId(initialBatchId)
  }, [initialBatchId, setSelectedBatchId])

  return {
    tenantId,
    tenantReady,
    batches,
    selectedBatchId,
    feedLoaded,
    feedError,
    feedMeta,
    syncAt,
    refresh,
  }
}
