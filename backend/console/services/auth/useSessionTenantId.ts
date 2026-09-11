'use client'

import { useCallback, useEffect, useState } from 'react'
import { fetchSessionTenantId, type SessionTenantFetchResult } from './fetchSessionTenantId'

const TENANT_UPDATED_EVENT = 'zord-tenant-updated'

function broadcastTenantId(tenantId: string) {
  if (typeof window === 'undefined') return
  window.dispatchEvent(new CustomEvent(TENANT_UPDATED_EVENT, { detail: { tenantId } }))
}

/**
 * Tenant id for display and client cache keys.
 * Source is `/api/auth/me` only. Prod BFF injects tenant from the same session.
 */
export function useSessionTenantId(): string {
  const { tenantId } = useSessionTenant()
  return tenantId
}

export type UseSessionTenantResult = {
  tenantId: string
  /** True after the first auth/me resolution attempt finishes. */
  tenantReady: boolean
  /** Last manual or automatic fetch status message. */
  tenantStatus: string
  tenantFetching: boolean
  /** Re-run `/api/auth/me`. */
  refreshTenant: (options?: { batchId?: string }) => Promise<SessionTenantFetchResult>
}

export function useSessionTenant(): UseSessionTenantResult {
  const [tenantId, setTenantId] = useState('')
  const [tenantReady, setTenantReady] = useState(false)
  const [tenantStatus, setTenantStatus] = useState('')
  const [tenantFetching, setTenantFetching] = useState(false)

  const refreshTenant = useCallback(async (_options?: { batchId?: string }) => {
    setTenantFetching(true)
    try {
      const result = await fetchSessionTenantId()
      setTenantId(result.tenantId)
      setTenantStatus(result.message)
      setTenantReady(true)
      if (result.tenantId) broadcastTenantId(result.tenantId)
      return result
    } finally {
      setTenantFetching(false)
    }
  }, [])

  useEffect(() => {
    const onTenantUpdated = (event: Event) => {
      const tid = (event as CustomEvent<{ tenantId?: string }>).detail?.tenantId?.trim() ?? ''
      if (tid) setTenantId(tid)
    }
    window.addEventListener(TENANT_UPDATED_EVENT, onTenantUpdated)
    return () => window.removeEventListener(TENANT_UPDATED_EVENT, onTenantUpdated)
  }, [])

  useEffect(() => {
    let cancelled = false
    void (async () => {
      const result = await fetchSessionTenantId()
      if (cancelled) return
      setTenantId(result.tenantId)
      setTenantStatus(result.message)
      setTenantReady(true)
    })()
    return () => {
      cancelled = true
    }
  }, [])

  return { tenantId, tenantReady, tenantStatus, tenantFetching, refreshTenant }
}
