package recon

import (
	"context"
	"reflect"
	"testing"
	"time"
)

const (
	rgTenant = "11111111-1111-1111-1111-111111111111"
	rgConnA  = "22222222-2222-2222-2222-222222222222"
	rgConnB  = "33333333-3333-3333-3333-333333333333"
)

// rgStore seeds one matched payment (cash numbers non-zero) plus marketplace
// refunds. withReverse=true ingests the reverse transfer, so no signal exists.
func rgStore(t *testing.T, withReverse bool) *MemoryFinancialStore {
	t.Helper()
	ctx := context.Background()
	s := NewMemoryFinancialStore()
	s.Lines = []SettlementLine{{ID: "sl1", PaymentID: "pay_1", LineType: "payment", AmountMinor: 10000, CreditMinor: 9728, FeeMinor: 272, Currency: "INR"}}
	s.Results = []FinancialResult{{
		EntityType: EntityPayment, EntityID: "pay_1", Result: ResultMatched,
		Reason: "captured_settlement_exact_bank", ExpectedAmount: 10000, ObservedAmount: 9728, BankCreditProven: true,
	}}
	s.Exceptions = []ReconciliationException{{
		ID: "ex-persisted", EntityType: EntityPayment, EntityID: "pay_9", ReconciliationResult: ResultVariance,
		Reason: "amount_mismatch", ExpectedAmount: 500, ObservedAmount: 400, VarianceAmount: 100,
	}}
	for _, r := range []RefundFact{
		{RefundID: "rfnd_sig", PaymentID: "pay_1", AmountMinor: 1500, Currency: "INR", ProviderStatus: "processed", SellerID: "acc_seller", ObservedAt: time.Now().UTC()},
		{RefundID: "rfnd_failed", PaymentID: "pay_1", AmountMinor: 700, ProviderStatus: "failed", SellerID: "acc_seller"},
		{RefundID: "rfnd_cancelled", PaymentID: "pay_1", AmountMinor: 800, ProviderStatus: "cancelled", SellerID: "acc_seller"},
		{RefundID: "rfnd_noseller", PaymentID: "pay_1", AmountMinor: 900, ProviderStatus: "processed", SellerID: "  "},
	} {
		if _, err := s.UpsertRefund(ctx, rgTenant, rgConnA, r); err != nil {
			t.Fatal(err)
		}
	}
	edge := MarketplaceTransferEdge{TransferID: "trf_1", PaymentID: "pay_1", SellerID: "acc_seller", AmountMinor: 1500}
	if withReverse {
		edge.ReverseTransferID = "rtrf_1"
	}
	if _, err := s.UpsertTransferEdge(ctx, rgTenant, rgConnA, edge); err != nil {
		t.Fatal(err)
	}
	return s
}

func refundGraphRows(list []ReconciliationException) []ReconciliationException {
	var out []ReconciliationException
	for _, ex := range list {
		if ex.Reason == ReasonRefundWithoutReverse {
			out = append(out, ex)
		}
	}
	return out
}

func TestListExceptions_IncludesRefundWithoutReverse(t *testing.T) {
	ctx := context.Background()
	store := rgStore(t, false)
	svc := NewFinancialService(store)

	list, err := svc.ListExceptions(ctx, rgTenant, rgConnA)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("want persisted + 1 derived, got %+v", list)
	}
	if list[0].ID != "ex-persisted" {
		t.Fatalf("persisted exception must remain first: %+v", list[0])
	}
	rows := refundGraphRows(list)
	if len(rows) != 1 {
		t.Fatalf("want exactly 1 refund_without_reverse_transfer (failed/cancelled/no-seller excluded), got %+v", rows)
	}
	ex := rows[0]
	if ex.EntityType != EntityRefund || ex.EntityID != "rfnd_sig" {
		t.Fatalf("entity: %+v", ex)
	}
	if ex.ReconciliationResult != ResultUnresolved || ex.ReconciliationResult == ResultMatched {
		t.Fatalf("verdict must be existing UNRESOLVED, got %q", ex.ReconciliationResult)
	}
	if ex.TenantID != rgTenant || ex.ConnectorID != rgConnA {
		t.Fatalf("scope: %+v", ex)
	}
	if ex.VarianceAmount != 0 || ex.ObservedAmount != 0 || ex.ExpectedAmount != 1500 {
		t.Fatalf("amounts must be reference-only int64 minor with zero variance: %+v", ex)
	}
	if ex.ID != RefundGraphExceptionID(rgTenant, rgConnA, "rfnd_sig") {
		t.Fatalf("id must be deterministic: %s", ex.ID)
	}
	// Not persisted to any table.
	if len(store.Exceptions) != 1 {
		t.Fatalf("derived exception must not be persisted: %+v", store.Exceptions)
	}

	got, ok, err := svc.GetException(ctx, rgTenant, rgConnA, ex.ID)
	if err != nil || !ok || got.EntityID != "rfnd_sig" {
		t.Fatalf("GetException: ok=%v err=%v got=%+v", ok, err, got)
	}

	inv, err := svc.Investigate(ctx, rgTenant, rgConnA, ex.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if inv.FinancialImpact != 0 || inv.ExceptionID != ex.ID || inv.RootCause == ReasonRefundWithoutReverse {
		t.Fatalf("investigation: %+v", inv)
	}
}

func TestListExceptions_ReverseTransferClearsSignal(t *testing.T) {
	svc := NewFinancialService(rgStore(t, true))
	list, err := svc.ListExceptions(context.Background(), rgTenant, rgConnA)
	if err != nil {
		t.Fatal(err)
	}
	if rows := refundGraphRows(list); len(rows) != 0 {
		t.Fatalf("reverse transfer present => no exception, got %+v", rows)
	}
}

func TestListExceptions_RefundGraphConnectorScoped(t *testing.T) {
	ctx := context.Background()
	store := rgStore(t, false)
	// Connector B: a refund lacking reverse, plus a reverse edge for A's
	// payment/seller that must NOT clear A's exception.
	if _, err := store.UpsertRefund(ctx, rgTenant, rgConnB, RefundFact{
		RefundID: "rfnd_b", PaymentID: "pay_b", AmountMinor: 300, ProviderStatus: "processed", SellerID: "acc_b",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertTransferEdge(ctx, rgTenant, rgConnB, MarketplaceTransferEdge{
		TransferID: "trf_b", ReverseTransferID: "rtrf_b", PaymentID: "pay_1", SellerID: "acc_seller",
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewFinancialService(store)

	a, err := svc.RefundGraphExceptions(ctx, rgTenant, rgConnA)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 1 || a[0].EntityID != "rfnd_sig" || a[0].ConnectorID != rgConnA {
		t.Fatalf("connector A: %+v", a)
	}
	b, err := svc.RefundGraphExceptions(ctx, rgTenant, rgConnB)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 1 || b[0].EntityID != "rfnd_b" || b[0].ConnectorID != rgConnB {
		t.Fatalf("connector B: %+v", b)
	}
	if a[0].ID == RefundGraphExceptionID(rgTenant, rgConnB, "rfnd_sig") {
		t.Fatal("exception id must be connector-scoped")
	}
	if _, ok, _ := svc.GetException(ctx, rgTenant, rgConnB, a[0].ID); ok {
		t.Fatal("connector B must not resolve connector A's derived exception")
	}
	if got, _ := svc.RefundGraphExceptions(ctx, "", rgConnA); len(got) != 0 {
		t.Fatalf("empty tenant must not derive: %+v", got)
	}
}

func TestRefundGraphException_CashUnchanged(t *testing.T) {
	ctx := context.Background()
	with := NewFinancialService(rgStore(t, false))   // signal present
	without := NewFinancialService(rgStore(t, true)) // same refunds, reverse ingested

	if l, _ := with.ListExceptions(ctx, rgTenant, rgConnA); len(refundGraphRows(l)) != 1 {
		t.Fatal("precondition: signal must be present")
	}

	cw, err := with.CashPosition(ctx, rgTenant, rgConnA)
	if err != nil {
		t.Fatal(err)
	}
	co, err := without.CashPosition(ctx, rgTenant, rgConnA)
	if err != nil {
		t.Fatal(err)
	}
	cw.AsOf, co.AsOf = time.Time{}, time.Time{}
	if !reflect.DeepEqual(cw, co) {
		t.Fatalf("cash position changed by signal:\nwith=%+v\nwithout=%+v", cw, co)
	}
	// Also: cash computed over the full exceptions list equals persisted-only.
	all, _ := with.ListExceptions(ctx, rgTenant, rgConnA)
	full := CashPosition(nil, nil, all)
	if full.UnresolvedExposureMinor != 100 {
		t.Fatalf("derived exception must add zero exposure, got %d", full.UnresolvedExposureMinor)
	}

	sw, _ := with.FinanceSummary(ctx, rgTenant, rgConnA)
	so, _ := without.FinanceSummary(ctx, rgTenant, rgConnA)
	if sw.ExposureMinor != so.ExposureMinor || sw.MatchedCount != so.MatchedCount || !reflect.DeepEqual(sw.ResultCounts, so.ResultCounts) {
		t.Fatalf("finance summary changed:\nwith=%+v\nwithout=%+v", sw, so)
	}

	schW, err := with.CashSchedule(ctx, rgTenant, rgConnA, 14)
	if err != nil {
		t.Fatal(err)
	}
	schO, err := without.CashSchedule(ctx, rgTenant, rgConnA, 14)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stripScheduleClock(schW), stripScheduleClock(schO)) {
		t.Fatalf("expected-cash schedule changed by signal")
	}
}

func stripScheduleClock(s CashSchedule) CashSchedule {
	s.AsOf = time.Time{}
	return s
}

func TestRefundWithoutReverse_CancelledStatusExcluded(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryFinancialStore()
	for _, r := range []RefundFact{
		{RefundID: "rfnd_failed", PaymentID: "pay_1", AmountMinor: 700, ProviderStatus: "failed", SellerID: "acc_seller"},
		{RefundID: "rfnd_cancelled", PaymentID: "pay_1", AmountMinor: 800, ProviderStatus: "cancelled", SellerID: "acc_seller"},
		{RefundID: "rfnd_canceled", PaymentID: "pay_1", AmountMinor: 850, ProviderStatus: "Canceled", SellerID: "acc_seller"},
	} {
		if _, err := store.UpsertRefund(ctx, rgTenant, rgConnA, r); err != nil {
			t.Fatal(err)
		}
	}
	// No reverse edges at all.
	if sig := DetectRefundsWithoutReverse(mustRefunds(t, store), nil); len(sig) != 0 {
		t.Fatalf("failed/cancelled must not signal: %+v", sig)
	}
	svc := NewFinancialService(store)
	list, err := svc.ListExceptions(ctx, rgTenant, rgConnA)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("failed/cancelled must not produce an exception: %+v", list)
	}
	res, err := svc.MarketplaceRefundGraphExceptions(ctx, rgTenant, rgConnA)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Signals) != 0 {
		t.Fatalf("legacy endpoint source must agree: %+v", res.Signals)
	}
}

func mustRefunds(t *testing.T, s *MemoryFinancialStore) []RefundFact {
	t.Helper()
	r, err := s.ListRefunds(context.Background(), rgTenant, rgConnA, "")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRefundWithoutReverse_NeverMatched(t *testing.T) {
	ctx := context.Background()
	store := rgStore(t, false)
	before := append([]FinancialResult{}, store.Results...)
	svc := NewFinancialService(store)

	list, err := svc.ListExceptions(ctx, rgTenant, rgConnA)
	if err != nil {
		t.Fatal(err)
	}
	rows := refundGraphRows(list)
	if len(rows) != 1 {
		t.Fatalf("precondition: %+v", rows)
	}
	for _, ex := range rows {
		if ex.ReconciliationResult == ResultMatched {
			t.Fatalf("refund_without_reverse_transfer must never be MATCHED: %+v", ex)
		}
		switch ex.ReconciliationResult {
		case ResultAmbiguous, ResultUnresolved, ResultConflicted, ResultVariance, ResultOrphan:
		default:
			t.Fatalf("verdict must be an existing non-MATCHED status, got %q", ex.ReconciliationResult)
		}
	}
	if _, err := svc.Investigate(ctx, rgTenant, rgConnA, rows[0].ID, ""); err != nil {
		t.Fatal(err)
	}
	// Listing + investigating never writes results or persisted exceptions.
	if !reflect.DeepEqual(before, store.Results) {
		t.Fatalf("result verdicts changed:\nbefore=%+v\nafter=%+v", before, store.Results)
	}
	results, _ := store.ListReconciliationResults(ctx, rgTenant, rgConnA)
	for _, r := range results {
		if r.EntityType == EntityRefund || r.EntityID == "rfnd_sig" {
			t.Fatalf("signal must not create a verdict row: %+v", r)
		}
	}
	if len(store.Exceptions) != 1 {
		t.Fatalf("no new persisted exceptions: %+v", store.Exceptions)
	}
}

func TestRefundWithoutReverse_ThroughExceptionsPath(t *testing.T) {
	ctx := context.Background()
	svc := NewFinancialService(rgStore(t, false))

	// 1) Existing exceptions listing.
	list, err := svc.ListExceptions(ctx, rgTenant, rgConnA)
	if err != nil {
		t.Fatal(err)
	}
	rows := refundGraphRows(list)
	if len(rows) != 1 {
		t.Fatalf("want signal in exceptions listing, got %+v", list)
	}
	ex := rows[0]

	// 2) exceptions.go NeedsInvestigation accepts it via its existing verdict.
	if !NeedsInvestigation(FinancialResult{EntityType: ex.EntityType, EntityID: ex.EntityID, Result: ex.ReconciliationResult, Exception: &ex}) {
		t.Fatalf("NeedsInvestigation must be true for %+v", ex)
	}

	// 3) exceptions.go DeterministicInvestigation has a dedicated reason branch.
	rec := DeterministicInvestigation(ex)
	if rec.RootCause == ex.Reason || rec.RootCause == "" {
		t.Fatalf("expected dedicated root cause, got %q", rec.RootCause)
	}
	if rec.FinancialImpact != 0 {
		t.Fatalf("financial impact must be 0 (not cash), got %d", rec.FinancialImpact)
	}

	// 4) Investigate (service path used by close + agents) resolves derived id
	//    and by entity id.
	byID, err := svc.Investigate(ctx, rgTenant, rgConnA, ex.ID, "")
	if err != nil || byID.RootCause != rec.RootCause || byID.TenantID != rgTenant || byID.ConnectorID != rgConnA {
		t.Fatalf("Investigate by id: err=%v rec=%+v", err, byID)
	}
	if _, err := svc.Investigate(ctx, rgTenant, rgConnA, "", "rfnd_sig"); err != nil {
		t.Fatalf("Investigate by entity: %v", err)
	}

	// 5) Separate endpoint still reports the same refund from the same source.
	res, err := svc.MarketplaceRefundGraphExceptions(ctx, rgTenant, rgConnA)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Signals) != 1 || res.Signals[0].RefundID != ex.EntityID || !res.NotCash {
		t.Fatalf("legacy endpoint: %+v", res)
	}
}
