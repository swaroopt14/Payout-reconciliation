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
	TaxMinor    int64
	CGSTMinor   int64
	SGSTMinor   int64
	IGSTMinor   int64
	TDSMinor    int64
	HSN         string
	// ReuploadAmountMinor (B5): amount carried by a later upload of the same
	// merchant row. AmountMinor keeps the original; a difference is VARIANCE.
	ReuploadAmountMinor *int64
}

// ReasonMerchantAmountChangedOnReupload: the merchant re-uploaded the same
// invoice/payment/payout with a different amount_minor. The original is kept
// (never overwritten) and the row is VARIANCE until the merchant resolves it.
const ReasonMerchantAmountChangedOnReupload = "merchant_amount_changed_on_reupload"

// merchantReuploadGap reports an amount changed by a merchant re-upload.
func merchantReuploadGap(merch *MerchantBookFact) (int64, bool) {
	if merch == nil || merch.ReuploadAmountMinor == nil || *merch.ReuploadAmountMinor == merch.AmountMinor {
		return 0, false
	}
	gap := *merch.ReuploadAmountMinor - merch.AmountMinor
	if gap < 0 {
		gap = -gap
	}
	return gap, true
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
	if gap, ok := merchantReuploadGap(merch); ok {
		return gap, ReasonMerchantAmountChangedOnReupload, true
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
	if gap, ok := merchantReuploadGap(merch); ok {
		return gap, ReasonMerchantAmountChangedOnReupload, true
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
