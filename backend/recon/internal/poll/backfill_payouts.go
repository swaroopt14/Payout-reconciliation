package poll

import (
	"context"
	"fmt"
	"time"

	"zord-outcome-engine/internal/payouttruth"
	"zord-outcome-engine/internal/poll/providers/razorpay"

	"github.com/google/uuid"
)

// PayoutBackfillProvider is implemented by providers that can list payouts.
// Kept separate from BackfillProvider so existing providers/fakes compile.
type PayoutBackfillProvider interface {
	ListPayoutsPage(ctx context.Context, from, to time.Time, skip, count int) (razorpay.NeutralPage[razorpay.NeutralPayout], error)
}

// payoutProcessor returns the SAME payout-truth intake the webhook path uses
// (observe.Processor.applyPayout -> payouttruth.Processor.Process over the
// same store). Canonical payouts are keyed by (tenant_id, connector_id,
// provider, payout_id) — canonical_payouts_uidx — so a payout seen by both a
// webhook and the timer pull is counted once (D18, D26).
func (s *BackfillService) payoutProcessor() (*payouttruth.Processor, error) {
	ps, ok := s.store.(payouttruth.Store)
	if !ok {
		return nil, fmt.Errorf("backfill store does not support payout truth")
	}
	return payouttruth.NewProcessor(ps), nil
}

// RunPayouts runs a payouts timer-backfill job.
func (s *BackfillService) RunPayouts(ctx context.Context, jobID string) (BackfillSummary, error) {
	return s.run(ctx, jobID, ResourcePayouts)
}

func (s *BackfillService) paginatePayouts(ctx context.Context, job *BackfillJob, cursor *BackfillCursor, provider BackfillProvider) error {
	pp, ok := provider.(PayoutBackfillProvider)
	if !ok {
		return fmt.Errorf("provider does not support payout backfill")
	}
	proc, err := s.payoutProcessor()
	if err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if jobRow, err := s.store.GetJob(ctx, job.ID); err == nil && jobRow.Status == JobCancelled {
			return fmt.Errorf("job cancelled")
		}
		page, err := pp.ListPayoutsPage(ctx, job.WindowFrom, job.WindowTo, cursor.PageSkip, cursor.PageCount)
		if err != nil {
			return err
		}
		snapshot := *job
		cursorSnap := *cursor
		if err := s.store.RunInTx(ctx, func(ctx context.Context) error {
			if err := s.persistPayoutPage(ctx, proc, &snapshot, &cursorSnap, page); err != nil {
				return err
			}
			if !page.HasMore || len(page.Items) == 0 {
				cursorSnap.Status = CursorComplete
			} else {
				cursorSnap.PageSkip += len(page.Items)
				cursorSnap.PagesCompleted++
			}
			exp := s.now().Add(DefaultLeaseTTL)
			cursorSnap.LeaseExpiresAt = &exp
			return s.store.AdvanceCursor(ctx, cursorSnap)
		}); err != nil {
			return err
		}
		*job = snapshot
		*cursor = cursorSnap
		if cursor.Status == CursorComplete {
			return nil
		}
	}
}

func (s *BackfillService) persistPayoutPage(ctx context.Context, proc *payouttruth.Processor, job *BackfillJob, cursor *BackfillCursor, page razorpay.NeutralPage[razorpay.NeutralPayout]) error {
	receiptID := uuid.Must(uuid.NewV7()).String()
	if err := s.store.InsertResponseReceipt(ctx, ResponseReceipt{
		ID:                receiptID,
		TenantID:          job.TenantID,
		ConnectorID:       job.ConnectorID,
		BackfillJobID:     job.ID,
		Provider:          job.Provider,
		ResourceType:      job.ResourceType,
		RequestPath:       page.Meta.Path,
		RequestQueryHash:  page.Meta.QueryHash,
		ResponseStatus:    page.Meta.Status,
		ResponseHash:      page.Meta.Hash,
		PageSkip:          cursor.PageSkip,
		PageCount:         cursor.PageCount,
		ProviderItemCount: len(page.Items),
	}); err != nil {
		return err
	}
	var lastID string
	for _, item := range page.Items {
		job.FetchedCount++
		backfillFetchedTotal.Inc()
		// sourceEventID is empty (as for payments): identity is the payout id +
		// payload hash, so an unchanged payout re-pulled by the next timer run
		// is a Duplicate, not new history.
		obs, err := payouttruth.MapNeutral(job.TenantID, job.ConnectorID, job.Provider, SourceAPIBackfill, "", item, s.now())
		if err != nil {
			return err
		}
		res, err := proc.Process(ctx, obs)
		if err != nil {
			return err
		}
		switch res.Kind {
		case payouttruth.KindInserted:
			job.InsertedCount++
			backfillInsertedTotal.Inc()
			observeObservationSource(SourceAPIBackfill)
		case payouttruth.KindUpdated, payouttruth.KindObserved:
			job.UpdatedCount++
			backfillUpdatedTotal.Inc()
			observeObservationSource(SourceAPIBackfill)
		case payouttruth.KindDuplicate:
			job.DuplicateCount++
			backfillDuplicatesTotal.Inc()
		}
		lastID = item.PayoutID
	}
	cursor.LastProviderID = lastID
	cursor.LastResponseHash = page.Meta.Hash
	return s.store.UpdateJob(ctx, *job)
}
