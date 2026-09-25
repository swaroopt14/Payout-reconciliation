package persistence

import (
	"strings"
	"testing"
)

func normSQL(s string) string { return strings.Join(strings.Fields(s), " ") }

// SQL-string assertion (no DB harness in unit tests): ON CONFLICT refund_id
// keeps the existing non-empty value unless $13 (reversal customer_refund_id).
func TestTransferEdgeUpsert_ReupsertKeepsExistingRefundID(t *testing.T) {
	q := normSQL(upsertTransferEdgeSQL)
	want := normSQL(`refund_id=CASE
		WHEN $13::boolean AND NULLIF(EXCLUDED.refund_id,'') IS NOT NULL THEN EXCLUDED.refund_id
		ELSE COALESCE(NULLIF(marketplace_transfer_edges.refund_id,''), NULLIF(EXCLUDED.refund_id,''))
	END`)
	if !strings.Contains(q, "ON CONFLICT (tenant_id, connector_id, transfer_id) DO UPDATE SET") {
		t.Fatalf("missing tenant+connector scoped ON CONFLICT: %s", q)
	}
	if !strings.Contains(q, want) {
		t.Fatalf("refund_id must coalesce toward existing value:\n%s", q)
	}
	if strings.Contains(q, "refund_id=COALESCE(NULLIF(EXCLUDED.refund_id,''), marketplace_transfer_edges.refund_id)") {
		t.Fatal("old overwrite-on-conflict refund_id clause must be gone")
	}
}

func TestTransferEdgeUpsert_CustomerRefundIDWins(t *testing.T) {
	q := normSQL(upsertTransferEdgeSQL)
	if !strings.Contains(q, "WHEN $13::boolean AND NULLIF(EXCLUDED.refund_id,'') IS NOT NULL THEN EXCLUDED.refund_id") {
		t.Fatalf("reversal customer_refund_id must take precedence:\n%s", q)
	}
}
