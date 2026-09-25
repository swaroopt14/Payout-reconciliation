package imports

import (
	"context"

	"zord-outcome-engine/internal/poll/providers/razorpay"
	"zord-outcome-engine/models"
)

type Store interface {
	Create(ctx context.Context, imp Import) (Import, error)
	Get(ctx context.Context, tenantID, id string) (Import, error)
	GetByHash(ctx context.Context, tenantID, importType, hash string) (Import, error)
	Save(ctx context.Context, imp Import) error
	ReplaceRows(ctx context.Context, importID string, rows []RowResult) error
	ListRows(ctx context.Context, importID string) ([]RowResult, error)
	// Commit persists observations + outbox. Must not write proof/match tables.
	Commit(ctx context.Context, imp Import, rows []RowResult, events []models.OutboxRow) (Import, error)
}

type MemoryStore struct {
	Imports        map[string]Import
	Rows           map[string][]RowResult
	Settlements    []razorpay.NeutralSettlementLine
	Banks          []BankObservation
	MerchantBooks  []MerchantBookRow
	Outbox         []models.OutboxRow
	ProofSubjects  int
	PaymentAmounts map[string]int64
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{Imports: map[string]Import{}, Rows: map[string][]RowResult{}, PaymentAmounts: map[string]int64{}}
}

func (m *MemoryStore) Create(_ context.Context, imp Import) (Import, error) {
	if existing, err := m.GetByHash(context.Background(), imp.TenantID, imp.ImportType, imp.FileSHA256); err == nil && existing.ID != "" {
		return Import{}, &FatalError{Code: ErrDuplicateFile, Message: MessageFor(ErrDuplicateFile)}
	}
	m.Imports[imp.TenantID+"|"+imp.ID] = imp
	return imp, nil
}

func (m *MemoryStore) Get(_ context.Context, tenantID, id string) (Import, error) {
	imp, ok := m.Imports[tenantID+"|"+id]
	if !ok {
		return Import{}, ErrNotFound
	}
	return imp, nil
}

func (m *MemoryStore) GetByHash(_ context.Context, tenantID, importType, hash string) (Import, error) {
	for _, imp := range m.Imports {
		if imp.TenantID == tenantID && imp.ImportType == importType && imp.FileSHA256 == hash {
			return imp, nil
		}
	}
	return Import{}, ErrNotFound
}

func (m *MemoryStore) Save(_ context.Context, imp Import) error {
	m.Imports[imp.TenantID+"|"+imp.ID] = imp
	return nil
}

func (m *MemoryStore) ReplaceRows(_ context.Context, importID string, rows []RowResult) error {
	m.Rows[importID] = rows
	return nil
}

func (m *MemoryStore) ListRows(_ context.Context, importID string) ([]RowResult, error) {
	return append([]RowResult{}, m.Rows[importID]...), nil
}

func (m *MemoryStore) Commit(_ context.Context, imp Import, rows []RowResult, events []models.OutboxRow) (Import, error) {
	var inserted int64
	seen := map[string]struct{}{}
	for i := range rows {
		if rows[i].Status != RowValid && rows[i].Status != RowAcceptedWithoutValidUTR {
			continue
		}
		key := rows[i].RowHash
		if _, ok := seen[key]; ok {
			rows[i].Status = RowDuplicate
			continue
		}
		seen[key] = struct{}{}
		if rows[i].Settlement != nil {
			line := *rows[i].Settlement
			if line.SourceFile == "" {
				line.SourceFile = imp.FileName
			}
			if line.PaymentID != "" {
				amt, found := m.PaymentAmounts[line.PaymentID]
				line.PaymentLink = razorpay.PaymentLinkFor(line.PaymentID, line.AmountMinor, amt, found)
			}
			m.Settlements = append(m.Settlements, line)
		}
		if rows[i].Bank != nil {
			b := *rows[i].Bank
			if b.CreditDebit == "" {
				b.CreditDebit = bankSide(b.CreditMinor, b.DebitMinor)
			}
			m.Banks = append(m.Banks, b)
		}
		if rows[i].Merchant != nil {
			if m.mergeMerchant(*rows[i].Merchant) {
				rows[i].Status = RowDuplicate
				imp.DuplicateRows++
				continue
			}
			m.MerchantBooks = append(m.MerchantBooks, *rows[i].Merchant)
		}
		rows[i].Status = RowInserted
		inserted++
	}
	imp.InsertedRows = inserted
	imp.RejectedRows = imp.InvalidRows
	imp.Status = StatusCommitted
	if imp.InvalidRows > 0 {
		imp.Status = StatusPartial
	}
	m.Imports[imp.TenantID+"|"+imp.ID] = imp
	m.Rows[imp.ID] = rows
	m.Outbox = append(m.Outbox, events...)
	return imp, nil
}

func bankSide(credit, debit int64) string {
	if credit > 0 && debit == 0 {
		return "CREDIT"
	}
	if debit > 0 && credit == 0 {
		return "DEBIT"
	}
	if credit > 0 {
		return "CREDIT"
	}
	if debit > 0 {
		return "DEBIT"
	}
	return ""
}

// mergeMerchant applies B5 identity semantics: an already-stored fact with the
// same identity (new or legacy hash, or same ids) is never duplicated and its
// original amount_minor is never overwritten; a different amount is recorded
// in ReuploadAmountMinor for recon to surface. Returns true when merged.
func (m *MemoryStore) mergeMerchant(row MerchantBookRow) bool {
	for j := range m.MerchantBooks {
		cur := &m.MerchantBooks[j]
		if !cur.SameIdentity(row) && !row.SameIdentity(*cur) {
			continue
		}
		if row.AmountMinor != cur.AmountMinor {
			amt := row.AmountMinor
			cur.ReuploadAmountMinor = &amt
		} else {
			cur.ReuploadAmountMinor = nil
		}
		return true
	}
	return false
}
