// Shared zord analytics helpers. No static import of services/analytics here —
// the seeded store is loaded only through withDemoAnalytics (demoGate.ts) when the demo flag is on.
export { resolveRequestContext, withNoStore, withDemoAnalytics, demoJson } from './demoGate'

export const dynamic = 'force-dynamic'
