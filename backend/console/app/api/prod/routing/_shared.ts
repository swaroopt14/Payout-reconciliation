import { BACKEND_SERVICES, buildUrl } from '@/config/api.endpoints'

export function routerUrl(endpoint: keyof typeof BACKEND_SERVICES.ROUTER.ENDPOINTS) {
  return buildUrl('ROUTER', BACKEND_SERVICES.ROUTER.ENDPOINTS[endpoint])
}
