package imports

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"zord-outcome-engine/models"

	"github.com/google/uuid"
)

type Service struct {
	Store Store
}

func NewService(store Store) *Service {
	return &Service{Store: store}
}

type UploadInput struct {
	TenantID     string
	ConnectorID  string
	AccountID    string
	ImportType   string
	FileName     string
	ContentType  string
	ProviderMode string
	Payload      []byte
}

func (s *Service) Upload(ctx context.Context, in UploadInput) (Import, error) {
	if in.ImportType == "" {
		in.ImportType = TypeBankCSV
	}
	if in.ProviderMode == "" {
		in.ProviderMode = "test"
	}
	hash := HashBytes(in.Payload)
	if existing, err := s.Store.GetByHash(ctx, in.TenantID, in.ImportType, hash); err == nil && existing.ID != "" {
		out := existing
		out.Status = StatusDuplicate
		out.InsertedRows = 0
		out.UpdatedRows = 0
		return out, nil
	}
	imp := Import{
		ID:            uuid.Must(uuid.NewV7()).String(),
		TenantID:      in.TenantID,
		ConnectorID:   in.ConnectorID,
		AccountID:     in.AccountID,
		ImportType:    in.ImportType,
		SourceType:    in.ImportType,
		ProviderMode:  in.ProviderMode,
		FileName:      in.FileName,
		ContentType:   in.ContentType,
		FileSizeBytes: int64(len(in.Payload)),
		FileSHA256:    hash,
		Payload:       in.Payload,
		Status:        StatusUploaded,
		CreatedAt:     time.Now().UTC(),
	}
	return s.Store.Create(ctx, imp)
}

func (s *Service) Get(ctx context.Context, tenantID, id string) (Import, error) {
	return s.Store.Get(ctx, tenantID, id)
}

func (s *Service) ListRows(ctx context.Context, tenantID, id string) ([]RowResult, error) {
	if _, err := s.Store.Get(ctx, tenantID, id); err != nil {
		return nil, err
	}
	return s.Store.ListRows(ctx, id)
}

func (s *Service) Cancel(ctx context.Context, tenantID, id string) (Import, error) {
	imp, err := s.Store.Get(ctx, tenantID, id)
	if err != nil {
		return Import{}, err
	}
	if imp.Status == StatusCommitted {
		return imp, nil
	}
	imp.Status = StatusCancelled
	return imp, s.Store.Save(ctx, imp)
}

func (s *Service) Validate(ctx context.Context, tenantID, id string, req ValidateRequest) (Import, []RowResult, error) {
	imp, err := s.Store.Get(ctx, tenantID, id)
	if err != nil {
		return Import{}, nil, err
	}
	imp.Status = StatusValidating
	imp.SelectedMapping = req.Mapping
	imp.Currency = strings.ToUpper(strings.TrimSpace(req.Currency))
	imp.AmountUnit = req.AmountUnit
	imp.Timezone = req.Timezone
	imp.Profile = req.Profile
	rows, detected, ferr := s.parse(imp)
	if ferr != nil {
		imp.Status = StatusValidationFailed
		_ = s.Store.Save(ctx, imp)
		return imp, nil, ferr
	}
	imp.DetectedColumns = detected
	tally(&imp, rows)
	imp.ValidatedAt = time.Now().UTC()
	if imp.InvalidRows > 0 && imp.ValidRows > 0 {
		imp.Status = StatusPartial
	} else if imp.InvalidRows > 0 && imp.ValidRows == 0 {
		imp.Status = StatusValidationFailed
	} else {
		imp.Status = StatusValidated
	}
	if err := s.Store.Save(ctx, imp); err != nil {
		return imp, rows, err
	}
	if err := s.Store.ReplaceRows(ctx, imp.ID, rows); err != nil {
		return imp, rows, err
	}
	return imp, rows, nil
}

func (s *Service) Commit(ctx context.Context, tenantID, id string) (Import, error) {
	imp, err := s.Store.Get(ctx, tenantID, id)
	if err != nil {
		return Import{}, err
	}
	if imp.Status != StatusValidated && imp.Status != StatusPartial {
		if imp.Status == StatusCommitted {
			return imp, nil
		}
		return Import{}, &FatalError{Code: StatusCommitFailed, Message: "import is not validated"}
	}
	rows, _, err := s.parse(imp)
	if err != nil {
		return Import{}, err
	}
	tally(&imp, rows)
	imp.Status = StatusCommitting
	events := buildOutbox(imp, rows)
	committed, err := s.Store.Commit(ctx, imp, rows, events)
	if err != nil {
		imp.Status = StatusCommitFailed
		_ = s.Store.Save(ctx, imp)
		return imp, err
	}
	return committed, nil
}

// UploadValidateCommit is the one-shot wrapper for POST /v1/bank-statements/upload.
func (s *Service) UploadValidateCommit(ctx context.Context, in UploadInput, req ValidateRequest) (Import, error) {
	imp, err := s.Upload(ctx, in)
	if err != nil {
		return Import{}, err
	}
	if imp.Status == StatusDuplicate || imp.Status == StatusCommitted {
		return imp, nil
	}
	imp, _, err = s.Validate(ctx, in.TenantID, imp.ID, req)
	if err != nil {
		return imp, err
	}
	if imp.Status == StatusValidationFailed {
		return imp, nil
	}
	return s.Commit(ctx, in.TenantID, imp.ID)
}

func (s *Service) parse(imp Import) ([]RowResult, []string, error) {
	switch imp.ImportType {
	case TypeSettlementJSON:
		out, err := ParseSettlementJSON(imp.Payload, imp.FileSHA256)
		if err != nil {
			return nil, nil, err
		}
		return out.Rows, nil, nil
	case TypeSettlementCSV:
		out, err := ParseSettlementCSV(imp.Payload, imp.FileSHA256)
		if err != nil {
			return nil, nil, err
		}
		return out.Rows, nil, nil
	case TypeMerchantBooks:
		out, err := ParseMerchantBooksCSV(imp.Payload, imp.FileSHA256)
		if err != nil {
			return nil, nil, err
		}
		return out.Rows, nil, nil
	default:
		rows, detected, err := ParseBankCSV(imp.Payload, BankOptions{
			AccountID: imp.AccountID, Mapping: imp.SelectedMapping, Currency: imp.Currency,
			AmountUnit: imp.AmountUnit, Timezone: imp.Timezone, Profile: imp.Profile,
		})
		return rows, detected, err
	}
}

func tally(imp *Import, rows []RowResult) {
	imp.RowsSeen = int64(len(rows))
	imp.ValidRows = 0
	imp.InvalidRows = 0
	imp.DuplicateRows = 0
	for _, r := range rows {
		switch r.Status {
		case RowValid, RowAcceptedWithoutValidUTR:
			imp.ValidRows++
		case RowDuplicate:
			imp.DuplicateRows++
			imp.InvalidRows++
		default:
			imp.InvalidRows++
		}
	}
}

func buildOutbox(imp Import, rows []RowResult) []models.OutboxRow {
	tid, err := uuid.Parse(imp.TenantID)
	if err != nil {
		tid = uuid.Nil
	}
	var events []models.OutboxRow
	now := time.Now().UTC()
	for _, r := range rows {
		if r.Status != RowValid && r.Status != RowAcceptedWithoutValidUTR {
			continue
		}
		if r.Settlement != nil {
			payload, _ := json.Marshal(map[string]any{
				"event_type":     models.EventTypeSettlementObservationNormalizedV1,
				"event_version":  models.EventVersionV1,
				"schema_version": models.SchemaVersionV1,
				"tenant_id":      imp.TenantID,
				"observation_id": r.CanonicalRecordID,
				"provider":       "razorpay",
				"entity_id":      r.Settlement.EntityID,
				"payment_id":     r.Settlement.PaymentID,
				"settlement_id":  r.Settlement.SettlementID,
				"settlement_utr": r.Settlement.UTR,
				"line_type":      r.Settlement.LineType,
				"credit_amount":  r.Settlement.CreditMinor,
				"debit_amount":   r.Settlement.DebitMinor,
				"fee_amount":     r.Settlement.FeeMinor,
				"tax_amount":     r.Settlement.TaxMinor,
				"currency":       r.Settlement.Currency,
				"source_hash":    imp.FileSHA256,
				"import_id":      imp.ID,
			})
			events = append(events, models.OutboxRow{
				EventID: uuid.Must(uuid.NewV7()), TenantID: tid,
				AggregateType: "provider_settlement_line_observation", AggregateID: uuid.Must(uuid.NewV7()),
				EventType: models.EventTypeSettlementObservationNormalizedV1, Payload: payload, CreatedAt: now,
			})
		}
		if r.Merchant != nil {
			payload, _ := json.Marshal(map[string]any{
				"event_type":    "merchant.books.normalized.v1",
				"event_version": models.EventVersionV1,
				"schema_version": models.SchemaVersionV1,
				"tenant_id":     imp.TenantID,
				"invoice_id":    r.Merchant.InvoiceID,
				"payment_id":    r.Merchant.PaymentID,
				"payout_id":     r.Merchant.PayoutID,
				"amount_minor":  r.Merchant.AmountMinor,
				"currency":      r.Merchant.Currency,
				"source_hash":   imp.FileSHA256,
				"import_id":     imp.ID,
			})
			events = append(events, models.OutboxRow{
				EventID: uuid.Must(uuid.NewV7()), TenantID: tid,
				AggregateType: "merchant_book_fact", AggregateID: uuid.Must(uuid.NewV7()),
				EventType: "merchant.books.normalized.v1", Payload: payload, CreatedAt: now,
			})
		}
		if r.Bank != nil {
			payload, _ := json.Marshal(map[string]any{
				"event_type":        models.EventTypeBankObservationNormalizedV1,
				"event_version":     models.EventVersionV1,
				"schema_version":    models.SchemaVersionV1,
				"tenant_id":         imp.TenantID,
				"account_id":        r.Bank.AccountID,
				"utr":               r.Bank.UTR,
				"credit_amount":     r.Bank.CreditMinor,
				"debit_amount":      r.Bank.DebitMinor,
				"currency":          r.Bank.Currency,
				"row_hash":          r.Bank.RowHash,
				"import_id":         imp.ID,
				"source_row_number": r.Bank.SourceRowNumber,
			})
			events = append(events, models.OutboxRow{
				EventID: uuid.Must(uuid.NewV7()), TenantID: tid,
				AggregateType: "bank_transaction_observation", AggregateID: uuid.Must(uuid.NewV7()),
				EventType: models.EventTypeBankObservationNormalizedV1, Payload: payload, CreatedAt: now,
			})
		}
	}
	events = append(events, models.OutboxRow{
		EventID: uuid.Must(uuid.NewV7()), TenantID: tid,
		AggregateType: "data_import", AggregateID: uuid.Must(uuid.NewV7()),
		EventType: models.EventTypeImportCompletedV1, Payload: ImportCompletedPayload(imp), CreatedAt: now,
	})
	return events
}
