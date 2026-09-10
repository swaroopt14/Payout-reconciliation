import type { DockId } from '@/services/payout-command/model'
import { canonicalDockPath } from '@/services/payout-command/canonicalDockPath'

/** Canonical India Finance Controller routes. */
export function financeDockHref(id: DockId): string {
  return canonicalDockPath(id)
}
