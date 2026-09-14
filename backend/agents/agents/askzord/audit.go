package askzord

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

type AnswerAudit struct {
	ID           string    `json:"id"`
	TenantID     string    `json:"tenant_id"`
	Question     string    `json:"question"`
	Intent       string    `json:"intent"`
	ModelVersion string    `json:"model_version"`
	EvidenceIDs  []string  `json:"evidence_ids"`
	Answer       string    `json:"answer"`
	CreatedAt    time.Time `json:"created_at"`
}

type AuditStore interface {
	Insert(ctx context.Context, row AnswerAudit) (AnswerAudit, error)
	Get(ctx context.Context, tenantID, id string) (AnswerAudit, bool, error)
}

type MemoryAuditStore struct {
	mu   sync.Mutex
	rows []AnswerAudit
}

func NewMemoryAuditStore() *MemoryAuditStore {
	return &MemoryAuditStore{}
}

func (m *MemoryAuditStore) Insert(_ context.Context, row AnswerAudit) (AnswerAudit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if row.ID == "" {
		var b [16]byte
		_, _ = rand.Read(b[:])
		row.ID = hex.EncodeToString(b[:])
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now().UTC()
	}
	m.rows = append(m.rows, row)
	return row, nil
}

func (m *MemoryAuditStore) Get(_ context.Context, tenantID, id string) (AnswerAudit, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.rows {
		if r.ID == id && r.TenantID == tenantID {
			return r, true, nil
		}
	}
	return AnswerAudit{}, false, nil
}
