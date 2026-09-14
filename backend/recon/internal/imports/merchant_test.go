package imports

import (
	"context"
	"testing"
)

func TestParseMerchantBooksCSV(t *testing.T) {
	csv := "invoice_id,payment_id,amount_minor,currency,due_date\nINV-1,pay_001,10000,INR,2026-09-01\n"
	out, err := ParseMerchantBooksCSV([]byte(csv), "hash")
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Rows) != 1 || out.Rows[0].Merchant == nil || out.Rows[0].Merchant.AmountMinor != 10000 {
		t.Fatalf("%+v", out.Rows)
	}
	if out.Rows[0].Merchant.PaymentID != "pay_001" {
		t.Fatal("payment_id")
	}
}

func TestParseMerchantBooksCSVTaxComponents(t *testing.T) {
	csv := "invoice_id,payment_id,amount_minor,currency,cgst_minor,sgst_minor\nINV-1,pay_001,10000,INR,900,900\n"
	out, err := ParseMerchantBooksCSV([]byte(csv), "hash")
	if err != nil {
		t.Fatal(err)
	}
	row := out.Rows[0].Merchant
	if row == nil || row.CGSTMinor != 900 || row.SGSTMinor != 900 {
		t.Fatalf("%+v", row)
	}
}

func TestMerchantImportLifecycle(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	csv := []byte("invoice_id,payment_id,amount_minor,currency\nINV-1,pay_001,5000,INR\n")
	imp, err := svc.Upload(context.Background(), UploadInput{
		TenantID: "11111111-1111-1111-1111-111111111111",
		ImportType: TypeMerchantBooks, FileName: "books.csv", Payload: csv,
	})
	if err != nil {
		t.Fatal(err)
	}
	imp, rows, err := svc.Validate(context.Background(), imp.TenantID, imp.ID, ValidateRequest{})
	if err != nil || imp.ValidRows != 1 || len(rows) != 1 {
		t.Fatalf("err=%v imp=%+v rows=%d", err, imp, len(rows))
	}
	imp, err = svc.Commit(context.Background(), imp.TenantID, imp.ID)
	if err != nil || imp.Status != StatusCommitted {
		t.Fatalf("%v %s", err, imp.Status)
	}
	if imp.HonestMessage() != CopyMerchantImported {
		t.Fatalf("%s", imp.HonestMessage())
	}
	if imp.InsertedRows != 1 || len(store.MerchantBooks) != 1 || store.MerchantBooks[0].PaymentID != "pay_001" {
		t.Fatalf("inserted=%d books=%+v", imp.InsertedRows, store.MerchantBooks)
	}
}
