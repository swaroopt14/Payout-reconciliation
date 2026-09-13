package recon

import (
	"strings"
	"time"
)

// MerchantBookFact is the merchant's own left-side book (invoice/order).
// It is the genuine 2-way counterpart. PSP payment vs PSP settlement is not.
type MerchantBookFact struct {
	ID          string
	InvoiceID   string
	OrderID     string
	PaymentID   string
	PayoutID    string
	AmountMinor int64
	Currency    string
	DueAt       time.Time
	BatchID     string
}

type DisputeFact struct {
	ID          string
	PaymentID   string
	AmountMinor int64
	Currency    string
	Status      string
}

func merchantBooksGap(pay PaymentFact, merch *MerchantBookFact) (int64, string, bool) {
	if merch == nil {
		return 0, "", false
	}
	pc := strings.ToUpper(strings.TrimSpace(pay.Currency))
	mc := strings.ToUpper(strings.TrimSpace(merch.Currency))
	if pc != "" && mc != "" && pc != mc {
		return 0, "merchant_currency_mismatch", true
	}
	if merch.AmountMinor != pay.AmountMinor {
		gap := merch.AmountMinor - pay.AmountMinor
		if gap < 0 {
			gap = -gap
		}
		return gap, "merchant_amount_mismatch", true
	}
	return 0, "", false
}

func payoutMerchantGap(p PayoutFact, merch *MerchantBookFact) (int64, string, bool) {
	if merch == nil {
		return 0, "", false
	}
	pc := strings.ToUpper(strings.TrimSpace(p.Currency))
	mc := strings.ToUpper(strings.TrimSpace(merch.Currency))
	if pc != "" && mc != "" && pc != mc {
		return 0, "merchant_currency_mismatch", true
	}
	if merch.AmountMinor != p.AmountMinor {
		gap := merch.AmountMinor - p.AmountMinor
		if gap < 0 {
			gap = -gap
		}
		return gap, "merchant_amount_mismatch", true
	}
	return 0, "", false
}

func openChargeback(disputes []DisputeFact) bool {
	for _, d := range disputes {
		st := strings.ToLower(strings.TrimSpace(d.Status))
		if st == "" || st == "open" || st == "lost" || st == "won_pending" {
			return true
		}
	}
	return false
}
