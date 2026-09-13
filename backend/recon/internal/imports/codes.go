package imports

const (
	ErrMissingRequiredColumn = "MISSING_REQUIRED_COLUMN"
	ErrUnknownColumn         = "UNKNOWN_COLUMN"
	ErrInvalidDate           = "INVALID_DATE"
	ErrAmbiguousDate         = "AMBIGUOUS_DATE"
	ErrInvalidTimestamp      = "INVALID_TIMESTAMP"
	ErrInvalidAmount         = "INVALID_AMOUNT"
	ErrWrongAmountUnit       = "WRONG_AMOUNT_UNIT"
	ErrAmountOverflow        = "AMOUNT_OVERFLOW"
	ErrInvalidCurrency       = "INVALID_CURRENCY"
	ErrCurrencyMismatch      = "CURRENCY_MISMATCH"
	ErrInvalidUTR            = "INVALID_UTR"
	ErrMissingEntityID       = "MISSING_ENTITY_ID"
	ErrMissingSettlementID   = "MISSING_SETTLEMENT_ID"
	ErrMissingPaymentID      = "MISSING_PAYMENT_ID"
	ErrUnsupportedLineType   = "UNSUPPORTED_LINE_TYPE"
	ErrDuplicateFile         = "DUPLICATE_FILE"
	ErrDuplicateRow          = "DUPLICATE_ROW"
	ErrRowTooLarge           = "ROW_TOO_LARGE"
	ErrMalformedJSON         = "MALFORMED_JSON"
	ErrMalformedCSV          = "MALFORMED_CSV"
	ErrUnsupportedEncoding   = "UNSUPPORTED_ENCODING"
)

func MessageFor(code string) string {
	switch code {
	case ErrMissingRequiredColumn:
		return "A required column is missing."
	case ErrInvalidDate, ErrInvalidTimestamp:
		return "Timestamp could not be parsed with an allowed layout."
	case ErrInvalidAmount:
		return "Amount must be an integer currency subunit."
	case ErrWrongAmountUnit:
		return "Amount unit does not match the selected profile."
	case ErrInvalidCurrency, ErrCurrencyMismatch:
		return "Currency is not a valid ISO-4217 code or does not match the import."
	case ErrInvalidUTR:
		return "UTR is a placeholder value."
	case ErrUnsupportedLineType:
		return "Line type is not payment, refund, transfer, or adjustment."
	case ErrDuplicateFile:
		return "This file hash was already imported."
	case ErrDuplicateRow:
		return "This row hash was already seen in the import."
	case ErrMissingEntityID:
		return "entity_id is required."
	case ErrMissingSettlementID:
		return "settlement_id is required."
	default:
		return code
	}
}

type FatalError struct {
	Code    string
	Message string
}

func (e *FatalError) Error() string { return e.Message }

const (
	StatusCreated          = "created"
	StatusUploaded         = "uploaded"
	StatusValidating       = "validating"
	StatusValidated        = "validated"
	StatusPartial          = "partial"
	StatusCommitting       = "committing"
	StatusCommitted        = "committed"
	StatusUploadFailed     = "upload_failed"
	StatusValidationFailed = "validation_failed"
	StatusCommitFailed     = "commit_failed"
	StatusCancelled        = "cancelled"
	StatusDuplicate        = "duplicate"
)

const (
	RowValid                   = "valid"
	RowInvalid                 = "invalid"
	RowDuplicate               = "duplicate"
	RowAcceptedWithoutValidUTR = "accepted_without_valid_utr"
	RowInserted                = "inserted"
	RowRejected                = "rejected"
)

const (
	TypeBankCSV        = "bank_statement_csv"
	TypeSettlementCSV  = "settlement_recon_csv"
	TypeSettlementJSON = "settlement_recon_json"
	TypeMerchantBooks  = "merchant_books_csv"
)

const NextStepRunRecon = "run_reconciliation"

const (
	CopyBankImported       = "bank observation imported"
	CopySettlementImported = "settlement observation imported"
	CopyMerchantImported   = "merchant books imported"
)
