package persistence

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestApprove_SetsApprovedByAt(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewPaymentIntentRepo(db)

	const tenant, intent, approver = "11111111-1111-1111-1111-111111111111", "intent-1", "approver-user-7"
	mock.ExpectExec(`UPDATE payment_intents\s+SET governance_state = 'VALID'.*approved_by = \$3, approved_at = now\(\).*WHERE tenant_id = \$1 AND intent_id = \$2`).
		WithArgs(tenant, intent, approver).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE outbox`).
		WithArgs(tenant, intent).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := repo.ApproveHeldIntent(context.Background(), tenant, intent, approver); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}

	// No approver identity -> no write at all.
	if err := repo.ApproveHeldIntent(context.Background(), tenant, intent, "  "); err == nil {
		t.Fatal("approval without approved_by must fail")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}

	// The columns are added by an additive, nullable migration.
	raw, err := os.ReadFile("../../db/migrations/20260925230000_add_payment_intents_approved_by_at.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := strings.SplitN(string(raw), "-- +goose Down", 2)[0]
	for _, col := range []string{`approved_by TEXT NULL`, `approved_at TIMESTAMPTZ NULL`} {
		if !regexp.MustCompile(`ADD COLUMN IF NOT EXISTS ` + regexp.QuoteMeta(col)).MatchString(up) {
			t.Fatalf("migration missing additive nullable column %q", col)
		}
	}
	if strings.Contains(strings.ToUpper(up), "NOT NULL") || strings.Contains(strings.ToUpper(up), "DROP ") {
		t.Fatal("approved_by/approved_at migration must be additive and nullable")
	}
}
