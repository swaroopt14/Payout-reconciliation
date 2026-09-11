import { BACKEND_SERVICES, buildUrl } from '@/config/api.endpoints'
import { ACCESS_COOKIE_NAME } from '@/services/auth/server'
import type { NextRequest } from 'next/server'

export function routerUrl(endpoint: keyof typeof BACKEND_SERVICES.ROUTER.ENDPOINTS) {
  return buildUrl('ROUTER', BACKEND_SERVICES.ROUTER.ENDPOINTS[endpoint])
}

export function routerUpstreamHeaders(request: NextRequest, tenantId: string): Record<string, string> {
  const headers: Record<string, string> = {
    'content-type': 'application/json',
    'x-tenant-id': tenantId,
  }
  const access = request.cookies.get(ACCESS_COOKIE_NAME)?.value?.trim()
  if (access) headers.authorization = `Bearer ${access}`
  const serviceToken = (process.env.ROUTER_AUTH_TOKEN || process.env.ZORD_ROUTER_TOKEN || '').trim()
  if (serviceToken) headers['x-router-token'] = serviceToken
  return headers
}
