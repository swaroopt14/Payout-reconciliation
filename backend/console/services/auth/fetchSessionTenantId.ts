'use client'

export type SessionTenantSource = 'auth_me' | 'none'

export type SessionTenantFetchResult = {
  tenantId: string
  ok: boolean
  message: string
  source: SessionTenantSource
}

function parseAuthMeTenant(data: unknown): string {
  const payload = data as
    | { session?: { tenant_id?: string }; user?: { tenant_id?: string; tenantId?: string } }
    | null
  return (
    payload?.session?.tenant_id?.trim() ||
    payload?.user?.tenant_id?.trim() ||
    payload?.user?.tenantId?.trim() ||
    ''
  )
}

function clearStaleLocalTenant() {
  try {
    if (typeof window !== 'undefined') window.localStorage.removeItem('zord_tenant_id')
  } catch {
    /* ignore */
  }
}

/**
 * Resolve tenant id from the signed-in session API only.
 * BFF `/api/prod/*` routes inject the same tenant from httpOnly cookies.
 * Env, localStorage, and batch lookup are not tenant sources.
 */
export async function fetchSessionTenantId(_options?: {
  batchId?: string
}): Promise<SessionTenantFetchResult> {
  try {
    const res = await fetch('/api/auth/me', { credentials: 'include', cache: 'no-store' })
    if (res.ok) {
      const data = await res.json().catch(() => null)
      const tid = parseAuthMeTenant(data)
      clearStaleLocalTenant()
      if (tid) {
        return { tenantId: tid, ok: true, message: 'Tenant loaded from your session.', source: 'auth_me' }
      }
      return {
        tenantId: '',
        ok: false,
        message: 'Signed in, but tenant_id was not on the session. Sign in with a workspace that has a tenant.',
        source: 'none',
      }
    }
    if (res.status === 401 || res.status === 403) {
      clearStaleLocalTenant()
      return {
        tenantId: '',
        ok: false,
        message: 'Not signed in. Sign in, then try again.',
        source: 'none',
      }
    }
  } catch {
    /* network */
  }

  return {
    tenantId: '',
    ok: false,
    message: 'Could not load session. Sign in and retry.',
    source: 'none',
  }
}
