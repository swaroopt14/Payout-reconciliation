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
	SourceRow   int64
	RowHash     string
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
		row := MerchantBookRow{
			InvoiceID: get("invoice_id"), OrderID: get("order_id"),
			PaymentID: get("payment_id"), PayoutID: get("payout_id"),
			AmountMinor: amt, Currency: cur, DueAt: due, BatchID: get("batch_id"),
			SourceRow: rowNum,
		}
		row.RowHash = HashCanonical(map[string]any{
			"invoice_id": row.InvoiceID, "payment_id": row.PaymentID,
			"payout_id": row.PayoutID, "amount_minor": row.AmountMinor, "currency": row.Currency,
		})
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

func parseMerchantDate(s string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339, "2006-01-02", "02/01/2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}
