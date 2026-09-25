package razorpay

import (
	"context"
	"time"
)

// ListPayoutsPage maps one RazorpayX /payouts page into provider-neutral
// payouts for the timer backfill (D26). Recon-side only: the scheduler never
// calls this, it only asks recon to run a backfill job.
func (a *BackfillAdapter) ListPayoutsPage(ctx context.Context, from, to time.Time, skip, count int) (NeutralPage[NeutralPayout], error) {
	page, meta, err := a.client.ListPayoutsPage(ctx, TimeWindow{From: from, To: to}, skip, count)
	out := NeutralPage[NeutralPayout]{
		Skip:    skip,
		Count:   count,
		Meta:    meta,
		HasMore: len(page.Items) >= count && count > 0,
	}
	if err != nil {
		return out, err
	}
	out.Items = make([]NeutralPayout, 0, len(page.Items))
	for _, item := range page.Items {
		out.Items = append(out.Items, NeutralFromPayout(item))
	}
	return out, nil
}
