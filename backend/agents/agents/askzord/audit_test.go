package askzord

import (
	"context"
	"testing"
)

func TestMemoryAuditStoreInsertGet(t *testing.T) {
	store := NewMemoryAuditStore()
	row, err := store.Insert(context.Background(), AnswerAudit{
		TenantID: "t1", Question: "why unmatched?", Intent: IntentKnowledge,
		ModelVersion: "askzord.BuildAnswer", EvidenceIDs: []string{"pay_1", "bank_1"},
		Answer: "UNRESOLVED until bank credit is proven.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if row.ID == "" {
		t.Fatal("expected audit id")
	}
	got, ok, err := store.Get(context.Background(), "t1", row.ID)
	if err != nil || !ok {
		t.Fatalf("get ok=%v err=%v", ok, err)
	}
	if got.Question != row.Question || len(got.EvidenceIDs) != 2 {
		t.Fatalf("got=%+v", got)
	}
	if _, ok, _ := store.Get(context.Background(), "other", row.ID); ok {
		t.Fatal("tenant isolation")
	}
}
