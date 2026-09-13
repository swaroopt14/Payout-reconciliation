package imports

import (
	"encoding/json"
	"time"

	"zord-outcome-engine/internal/poll/providers/razorpay"
	"zord-outcome-engine/models"
)

type Import struct {
	ID              string
	TenantID        string
	ConnectorID     string
	AccountID       string
	ImportType      string
	SourceType      string
	ProviderMode    string
	FileName        string
	ContentType     string
	FileSizeBytes   int64
	FileSHA256      string
	StorageURI      string
	Payload         []byte
	Currency        string
	Status          string
	DetectedColumns []string
	SelectedMapping map[string]string
	AmountUnit      string
	Timezone        string
	Profile         string
	RowsSeen        int64
	ValidRows       int64
	InvalidRows     int64
	DuplicateRows   int64
	InsertedRows    int64
	UpdatedRows     int64
	RejectedRows    int64
	CreatedAt       time.Time
	ValidatedAt     time.Time
	CommittedAt     time.Time
}

type RowResult struct {
	ID                string
	ImportID          string
	RowNumber         int64
	RowHash           string
	Status            string
	CanonicalRecordID string
	ErrorCode         string
	ErrorMessage      string
	Raw               json.RawMessage
	Settlement        *razorpay.NeutralSettlementLine
	Bank              *BankObservation
	Merchant          *MerchantBookRow
}

type BankObservation struct {
	AccountID             string
	BankTransactionID     string
	ValueDate             time.Time
	Description           string
	NormalizedDescription string
	UTR                   string
	UTRRaw                string
	ReferenceNumber       string
	CreditMinor           int64
	DebitMinor            int64
	CreditDebit           string
	Currency              string
	SourceRowNumber       int64
	RowHash               string
	IdentityHash          string
	Raw                   json.RawMessage
}

type ValidateRequest struct {
	Mapping    map[string]string `json:"mapping"`
	Currency   string            `json:"currency"`
	AmountUnit string            `json:"amount_unit"`
	Timezone   string            `json:"timezone"`
	Profile    string            `json:"profile"`
}

type Summary struct {
	ImportID                    string         `json:"import_id"`
	ImportType                  string         `json:"import_type"`
	Status                      string         `json:"status"`
	FileSHA256                  string         `json:"file_sha256"`
	RowsSeen                    int64          `json:"rows_seen"`
	ValidRows                   int64          `json:"valid_rows"`
	InvalidRows                 int64          `json:"invalid_rows"`
	DuplicateRows               int64          `json:"duplicate_rows"`
	InsertedCount               int64          `json:"inserted_count"`
	UpdatedCount                int64          `json:"updated_count"`
	RejectedCount               int64          `json:"rejected_count"`
	SourceHash                  string         `json:"source_hash"`
	Message                     string         `json:"message"`
	NextStep                    string         `json:"next_step,omitempty"`
	NormalizedObservationEvents int64          `json:"normalized_observation_events,omitempty"`
	Errors                      []RowErrorView `json:"errors,omitempty"`
}

type RowErrorView struct {
	RowNumber   int64  `json:"row_number"`
	Code        string `json:"code"`
	Message     string `json:"message"`
	Recoverable bool   `json:"recoverable"`
}

func (imp Import) HonestMessage() string {
	switch imp.ImportType {
	case TypeBankCSV:
		return CopyBankImported
	case TypeMerchantBooks:
		return CopyMerchantImported
	default:
		return CopySettlementImported
	}
}

func (imp Import) ToSummary(rows []RowResult) Summary {
	var errs []RowErrorView
	for _, r := range rows {
		if r.ErrorCode == "" {
			continue
		}
		errs = append(errs, RowErrorView{
			RowNumber:   r.RowNumber,
			Code:        r.ErrorCode,
			Message:     r.ErrorMessage,
			Recoverable: r.ErrorCode == ErrInvalidUTR,
		})
	}
	s := Summary{
		ImportID:      imp.ID,
		ImportType:    imp.ImportType,
		Status:        imp.Status,
		FileSHA256:    imp.FileSHA256,
		RowsSeen:      imp.RowsSeen,
		ValidRows:     imp.ValidRows,
		InvalidRows:   imp.InvalidRows,
		DuplicateRows: imp.DuplicateRows,
		InsertedCount: imp.InsertedRows,
		UpdatedCount:  imp.UpdatedRows,
		RejectedCount: imp.RejectedRows,
		SourceHash:    imp.FileSHA256,
		Message:       imp.HonestMessage(),
		Errors:        errs,
	}
	if imp.Status == StatusCommitted || imp.Status == StatusPartial {
		s.NextStep = NextStepRunRecon
		s.NormalizedObservationEvents = imp.InsertedRows
	}
	return s
}

func ImportCompletedPayload(imp Import) []byte {
	raw, _ := json.Marshal(map[string]any{
		"event_type":     models.EventTypeImportCompletedV1,
		"event_version":  models.EventVersionV1,
		"schema_version": models.SchemaVersionV1,
		"import_id":      imp.ID,
		"tenant_id":      imp.TenantID,
		"import_type":    imp.ImportType,
		"status":         imp.Status,
		"file_sha256":    imp.FileSHA256,
		"inserted_count": imp.InsertedRows,
		"message":        imp.HonestMessage(),
	})
	return raw
}
