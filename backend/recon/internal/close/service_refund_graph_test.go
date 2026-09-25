package close

import (
	"context"
	"reflect"
	"testing"

	"zord-outcome-engine/internal/recon"
)

func closeStore(t *testing.T, withReverse bool) *recon.MemoryFinancialStore {
	t.Helper()
	ctx := context.Background()
	s := recon.NewMemoryFinancialStore()
	s.Payments = []recon.PaymentFact{{
		PaymentID: "pay_1", CanonicalStatus: recon.PaymentCaptured, ProviderStatus: "captured",
		Captured: true, AmountMinor: 10000, Currency: "INR",
	}}
	s.Lines = []recon.SettlementLine{{
		ID: "sl1", PaymentID: "pay_1", LineType: "payment", AmountMinor: 10000, CreditMinor: 9728, FeeMinor: 272, Currency: "INR",
	}}
	for _, r := range []recon.RefundFact{
		{RefundID: "rfnd_sig", PaymentID: "pay_1", AmountMinor: 1500, ProviderStatus: "processed", SellerID: "acc_seller"},
		{RefundID: "rfnd_failed", PaymentID: "pay_1", AmountMinor: 700, ProviderStatus: "failed", SellerID: "acc_seller"},
	} {
		if _, err := s.UpsertRefund(ctx, "t", "c", r); err != nil {
			t.Fatal(err)
		}
	}
	e := recon.MarketplaceTransferEdge{TransferID: "trf_1", PaymentID: "pay_1", SellerID: "acc_seller", AmountMinor: 1500}
	if withReverse {
		e.ReverseTransferID = "rtrf_1"
	}
	if _, err := s.UpsertTransferEdge(ctx, "t", "c", e); err != nil {
		t.Fatal(err)
	}
	return s
}

func runClose(t *testing.T, withReverse bool) Report {
	t.Helper()
	s := closeStore(t, withReverse)
	svc := NewService(recon.NewFinancialService(s), nil, s)
	rep, err := svc.Run(context.Background(), RunRequest{TenantID: "t", ConnectorID: "c"})
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestCloseReport_RefundWithoutReverseInExceptionList_CashUnchanged(t *testing.T) {
	with := runClose(t, false)
	without := runClose(t, true)

	var found []ExceptionItem
	for _, ex := range with.ExceptionList {
		if ex.Reason == recon.ReasonRefundWithoutReverse {
			found = append(found, ex)
		}
	}
	if len(found) != 1 || found[0].EntityID != "rfnd_sig" || found[0].Result != recon.ResultUnresolved || found[0].Variance != 0 {
		t.Fatalf("close exception list: %+v", with.ExceptionList)
	}
	for _, ex := range without.ExceptionList {
		if ex.Reason == recon.ReasonRefundWithoutReverse {
			t.Fatalf("reverse present must not list: %+v", ex)
		}
	}
	if with.Exceptions != without.Exceptions+1 {
		t.Fatalf("exception count: with=%d without=%d", with.Exceptions, without.Exceptions)
	}
	if !reflect.DeepEqual(with.CashPosition, without.CashPosition) {
		t.Fatalf("cash position changed:\nwith=%v\nwithout=%v", with.CashPosition, without.CashPosition)
	}
	if with.UnresolvedExposureMinor != without.UnresolvedExposureMinor || with.Matched != without.Matched || with.Records != without.Records {
		t.Fatalf("exposure/verdicts changed: with=%+v without=%+v", with, without)
	}
}
