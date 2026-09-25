package razorpay

import "testing"

func TestIsPayoutNoNetOutflow(t *testing.T) {
	cases := map[string]bool{
		"failed": true, "cancelled": true, "canceled": true, "rejected": true,
		"reversed": true, " REVERSED ": true,
		"pending": false, "scheduled": false, "queued": false,
		"processing": false, "processed": false, "": false, "weird": false,
	}
	for status, want := range cases {
		if got := IsPayoutNoNetOutflow(status); got != want {
			t.Errorf("IsPayoutNoNetOutflow(%q) = %v, want %v", status, got, want)
		}
	}
}

// TestIsPayoutFailedLike_Unchanged pins the existing contract that
// payouttruth/types.go and recon/payout.go rely on: reversed is NOT failed-like.
func TestIsPayoutFailedLike_Unchanged(t *testing.T) {
	cases := map[string]bool{
		"failed": true, "cancelled": true, "canceled": true, "rejected": true,
		"reversed": false, "pending": false, "processing": false, "processed": false,
	}
	for status, want := range cases {
		if got := IsPayoutFailedLike(status); got != want {
			t.Errorf("IsPayoutFailedLike(%q) = %v, want %v", status, got, want)
		}
	}
}
