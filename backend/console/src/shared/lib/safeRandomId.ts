/**
 * UUID-ish id that works outside secure contexts (HTTP IP deploys),
 * where `crypto.randomUUID` is missing in the browser.
 */
export function safeRandomId(): string {
  const c = typeof globalThis !== 'undefined' ? globalThis.crypto : undefined
  if (c && typeof c.randomUUID === 'function') {
    try {
      return c.randomUUID()
    } catch {
      /* insecure context / restricted crypto */
    }
  }
  return `${Date.now()}-${Math.random().toString(36).slice(2, 11)}-${Math.random().toString(36).slice(2, 11)}`
}
