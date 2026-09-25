package imports

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"time"
)

type MerchantBookRow struct {
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
	SourceRow   int64
	// RowHash is the row identity (B5): invoice/payment/payout id + currency,
	// NOT the amount, so an edited re-upload maps onto the same fact.
	RowHash string
	// LegacyRowHash is the pre-B5 hash (included amount_minor and tax). Kept
	// so rows stored under the old hash are found, never duplicated.
	LegacyRowHash string
	// ReuploadAmountMinor is set when a later upload of the same identity
	// carried a different amount_minor. The original AmountMinor is kept;
	// recon surfaces the difference as VARIANCE merchant_amount_changed_on_reupload.
	ReuploadAmountMinor *int64 `json:",omitempty"`
}

// MerchantRowIdentityHash is the amount-free identity of a merchant book row.
// When a row carries no invoice/payment/payout id there is no stable
// identity, so the amount stays in the hash (two id-less rows with different
// amounts are different rows).
func MerchantRowIdentityHash(row MerchantBookRow) string {
	id := map[string]any{
		"identity": "merchant_book_v2",
		"invoice_id": row.InvoiceID, "payment_id": row.PaymentID,
		"payout_id": row.PayoutID, "currency": row.Currency,
	}
	if !row.HasIdentity() {
		id["amount_minor"] = row.AmountMinor
	}
	return HashCanonical(id)
}

// HasIdentity reports whether the row carries any business id.
func (r MerchantBookRow) HasIdentity() bool {
	return r.InvoiceID != "" || r.PaymentID != "" || r.PayoutID != ""
}

// SameIdentity reports whether two rows are the same merchant book fact.
func (r MerchantBookRow) SameIdentity(o MerchantBookRow) bool {
	if r.RowHash != "" && r.RowHash == o.RowHash {
		return true
	}
	if r.LegacyRowHash != "" && (r.LegacyRowHash == o.LegacyRowHash || r.LegacyRowHash == o.RowHash) {
		return true
	}
	return r.HasIdentity() && r.InvoiceID == o.InvoiceID && r.PaymentID == o.PaymentID &&
		r.PayoutID == o.PayoutID && strings.EqualFold(r.Currency, o.Currency)
}

// legacyMerchantRowHash reproduces the pre-B5 row hash exactly.
func legacyMerchantRowHash(row MerchantBookRow) string {
	return HashCanonical(map[string]any{
		"invoice_id": row.InvoiceID, "payment_id": row.PaymentID,
		"payout_id": row.PayoutID, "amount_minor": row.AmountMinor, "currency": row.Currency,
		"tax_minor": row.TaxMinor, "cgst_minor": row.CGSTMinor, "sgst_minor": row.SGSTMinor,
		"igst_minor": row.IGSTMinor, "tds_minor": row.TDSMinor,
	})
}

func ParseMerchantBooksCSV(raw []byte, fileHash string) (ParseOutcome, error) {
	r := csv.NewReader(bytes.NewReader(raw))
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	header, err := r.Read()
	if err != nil {
		if err == io.EOF {
			return ParseOutcome{}, &FatalError{Code: ErrMalformedCSV, Message: "empty CSV"}
		}
		return ParseOutcome{}, &FatalError{Code: ErrMalformedCSV, Message: err.Error()}
	}
	idx := merchantHeaders(header)
	if _, ok := idx["amount_minor"]; !ok {
		return ParseOutcome{}, &FatalError{Code: ErrMissingRequiredColumn, Message: "missing amount_minor"}
	}
	if _, hasPay := idx["payment_id"]; !hasPay {
		if _, hasPayout := idx["payout_id"]; !hasPayout {
			if _, hasInv := idx["invoice_id"]; !hasInv {
				return ParseOutcome{}, &FatalError{Code: ErrMissingRequiredColumn, Message: "missing payment_id, payout_id, or invoice_id"}
			}
		}
	}
	out := ParseOutcome{FileHash: fileHash}
	rowNum := int64(0)
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ParseOutcome{}, &FatalError{Code: ErrMalformedCSV, Message: err.Error()}
		}
		rowNum++
		get := func(k string) string {
			i, ok := idx[k]
			if !ok || i >= len(rec) {
				return ""
			}
			return strings.TrimSpace(rec[i])
		}
		amt, aerr := strconv.ParseInt(get("amount_minor"), 10, 64)
		res := RowResult{RowNumber: rowNum}
		if aerr != nil || amt < 0 || strings.Contains(get("amount_minor"), ".") {
			res.Status = RowInvalid
			res.ErrorCode = ErrInvalidAmount
			res.ErrorMessage = MessageFor(ErrInvalidAmount)
			out.Rows = append(out.Rows, res)
			continue
		}
		cur := strings.ToUpper(get("currency"))
		if cur == "" {
			cur = "INR"
		}
		if len(cur) != 3 {
			res.Status = RowInvalid
			res.ErrorCode = ErrInvalidCurrency
			res.ErrorMessage = MessageFor(ErrInvalidCurrency)
			out.Rows = append(out.Rows, res)
			continue
		}
		var due time.Time
		if ds := get("due_date"); ds != "" {
			parsed, ok := parseMerchantDate(ds)
			if !ok {
				res.Status = RowInvalid
				res.ErrorCode = ErrInvalidDate
				res.ErrorMessage = MessageFor(ErrInvalidDate)
				out.Rows = append(out.Rows, res)
				continue
			}
			due = parsed
		}
		tax, tok := parseOptionalMinor(get("tax_minor"))
		cgst, cok := parseOptionalMinor(get("cgst_minor"))
		sgst, sok := parseOptionalMinor(get("sgst_minor"))
		igst, iok := parseOptionalMinor(get("igst_minor"))
		tds, tdok := parseOptionalMinor(get("tds_minor"))
		if !tok || !cok || !sok || !iok || !tdok {
			res.Status = RowInvalid
			res.ErrorCode = ErrInvalidAmount
			res.ErrorMessage = MessageFor(ErrInvalidAmount)
			out.Rows = append(out.Rows, res)
			continue
		}
		row := MerchantBookRow{
			InvoiceID: get("invoice_id"), OrderID: get("order_id"),
			PaymentID: get("payment_id"), PayoutID: get("payout_id"),
			AmountMinor: amt, Currency: cur, DueAt: due, BatchID: get("batch_id"),
			TaxMinor: tax, CGSTMinor: cgst, SGSTMinor: sgst, IGSTMinor: igst, TDSMinor: tds,
			HSN: get("hsn"), SourceRow: rowNum,
		}
		row.RowHash = MerchantRowIdentityHash(row)
		row.LegacyRowHash = legacyMerchantRowHash(row)
		rawJSON, _ := json.Marshal(row)
		res.Status = RowValid
		res.RowHash = row.RowHash
		res.Raw = rawJSON
		res.Merchant = &row
		_ = fileHash
		out.Rows = append(out.Rows, res)
	}
	return out, nil
}

func merchantHeaders(header []string) map[string]int {
	aliases := map[string]string{
		"invoice_id": "invoice_id", "invoice": "invoice_id",
		"order_id": "order_id", "order": "order_id",
		"payment_id": "payment_id", "payout_id": "payout_id",
		"amount_minor": "amount_minor", "amount": "amount_minor",
		"currency": "currency", "due_date": "due_date", "due_at": "due_date",
		"batch_id": "batch_id",
		"tax_minor": "tax_minor", "tax": "tax_minor",
		"cgst_minor": "cgst_minor", "cgst": "cgst_minor",
		"sgst_minor": "sgst_minor", "sgst": "sgst_minor",
		"igst_minor": "igst_minor", "igst": "igst_minor",
		"tds_minor": "tds_minor", "tds": "tds_minor",
		"hsn": "hsn", "sac": "hsn",
	}
	out := map[string]int{}
	for i, h := range header {
		key := strings.ToLower(strings.TrimSpace(h))
		if canon, ok := aliases[key]; ok {
			out[canon] = i
		}
	}
	return out
}

func parseOptionalMinor(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, true
	}
	if strings.Contains(s, ".") {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

func parseMerchantDate(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339, "2006-01-02", "02/01/2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}
