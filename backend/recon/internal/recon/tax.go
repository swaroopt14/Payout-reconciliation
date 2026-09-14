package recon

import "strings"

// TaxLineFact is one invoice tax component (CGST/SGST/IGST/GST). It is a
// SourceObservation kind, not a derived PSP fee/tax split.
type TaxLineFact struct {
	ID          string
	PaymentID   string
	InvoiceID   string
	Component   string
	AmountMinor int64
	Currency    string
	HSN         string
}

func invoiceGSTMinor(merch *MerchantBookFact, lines []TaxLineFact) (int64, bool) {
	var gst int64
	seen := false
	for _, tl := range lines {
		if isTDSComponent(tl.Component) {
			continue
		}
		if isGSTComponent(tl.Component) || tl.Component == "" {
			gst += tl.AmountMinor
			seen = true
		}
	}
	if seen {
		return gst, true
	}
	if merch == nil {
		return 0, false
	}
	if merch.TaxMinor != 0 {
		return merch.TaxMinor, true
	}
	comp := merch.CGSTMinor + merch.SGSTMinor + merch.IGSTMinor
	if comp != 0 {
		return comp, true
	}
	return 0, false
}

func isGSTComponent(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "gst", "cgst", "sgst", "igst", "tax", "tax_line":
		return true
	default:
		return false
	}
}

func isTDSComponent(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "tds", "tcs":
		return true
	default:
		return false
	}
}

func settlementTaxMinor(lines []SettlementLine) int64 {
	var n int64
	for _, l := range lines {
		n += l.TaxMinor
	}
	return n
}

func taxLineGap(settlement []SettlementLine, merch *MerchantBookFact, taxLines []TaxLineFact) (int64, string, bool) {
	inv, ok := invoiceGSTMinor(merch, taxLines)
	if !ok {
		return 0, "", false
	}
	obs := settlementTaxMinor(settlement)
	if inv == obs {
		return 0, "", false
	}
	gap := inv - obs
	if gap < 0 {
		gap = -gap
	}
	return gap, "tax_line_mismatch", true
}
