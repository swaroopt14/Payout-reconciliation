package razorpay

import "strings"

// NormalizePaymentStatus maps Razorpay status strings onto the existing
// recon vocabulary (lowercase). Unknown values become "unknown".
const (
	PayoutPending    = "pending"
	PayoutScheduled  = "scheduled"
	PayoutQueued     = "queued"
	PayoutProcessing = "processing"
	PayoutProcessed  = "processed"
	PayoutReversed   = "reversed"
	PayoutCancelled  = "cancelled"
	PayoutRejected   = "rejected"
	PayoutFailed     = "failed"
)

// NormalizePayoutStatus keeps Razorpay payout lifecycle names exactly (lowercase).
func NormalizePayoutStatus(status string) string {
	s := strings.ToLower(strings.TrimSpace(status))
	switch s {
	case PayoutPending, PayoutScheduled, PayoutQueued, PayoutProcessing,
		PayoutProcessed, PayoutReversed, PayoutCancelled, PayoutRejected, PayoutFailed:
		return s
	case "canceled":
		return PayoutCancelled
	default:
		return s
	}
}

func PayoutRank(status string) int {
	switch NormalizePayoutStatus(status) {
	case PayoutPending:
		return 1
	case PayoutScheduled:
		return 2
	case PayoutQueued:
		return 3
	case PayoutProcessing, PayoutCancelled, PayoutRejected, PayoutFailed:
		return 4
	case PayoutProcessed:
		return 5
	case PayoutReversed:
		return 6
	default:
		return 0
	}
}

func IsPayoutOpen(status string) bool {
	switch NormalizePayoutStatus(status) {
	case PayoutPending, PayoutScheduled, PayoutQueued, PayoutProcessing:
		return true
	default:
		return false
	}
}

func IsPayoutFailedLike(status string) bool {
	switch NormalizePayoutStatus(status) {
	case PayoutFailed, PayoutCancelled, PayoutRejected:
		return true
	default:
		return false
	}
}

// IsPayoutNoNetOutflow reports whether a payout in this status moves no net
// money out of the merchant's account: it failed, was cancelled or rejected
// (failed-like), or was reversed after processing (the money came back).
// Such a payout is never an expected debit (D15, L5). Pending, scheduled,
// queued, processing and processed payouts are NOT covered — they stay
// expected debits until a bank debit lands or a confirmed failure/reversal
// arrives (D16).
//
// This is deliberately separate from IsPayoutFailedLike: reversed is not a
// failure (payouttruth and recon/payout.go depend on that distinction).
func IsPayoutNoNetOutflow(status string) bool {
	return IsPayoutFailedLike(status) || NormalizePayoutStatus(status) == PayoutReversed
}

func IsPayoutProcessed(status string) bool {
	return NormalizePayoutStatus(status) == PayoutProcessed
}

func NormalizePaymentStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "created":
		return "created"
	case "authorized":
		return "authorized"
	case "captured":
		return "captured"
	case "failed":
		return "failed"
	case "refunded":
		return "refunded"
	case "partially_refunded", "partial_refund":
		return "partially_refunded"
	default:
		return "unknown"
	}
}
