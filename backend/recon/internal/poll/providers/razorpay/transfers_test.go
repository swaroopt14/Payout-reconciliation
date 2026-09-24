package razorpay

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSellerIDFromTransferPrefersRecipient(t *testing.T) {
	got := SellerIDFromTransfer(TransferResponse{Recipient: " acc_rec ", Account: "acc_acct"})
	if got != "acc_rec" {
		t.Fatalf("got %q", got)
	}
}

func TestSellerIDFromTransferFallsBackToAccount(t *testing.T) {
	got := SellerIDFromTransfer(TransferResponse{Account: "  acc_only  "})
	if got != "acc_only" {
		t.Fatalf("got %q", got)
	}
}

func TestSellerIDFromTransferEmptyWhenAbsent(t *testing.T) {
	if got := SellerIDFromTransfer(TransferResponse{}); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if got := SellerIDFromTransfers(nil); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if got := SellerIDFromTransfers([]TransferResponse{{}, {Account: " "}}); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestSellerIDFromTransfersFirstNonEmpty(t *testing.T) {
	got := SellerIDFromTransfers([]TransferResponse{
		{},
		{Recipient: "acc_first"},
		{Recipient: "acc_second"},
	})
	if got != "acc_first" {
		t.Fatalf("got %q", got)
	}
}

func TestListTransfersForPayment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/payments/pay_1/transfers" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		w.WriteHeader(200)
		fmt.Fprintf(w, `{"entity":"collection","count":1,"items":[{"id":"trf_1","entity":"transfer","source":"pay_1","recipient":"acc_seller","amount":1000,"currency":"INR","status":"processed"}]}`)
	}))
	defer server.Close()
	cfg := testConfig()
	cfg.BaseURL = server.URL
	client, err := NewClient(cfg, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	items, err := client.ListTransfersForPayment(ctx, "pay_1")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Recipient != "acc_seller" {
		t.Fatalf("%+v", items)
	}
	if sid := SellerIDFromTransfers(items); sid != "acc_seller" {
		t.Fatalf("seller_id=%q", sid)
	}
}

func TestListTransfersForPaymentEmptyPaymentID(t *testing.T) {
	cfg := testConfig()
	client, err := NewClient(cfg, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	items, err := client.ListTransfersForPayment(context.Background(), "  ")
	if err != nil || items != nil {
		t.Fatalf("items=%v err=%v", items, err)
	}
}

func TestListTransfersForPaymentAccountField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		fmt.Fprintf(w, `{"entity":"collection","count":1,"items":[{"id":"trf_2","account":"acc_from_create","amount":500,"currency":"INR"}]}`)
	}))
	defer server.Close()
	cfg := testConfig()
	cfg.BaseURL = server.URL
	client, err := NewClient(cfg, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	items, err := client.ListTransfersForPayment(context.Background(), "pay_2")
	if err != nil {
		t.Fatal(err)
	}
	if got := SellerIDFromTransfers(items); got != "acc_from_create" {
		t.Fatalf("got %q", got)
	}
}
