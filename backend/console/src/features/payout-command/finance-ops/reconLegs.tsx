import type { FinanceReconLeg } from '@/services/payout-command/prod-api/financeTypes'
import { reconToneClass } from './payoutLifecycleModel'

export function legReasonLabel(reason?: string) {
  switch (reason) {
    case 'merchant_psp_settled':
      return 'Books agree with PSP settlement'
    case 'merchant_psp_payout':
      return 'PSP processed the payout'
    case 'merchant_psp_no_movement':
      return 'PSP and books agree no money moved'
    case 'merchant_psp_bank':
      return 'Bank cash movement proven'
    case 'merchant_psp_bank_no_movement':
      return 'Bank agrees no cash moved'
    case 'settlement_without_bank':
      return 'Settlement without bank credit'
    case 'payout_missing_bank':
      return 'Processed without bank debit'
    case 'bank_not_proven':
      return 'Bank not proven'
    default:
      return reason || '—'
  }
}

export function ReconLegBadge({
  label,
  leg,
}: {
  label: string
  leg?: FinanceReconLeg | null
}) {
  if (!leg?.result) {
    return (
      <div>
        <p className="text-[10px] font-semibold uppercase tracking-[0.06em] text-[#8F8F8F]">{label}</p>
        <span className="mt-1 inline-flex h-6 items-center rounded-[4px] bg-[#F3F4F6] px-2 text-[11px] font-semibold text-[#475569]">
          —
        </span>
      </div>
    )
  }
  return (
    <div>
      <p className="text-[10px] font-semibold uppercase tracking-[0.06em] text-[#8F8F8F]">{label}</p>
      <span
        className={`mt-1 inline-flex h-6 items-center rounded-[4px] px-2 text-[11px] font-semibold ${reconToneClass(String(leg.result))}`}
      >
        {leg.result}
      </span>
      <p className="mt-1 max-w-[180px] text-[11px] leading-snug text-[#64748B]">{legReasonLabel(leg.reason)}</p>
    </div>
  )
}
