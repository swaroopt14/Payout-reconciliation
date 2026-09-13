package recon

import "time"

const (
	ObligationInvoiceDue = "invoice_due"
	ObligationPayoutDue  = "payout_due"
)

// ExpectedCashEvent is an obligation the merchant should see settle, not a MATCHED claim.
type ExpectedCashEvent struct {
	ID          string
	Kind        string
	EntityID    string
	AmountMinor int64
	Currency    string
	DueAt       time.Time
	Direction   string
}

func ObligationsFromMerchant(facts []MerchantBookFact) []ExpectedCashEvent {
	var out []ExpectedCashEvent
	for _, f := range facts {
		kind := ObligationInvoiceDue
		dir := DirectionInbound
		entity := f.PaymentID
		if f.PayoutID != "" {
			kind = ObligationPayoutDue
			dir = DirectionOutbound
			entity = f.PayoutID
		}
		id := firstNonEmpty(f.ID, f.InvoiceID, f.OrderID, f.PaymentID, f.PayoutID)
		out = append(out, ExpectedCashEvent{
			ID: id, Kind: kind, EntityID: entity,
			AmountMinor: f.AmountMinor, Currency: f.Currency,
			DueAt: f.DueAt, Direction: dir,
		})
	}
	return out
}
