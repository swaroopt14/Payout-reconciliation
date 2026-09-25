package observe

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"zord-outcome-engine/internal/poll"
	"zord-outcome-engine/internal/poll/providers/razorpay"
	"zord-outcome-engine/internal/recon"
	"zord-outcome-engine/models"
)

func capturedEnvelope(t *testing.T) Envelope {
	t.Helper()
	created := time.Unix(1725000000, 0).UTC()
	return Envelope{
		EventName:          EventObservationReceived,
		SchemaVersion:      "v1",
		TenantID:           "11111111-1111-1111-1111-111111111111",
		ConnectorID:        "22222222-2222-2222-2222-222222222222",
		Provider:           "razorpay",
		ProviderMode:       "test",
		ProviderEventID:    "evt_1",
		ProviderEventType:  "payment.captured",
		ProviderEntityType: "payment",
		ProviderEntityID:   "pay_test_123",
		ReceiptID:          "33333333-3333-3333-3333-333333333333",
		RawBodyHash:        "sha256:abc",
		Amount:             50000,
		Currency:           "INR",
		Status:             "captured",
		OrderID:            "order_1",
		Captured:           true,
		ProviderCreatedAt:  &created,
		TraceID:            "trace-1",
	}
}

func TestNormalizePaymentCaptured(t *testing.T) {
	item, ok, err := NormalizePayment(capturedEnvelope(t))
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if item.PaymentID != "pay_test_123" || item.AmountMinor != 50000 || !item.Captured || item.Status != "captured" {
		t.Fatalf("item=%+v", item)
	}
	if item.PayloadHash == "" {
		t.Fatal("missing hash")
	}
}

func TestNormalizeSkipsRefund(t *testing.T) {
	env := capturedEnvelope(t)
	env.ProviderEventType = "refund.created"
	env.ProviderEntityType = "refund"
	env.ProviderEntityID = "rfnd_1"
	_, ok, err := NormalizePayment(env)
	if err != nil || ok {
		t.Fatalf("refund should skip ok=%v err=%v", ok, err)
	}
}

func TestApplyInsertsObservationAndOutbox(t *testing.T) {
	store := poll.NewMemoryStore()
	p := NewProcessor(store)
	res, err := p.Apply(context.Background(), capturedEnvelope(t))
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != ResultInserted {
		t.Fatalf("kind=%s", res.Kind)
	}
	if len(store.Payments) != 1 {
		t.Fatalf("payments=%d", len(store.Payments))
	}
	if len(store.Outbox) < 1 {
		t.Fatalf("outbox=%d", len(store.Outbox))
	}
	foundObs := false
	for _, row := range store.Outbox {
		if row.EventType == models.EventTypePaymentObservationNormalizedV1 {
			foundObs = true
			var payload map[string]any
			if err := json.Unmarshal(row.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if payload["source"] != SourceWebhook {
				t.Fatalf("source=%v", payload["source"])
			}
			if payload["status"] != "captured" {
				t.Fatalf("status=%v", payload["status"])
			}
		}
	}
	if !foundObs {
		t.Fatal("missing observation outbox")
	}
}

func TestApplyDuplicateDoesNotSecondOutbox(t *testing.T) {
	store := poll.NewMemoryStore()
	p := NewProcessor(store)
	env := capturedEnvelope(t)
	if _, err := p.Apply(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	res, err := p.Apply(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != ResultDuplicate {
		t.Fatalf("kind=%s", res.Kind)
	}
	if len(store.Outbox) != 2 {
		t.Fatalf("outbox=%d", len(store.Outbox))
	}
}

func TestApplyAuthorizedThenCapturedUpdates(t *testing.T) {
	store := poll.NewMemoryStore()
	p := NewProcessor(store)
	env := capturedEnvelope(t)
	env.ProviderEventType = "payment.authorized"
	env.Status = "authorized"
	env.Captured = false
	env.ProviderEventID = "evt_auth"
	if _, err := p.Apply(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	env.ProviderEventType = "payment.captured"
	env.Status = "captured"
	env.Captured = true
	env.ProviderEventID = "evt_cap"
	res, err := p.Apply(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != ResultUpdated {
		t.Fatalf("kind=%s", res.Kind)
	}
	var got string
	for _, obs := range store.Payments {
		got = obs.Item.Status
		if !obs.Item.Captured {
			t.Fatal("expected captured")
		}
		if obs.Source != SourceWebhook {
			t.Fatalf("source=%s", obs.Source)
		}
	}
	if got != "captured" {
		t.Fatalf("status=%s", got)
	}
	if len(store.Outbox) != 4 {
		t.Fatalf("outbox=%d", len(store.Outbox))
	}
}

func TestApplyFailedIsNotBankCredited(t *testing.T) {
	store := poll.NewMemoryStore()
	p := NewProcessor(store)
	env := capturedEnvelope(t)
	env.ProviderEventType = "payment.failed"
	env.Status = "failed"
	env.Captured = false
	res, err := p.Apply(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != ResultInserted {
		t.Fatalf("kind=%s", res.Kind)
	}
	for _, obs := range store.Payments {
		if obs.Item.Status != "failed" || obs.Item.Captured {
			t.Fatalf("item=%+v", obs.Item)
		}
	}
}

func TestApplySkipsRefundEvent(t *testing.T) {
	store := poll.NewMemoryStore()
	p := NewProcessor(store)
	env := capturedEnvelope(t)
	env.ProviderEventType = "refund.created"
	env.ProviderEntityType = "refund"
	env.ProviderEntityID = "rfnd_1"
	res, err := p.Apply(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != ResultSkipped {
		t.Fatalf("kind=%s", res.Kind)
	}
	if len(store.Payments) != 0 || len(store.Outbox) != 0 {
		t.Fatal("refund should not persist payment observation")
	}
}

func TestNormalizeRefund(t *testing.T) {
	env := capturedEnvelope(t)
	env.ProviderEventType = "refund.processed"
	env.ProviderEntityType = "refund"
	env.ProviderEntityID = "rfnd_1"
	env.PaymentID = "pay_1"
	env.Amount = 2000
	env.Status = "processed"
	item, ok, err := NormalizeRefund(env)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if item.RefundID != "rfnd_1" || item.PaymentID != "pay_1" || item.AmountMinor != 2000 {
		t.Fatalf("%+v", item)
	}
}

func TestApplyStoresRefundWhenSinkSet(t *testing.T) {
	store := poll.NewMemoryStore()
	sink := recon.NewMemoryFinancialStore()
	p := NewProcessor(store)
	p.Refunds = sink
	env := capturedEnvelope(t)
	env.ProviderEventType = "refund.created"
	env.ProviderEntityType = "refund"
	env.ProviderEntityID = "rfnd_9"
	env.PaymentID = "pay_9"
	env.Amount = 1500
	res, err := p.Apply(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != ResultInserted || res.RefundID != "rfnd_9" {
		t.Fatalf("%+v", res)
	}
	got, err := sink.ListRefunds(context.Background(), env.TenantID, env.ConnectorID, "pay_9")
	if err != nil || len(got) != 1 || got[0].AmountMinor != 1500 {
		t.Fatalf("%+v err=%v", got, err)
	}
}

func TestApplyBytesIgnoresOtherEvents(t *testing.T) {
	store := poll.NewMemoryStore()
	p := NewProcessor(store)
	res, err := p.ApplyBytes(context.Background(), []byte(`{"event_type":"intent.created.v1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != ResultIgnored {
		t.Fatalf("kind=%s", res.Kind)
	}
}

func TestApplyBytesMalformedIgnored(t *testing.T) {
	p := NewProcessor(poll.NewMemoryStore())
	res, err := p.ApplyBytes(context.Background(), []byte(`not-json`))
	if err != nil || res.Kind != ResultIgnored {
		t.Fatalf("kind=%s err=%v", res.Kind, err)
	}
}

func TestApplyRequiresTenant(t *testing.T) {
	p := NewProcessor(poll.NewMemoryStore())
	env := capturedEnvelope(t)
	env.TenantID = ""
	if _, err := p.Apply(context.Background(), env); err == nil {
		t.Fatal("expected error")
	}
}

func payoutEnvelope(t *testing.T) Envelope {
	t.Helper()
	created := time.Unix(1725000000, 0).UTC()
	return Envelope{
		EventName:          EventObservationReceived,
		SchemaVersion:      "v1",
		TenantID:           "11111111-1111-1111-1111-111111111111",
		ConnectorID:        "22222222-2222-2222-2222-222222222222",
		Provider:           "razorpay",
		ProviderMode:       "test",
		ProviderEventID:    "evt_payout_1",
		ProviderEventType:  "payout.processed",
		ProviderEntityType: "payout",
		ProviderEntityID:   "pout_test_123",
		ReceiptID:          "33333333-3333-3333-3333-333333333333",
		RawBodyHash:        "sha256:payout",
		Amount:             25000000,
		Currency:           "INR",
		Status:             "processed",
		ProviderCreatedAt:  &created,
		TraceID:            "trace-payout",
	}
}

func TestNormalizePayoutProcessed(t *testing.T) {
	item, ok, err := NormalizePayout(payoutEnvelope(t))
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if item.PayoutID != "pout_test_123" || item.Status != "processed" || item.AmountMinor != 25000000 {
		t.Fatalf("%+v", item)
	}
}

func TestNormalizePaymentSkipsPayout(t *testing.T) {
	_, ok, err := NormalizePayment(payoutEnvelope(t))
	if err != nil || ok {
		t.Fatalf("payout must not normalize as payment ok=%v err=%v", ok, err)
	}
}

func TestApplyPayoutInsertsCanonical(t *testing.T) {
	store := poll.NewMemoryStore()
	p := NewProcessor(store)
	res, err := p.Apply(context.Background(), payoutEnvelope(t))
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != ResultInserted || res.PayoutID != "pout_test_123" {
		t.Fatalf("%+v", res)
	}
	if len(store.CanonicalPayouts) != 1 {
		t.Fatalf("payouts=%d", len(store.CanonicalPayouts))
	}
	for _, pay := range store.CanonicalPayouts {
		if pay.ProviderStatus != "processed" {
			t.Fatalf("status=%s", pay.ProviderStatus)
		}
	}
}

func TestApplyPayoutLateProcessingDoesNotOverwriteProcessed(t *testing.T) {
	store := poll.NewMemoryStore()
	p := NewProcessor(store)
	env := payoutEnvelope(t)
	if _, err := p.Apply(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	env.ProviderEventType = "payout.processing"
	env.Status = "processing"
	env.ProviderEventID = "evt_payout_late"
	res, err := p.Apply(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != ResultUpdated && res.Kind != ResultInserted {
		t.Fatalf("kind=%s", res.Kind)
	}
	for _, pay := range store.CanonicalPayouts {
		if pay.ProviderStatus != "processed" {
			t.Fatalf("late processing overwrote processed: %s", pay.ProviderStatus)
		}
	}
}

func TestApplyStoresRefundSellerID(t *testing.T) {
	store := poll.NewMemoryStore()
	sink := recon.NewMemoryFinancialStore()
	p := NewProcessor(store)
	p.Refunds = sink
	env := capturedEnvelope(t)
	env.ProviderEventType = "refund.processed"
	env.ProviderEntityType = "refund"
	env.ProviderEntityID = "rfnd_seller"
	env.PaymentID = "pay_seller"
	env.Amount = 2200
	env.SellerID = "  acct_rx  "
	res, err := p.Apply(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != ResultInserted {
		t.Fatalf("%+v", res)
	}
	got, err := sink.ListRefunds(context.Background(), env.TenantID, env.ConnectorID, "pay_seller")
	if err != nil || len(got) != 1 {
		t.Fatalf("%+v err=%v", got, err)
	}
	if got[0].SellerID != "acct_rx" || !got[0].JoinsSellerCluster() {
		t.Fatalf("seller_id round-trip via ingest: %+v", got[0])
	}
}

type stubTransfers struct {
	items     []razorpay.TransferResponse
	reversals map[string][]razorpay.ReversalResponse
	err       error
	calls     []string
	revCalls  []string
}

func (s *stubTransfers) ListTransfersForPayment(_ context.Context, paymentID string) ([]razorpay.TransferResponse, error) {
	s.calls = append(s.calls, paymentID)
	return s.items, s.err
}

func (s *stubTransfers) ListReversalsForTransfer(_ context.Context, transferID string) ([]razorpay.ReversalResponse, error) {
	s.revCalls = append(s.revCalls, transferID)
	if s.reversals == nil {
		return nil, nil
	}
	return s.reversals[transferID], nil
}

func TestEnrichRefundWithTransfersSetsSellerID(t *testing.T) {
	item := reconRefund{RefundID: "rfnd_1", PaymentID: "pay_1"}
	EnrichRefundWithTransfers(&item, []razorpay.TransferResponse{{Recipient: "acc_seller"}})
	if item.SellerID != "acc_seller" {
		t.Fatalf("seller_id=%q", item.SellerID)
	}
}

func TestEnrichRefundWithTransfersAccountFallback(t *testing.T) {
	item := reconRefund{RefundID: "rfnd_1", PaymentID: "pay_1"}
	EnrichRefundWithTransfers(&item, []razorpay.TransferResponse{{Account: "acc_from_create"}})
	if item.SellerID != "acc_from_create" {
		t.Fatalf("seller_id=%q", item.SellerID)
	}
}

func TestEnrichRefundWithTransfersAbsentLeavesEmpty(t *testing.T) {
	item := reconRefund{RefundID: "rfnd_1", PaymentID: "pay_1"}
	EnrichRefundWithTransfers(&item, []razorpay.TransferResponse{{}})
	if item.SellerID != "" {
		t.Fatalf("expected empty seller_id, got %q", item.SellerID)
	}
	EnrichRefundWithTransfers(&item, nil)
	if item.SellerID != "" {
		t.Fatalf("expected empty seller_id, got %q", item.SellerID)
	}
}

func TestEnrichRefundWithTransfersDoesNotOverwrite(t *testing.T) {
	item := reconRefund{RefundID: "rfnd_1", SellerID: "acc_existing"}
	EnrichRefundWithTransfers(&item, []razorpay.TransferResponse{{Recipient: "acc_other"}})
	if item.SellerID != "acc_existing" {
		t.Fatalf("seller_id=%q", item.SellerID)
	}
}

func TestNormalizeRefundCopiesEnvelopeSellerID(t *testing.T) {
	env := capturedEnvelope(t)
	env.ProviderEventType = "refund.processed"
	env.ProviderEntityType = "refund"
	env.ProviderEntityID = "rfnd_1"
	env.PaymentID = "pay_1"
	env.SellerID = "  acc_env  "
	item, ok, err := NormalizeRefund(env)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if item.SellerID != "acc_env" {
		t.Fatalf("seller_id=%q", item.SellerID)
	}
}

func TestNormalizeRefundOmitsSellerIDWhenAbsent(t *testing.T) {
	env := capturedEnvelope(t)
	env.ProviderEventType = "refund.processed"
	env.ProviderEntityType = "refund"
	env.ProviderEntityID = "rfnd_1"
	env.PaymentID = "pay_1"
	item, ok, err := NormalizeRefund(env)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if item.SellerID != "" {
		t.Fatalf("expected empty, got %q", item.SellerID)
	}
	fact := MapRefundFact(item)
	if fact.SellerID != "" {
		t.Fatalf("fact seller_id=%q", fact.SellerID)
	}
}

func TestApplyRefundEnrichesSellerIDFromTransfers(t *testing.T) {
	store := poll.NewMemoryStore()
	sink := recon.NewMemoryFinancialStore()
	transfers := &stubTransfers{items: []razorpay.TransferResponse{{Recipient: "acc_live"}}}
	p := NewProcessor(store)
	p.Refunds = sink
	p.Transfers = transfers
	env := capturedEnvelope(t)
	env.ProviderEventType = "refund.created"
	env.ProviderEntityType = "refund"
	env.ProviderEntityID = "rfnd_enrich"
	env.PaymentID = "pay_enrich"
	env.Amount = 900
	res, err := p.Apply(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != ResultInserted {
		t.Fatalf("%+v", res)
	}
	if len(transfers.calls) != 1 || transfers.calls[0] != "pay_enrich" {
		t.Fatalf("calls=%v", transfers.calls)
	}
	got, err := sink.ListRefunds(context.Background(), env.TenantID, env.ConnectorID, "pay_enrich")
	if err != nil || len(got) != 1 {
		t.Fatalf("%+v err=%v", got, err)
	}
	if got[0].SellerID != "acc_live" {
		t.Fatalf("seller_id=%q", got[0].SellerID)
	}
}

func TestApplyRefundWebhookWithoutTransfersLeavesSellerIDEmpty(t *testing.T) {
	store := poll.NewMemoryStore()
	sink := recon.NewMemoryFinancialStore()
	p := NewProcessor(store)
	p.Refunds = sink
	env := capturedEnvelope(t)
	env.ProviderEventType = "refund.created"
	env.ProviderEntityType = "refund"
	env.ProviderEntityID = "rfnd_wh"
	env.PaymentID = "pay_wh"
	env.Amount = 700
	if _, err := p.Apply(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	got, err := sink.ListRefunds(context.Background(), env.TenantID, env.ConnectorID, "pay_wh")
	if err != nil || len(got) != 1 {
		t.Fatalf("%+v err=%v", got, err)
	}
	if got[0].SellerID != "" {
		t.Fatalf("webhook must leave seller_id empty, got %q", got[0].SellerID)
	}
}

func TestApplyRefundSkipsTransferLookupWhenEnvelopeHasSellerID(t *testing.T) {
	store := poll.NewMemoryStore()
	sink := recon.NewMemoryFinancialStore()
	transfers := &stubTransfers{items: []razorpay.TransferResponse{{Recipient: "acc_should_not"}}}
	p := NewProcessor(store)
	p.Refunds = sink
	p.Transfers = transfers
	env := capturedEnvelope(t)
	env.ProviderEventType = "refund.created"
	env.ProviderEntityType = "refund"
	env.ProviderEntityID = "rfnd_preset"
	env.PaymentID = "pay_preset"
	env.SellerID = "acc_preset"
	if _, err := p.Apply(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	if len(transfers.calls) != 0 {
		t.Fatalf("should not call transfers when seller_id preset, calls=%v", transfers.calls)
	}
	got, _ := sink.ListRefunds(context.Background(), env.TenantID, env.ConnectorID, "pay_preset")
	if len(got) != 1 || got[0].SellerID != "acc_preset" {
		t.Fatalf("%+v", got)
	}
}

func TestApplyRefundTransfersAbsentLeavesEmpty(t *testing.T) {
	store := poll.NewMemoryStore()
	sink := recon.NewMemoryFinancialStore()
	transfers := &stubTransfers{items: []razorpay.TransferResponse{}}
	p := NewProcessor(store)
	p.Refunds = sink
	p.Transfers = transfers
	env := capturedEnvelope(t)
	env.ProviderEventType = "refund.created"
	env.ProviderEntityType = "refund"
	env.ProviderEntityID = "rfnd_none"
	env.PaymentID = "pay_none"
	if _, err := p.Apply(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	got, _ := sink.ListRefunds(context.Background(), env.TenantID, env.ConnectorID, "pay_none")
	if len(got) != 1 || got[0].SellerID != "" {
		t.Fatalf("must not invent seller_id: %+v", got)
	}
}

func TestMarketplaceEdgesFromTransfersMapsSellerAndLinks(t *testing.T) {
	edges := MarketplaceEdgesFromTransfers("pay_1", "rfnd_1", "acc_r", []razorpay.TransferResponse{
		{ID: "trf_1", Recipient: "acc_r", Amount: 500, Currency: "INR", CreatedAt: 1725000000},
		{ID: "trf_2", Account: "acc_a", Amount: 700, Source: "pay_ignored"},
		{ID: "", Recipient: "acc_skip"}, // skipped — no transfer id
		{ID: "trf_3", Amount: 100},      // empty seller stays empty
	})
	if len(edges) != 3 {
		t.Fatalf("edges=%+v", edges)
	}
	if edges[0].SellerID != "acc_r" || edges[0].PaymentID != "pay_1" || edges[0].RefundID != "rfnd_1" {
		t.Fatalf("%+v", edges[0])
	}
	if edges[1].SellerID != "acc_a" {
		t.Fatalf("%+v", edges[1])
	}
	if edges[2].SellerID != "" {
		t.Fatalf("must not invent seller_id: %+v", edges[2])
	}
}

func TestApplyRefundUpsertsTransferEdges(t *testing.T) {
	store := poll.NewMemoryStore()
	sink := recon.NewMemoryFinancialStore()
	transfers := &stubTransfers{items: []razorpay.TransferResponse{
		{ID: "trf_live", Recipient: "acc_live", Amount: 900, Currency: "INR"},
	}}
	p := NewProcessor(store)
	p.Refunds = sink
	p.Transfers = transfers
	p.Edges = sink
	env := capturedEnvelope(t)
	env.ProviderEventType = "refund.created"
	env.ProviderEntityType = "refund"
	env.ProviderEntityID = "rfnd_edge"
	env.PaymentID = "pay_edge"
	env.Amount = 900
	if _, err := p.Apply(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	edges, err := sink.ListTransferEdges(context.Background(), env.TenantID, env.ConnectorID)
	if err != nil || len(edges) != 1 {
		t.Fatalf("edges=%+v err=%v", edges, err)
	}
	e := edges[0]
	if e.TransferID != "trf_live" || e.PaymentID != "pay_edge" || e.RefundID != "rfnd_edge" {
		t.Fatalf("%+v", e)
	}
	if e.SellerID != "acc_live" || e.AmountMinor != 900 {
		t.Fatalf("%+v", e)
	}
}

func TestApplyRefundUpsertsEdgesEvenWhenSellerPreset(t *testing.T) {
	store := poll.NewMemoryStore()
	sink := recon.NewMemoryFinancialStore()
	transfers := &stubTransfers{items: []razorpay.TransferResponse{
		{ID: "trf_preset", Account: "acc_from_xfer", Amount: 400},
	}}
	p := NewProcessor(store)
	p.Refunds = sink
	p.Transfers = transfers
	p.Edges = sink
	env := capturedEnvelope(t)
	env.ProviderEventType = "refund.created"
	env.ProviderEntityType = "refund"
	env.ProviderEntityID = "rfnd_preset_edge"
	env.PaymentID = "pay_preset_edge"
	env.SellerID = "acc_preset"
	if _, err := p.Apply(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	if len(transfers.calls) != 1 {
		t.Fatalf("Edges wired → must list transfers, calls=%v", transfers.calls)
	}
	got, _ := sink.ListRefunds(context.Background(), env.TenantID, env.ConnectorID, "pay_preset_edge")
	if len(got) != 1 || got[0].SellerID != "acc_preset" {
		t.Fatalf("must not overwrite preset seller: %+v", got)
	}
	edges, _ := sink.ListTransferEdges(context.Background(), env.TenantID, env.ConnectorID)
	if len(edges) != 1 || edges[0].SellerID != "acc_from_xfer" {
		t.Fatalf("edge seller from transfer: %+v", edges)
	}
}

func TestApplyRefundTransferEdgesEmptySeller(t *testing.T) {
	store := poll.NewMemoryStore()
	sink := recon.NewMemoryFinancialStore()
	transfers := &stubTransfers{items: []razorpay.TransferResponse{
		{ID: "trf_noseller", Amount: 50},
	}}
	p := NewProcessor(store)
	p.Refunds = sink
	p.Transfers = transfers
	p.Edges = sink
	env := capturedEnvelope(t)
	env.ProviderEventType = "refund.created"
	env.ProviderEntityType = "refund"
	env.ProviderEntityID = "rfnd_noseller"
	env.PaymentID = "pay_noseller"
	if _, err := p.Apply(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	edges, _ := sink.ListTransferEdges(context.Background(), env.TenantID, env.ConnectorID)
	if len(edges) != 1 || edges[0].SellerID != "" {
		t.Fatalf("empty seller must persist: %+v", edges)
	}
}

func TestApplyRefundFillsReverseFromReversals(t *testing.T) {
	store := poll.NewMemoryStore()
	sink := recon.NewMemoryFinancialStore()
	transfers := &stubTransfers{
		items: []razorpay.TransferResponse{{ID: "trf_r", Recipient: "acc_r", Amount: 800}},
		reversals: map[string][]razorpay.ReversalResponse{
			"trf_r": {{ID: "rvrsl_r", TransferID: "trf_r", CreatedAt: 1725000100}},
		},
	}
	p := NewProcessor(store)
	p.Refunds = sink
	p.Transfers = transfers
	p.Edges = sink
	env := capturedEnvelope(t)
	env.ProviderEventType = "refund.processed"
	env.ProviderEntityType = "refund"
	env.ProviderEntityID = "rfnd_r"
	env.PaymentID = "pay_r"
	if _, err := p.Apply(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	edges, _ := sink.ListTransferEdges(context.Background(), env.TenantID, env.ConnectorID)
	if len(edges) != 1 || edges[0].ReverseTransferID != "rvrsl_r" || !edges[0].HasReverse() {
		t.Fatalf("%+v", edges)
	}
}

func TestApplyReversalObservationUpdatesSameEdge(t *testing.T) {
	store := poll.NewMemoryStore()
	sink := recon.NewMemoryFinancialStore()
	p := NewProcessor(store)
	p.Edges = sink
	ctx := context.Background()
	_, err := sink.UpsertTransferEdge(ctx, "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222", recon.MarketplaceTransferEdge{
		TransferID: "trf_obs", PaymentID: "pay_obs", SellerID: "acc_obs", AmountMinor: 600,
	})
	if err != nil {
		t.Fatal(err)
	}
	env := capturedEnvelope(t)
	env.ProviderEventType = ""
	env.ProviderEntityType = "reversal"
	env.ProviderEntityID = "rvrsl_obs"
	env.TransferID = "trf_obs"
	env.PaymentID = "pay_obs"
	res, err := p.Apply(ctx, env)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != ResultUpdated {
		t.Fatalf("%+v", res)
	}
	edges, _ := sink.ListTransferEdges(ctx, env.TenantID, env.ConnectorID)
	if len(edges) != 1 {
		t.Fatalf("%+v", edges)
	}
	if edges[0].ReverseTransferID != "rvrsl_obs" || edges[0].SellerID != "acc_obs" || edges[0].AmountMinor != 600 {
		t.Fatalf("%+v", edges[0])
	}
}

// Edges wired but Transfers nil (no Razorpay creds on the live path): refund
// still lands from the envelope only. No panic, no edges, no invented seller_id,
// amount unchanged.
func TestApplyRefundEdgesWiredTransfersNilEnvelopeOnly(t *testing.T) {
	store := poll.NewMemoryStore()
	sink := recon.NewMemoryFinancialStore()
	p := NewProcessor(store)
	p.Refunds = sink
	p.Edges = sink
	p.Transfers = nil
	env := capturedEnvelope(t)
	env.ProviderEventType = "refund.created"
	env.ProviderEntityType = "refund"
	env.ProviderEntityID = "rfnd_nil_xfer"
	env.PaymentID = "pay_nil_xfer"
	env.Amount = 700
	env.SellerID = ""
	if _, err := p.Apply(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	edges, err := sink.ListTransferEdges(context.Background(), env.TenantID, env.ConnectorID)
	if err != nil || len(edges) != 0 {
		t.Fatalf("Transfers nil must create no edges: edges=%+v err=%v", edges, err)
	}
	got, err := sink.ListRefunds(context.Background(), env.TenantID, env.ConnectorID, "pay_nil_xfer")
	if err != nil || len(got) != 1 {
		t.Fatalf("refund must still land: got=%+v err=%v", got, err)
	}
	if got[0].SellerID != "" {
		t.Fatalf("seller_id must stay empty, got %q", got[0].SellerID)
	}
	if got[0].AmountMinor != 700 {
		t.Fatalf("amount must be unchanged, got %d", got[0].AmountMinor)
	}
}

// Transfer/reversal envelopes are classified by provider_entity_type ONLY.
// An envelope carrying transfer ids but no entity type must be ignored.
func TestNormalizeTransferEdgeIgnoresEnvelopeWithoutEntityType(t *testing.T) {
	for _, evt := range []string{"transfer.processed", "transfer.reversed", ""} {
		env := capturedEnvelope(t)
		env.ProviderEventType = evt
		env.ProviderEntityType = ""
		env.ProviderEntityID = "trf_x"
		env.TransferID = "trf_x"
		env.ReverseTransferID = "rvrsl_x"
		_, ok, err := NormalizeTransferEdge(env)
		if ok || err != nil {
			t.Fatalf("event %q: ok=%v err=%v", evt, ok, err)
		}
	}
}

func TestNormalizeTransferEdgeClassifiesByEntityType(t *testing.T) {
	env := capturedEnvelope(t)
	env.ProviderEventType = "transfer.processed"
	env.ProviderEntityType = "transfer"
	env.ProviderEntityID = "trf_n"
	env.TransferID = "trf_n"
	env.PaymentID = "pay_n"
	env.SellerID = "acc_n"
	env.Amount = 1200
	e, ok, err := NormalizeTransferEdge(env)
	if err != nil || !ok || e.TransferID != "trf_n" || e.AmountMinor != 1200 || e.SellerID != "acc_n" {
		t.Fatalf("ok=%v err=%v %+v", ok, err, e)
	}
	env = capturedEnvelope(t)
	env.ProviderEventType = "transfer.reversed"
	env.ProviderEntityType = "reversal"
	env.ProviderEntityID = "rvrsl_n"
	env.ReverseTransferID = "rvrsl_n"
	env.TransferID = "trf_n"
	e, ok, err = NormalizeTransferEdge(env)
	if err != nil || !ok || e.ReverseTransferID != "rvrsl_n" || e.TransferID != "trf_n" || e.AmountMinor != 0 || e.SellerID != "" {
		t.Fatalf("ok=%v err=%v %+v", ok, err, e)
	}
}

// Edge webhook JSON shape (provider.observation.received with transfer_id /
// reverse_transfer_id) round-trips through ParseEnvelope into an edge upsert.
func TestApplyBytesEdgeReversalPayloadUpdatesEdge(t *testing.T) {
	store := poll.NewMemoryStore()
	sink := recon.NewMemoryFinancialStore()
	p := NewProcessor(store)
	p.Edges = sink
	ctx := context.Background()
	tenant, conn := "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	fwd := []byte(`{"event_name":"provider.observation.received","tenant_id":"` + tenant + `","connector_id":"` + conn + `",
		"provider":"razorpay","provider_event_type":"transfer.processed","provider_entity_type":"transfer","provider_entity_id":"trf_e",
		"amount":900,"currency":"INR","transfer_id":"trf_e","payment_id":"pay_e","seller_id":"acc_e"}`)
	if res, err := p.ApplyBytes(ctx, fwd); err != nil || res.Kind != ResultInserted {
		t.Fatalf("%+v %v", res, err)
	}
	rev := []byte(`{"event_name":"provider.observation.received","tenant_id":"` + tenant + `","connector_id":"` + conn + `",
		"provider":"razorpay","provider_event_type":"transfer.reversed","provider_entity_type":"reversal","provider_entity_id":"rvrsl_e",
		"amount":900,"currency":"INR","transfer_id":"trf_e","reverse_transfer_id":"rvrsl_e"}`)
	if res, err := p.ApplyBytes(ctx, rev); err != nil || res.Kind != ResultUpdated {
		t.Fatalf("%+v %v", res, err)
	}
	edges, _ := sink.ListTransferEdges(ctx, tenant, conn)
	if len(edges) != 1 || edges[0].ReverseTransferID != "rvrsl_e" || edges[0].SellerID != "acc_e" || edges[0].AmountMinor != 900 || edges[0].PaymentID != "pay_e" {
		t.Fatalf("%+v", edges)
	}
}

func TestRefundIngest_StampsRefundIDOnlyOnMatchingSellerEdge(t *testing.T) {
	edges := MarketplaceEdgesFromTransfers("pay_s", "rfnd_A", " acc_A ", []razorpay.TransferResponse{
		{ID: "trf_A", Recipient: "acc_A", Amount: 600},
		{ID: "trf_B", Recipient: "acc_B", Amount: 400},
		{ID: "trf_none", Amount: 100},
	})
	if len(edges) != 3 {
		t.Fatalf("all edges still upsert: %+v", edges)
	}
	if edges[0].RefundID != "rfnd_A" || edges[1].RefundID != "" || edges[2].RefundID != "" {
		t.Fatalf("refund_id only on matching seller edge: %+v", edges)
	}
	// Refund without seller_id: no edge gets refund_id.
	for _, e := range MarketplaceEdgesFromTransfers("pay_s", "rfnd_A", "  ", []razorpay.TransferResponse{
		{ID: "trf_A", Recipient: "acc_A"}, {ID: "trf_none"},
	}) {
		if e.RefundID != "" {
			t.Fatalf("no seller → no stamp: %+v", e)
		}
	}

	// Via ingest: refund for acc_A on a split payment.
	store := poll.NewMemoryStore()
	sink := recon.NewMemoryFinancialStore()
	p := NewProcessor(store)
	p.Refunds = sink
	p.Edges = sink
	p.Transfers = &stubTransfers{items: []razorpay.TransferResponse{
		{ID: "trf_A", Recipient: "acc_A", Amount: 600},
		{ID: "trf_B", Recipient: "acc_B", Amount: 400},
	}}
	env := capturedEnvelope(t)
	env.ProviderEventType = "refund.created"
	env.ProviderEntityType = "refund"
	env.ProviderEntityID = "rfnd_A"
	env.PaymentID = "pay_s"
	env.SellerID = "acc_A"
	if _, err := p.Apply(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	got, _ := sink.ListTransferEdges(context.Background(), env.TenantID, env.ConnectorID)
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	for _, e := range got {
		want := ""
		if e.SellerID == "acc_A" {
			want = "rfnd_A"
		}
		if e.RefundID != want {
			t.Fatalf("edge %s seller %s refund_id=%q want %q", e.TransferID, e.SellerID, e.RefundID, want)
		}
	}
}

// End-to-end: B's transfer reversed (no customer_refund_id), refund for A lands.
// A's refund_without_reverse_transfer exception must appear.
func TestRefundIngest_SplitPaymentOtherSellerReversedStillFlags(t *testing.T) {
	store := poll.NewMemoryStore()
	sink := recon.NewMemoryFinancialStore()
	p := NewProcessor(store)
	p.Refunds = sink
	p.Edges = sink
	p.Transfers = &stubTransfers{
		items: []razorpay.TransferResponse{
			{ID: "trf_A", Recipient: "acc_A", Amount: 600},
			{ID: "trf_B", Recipient: "acc_B", Amount: 400},
		},
		reversals: map[string][]razorpay.ReversalResponse{
			"trf_B": {{ID: "rvrsl_B", TransferID: "trf_B", CreatedAt: 1725000100}},
		},
	}
	env := capturedEnvelope(t)
	env.ProviderEventType = "refund.processed"
	env.ProviderEntityType = "refund"
	env.ProviderEntityID = "rfnd_A"
	env.PaymentID = "pay_split"
	env.SellerID = "acc_A"
	env.Amount = 600
	if _, err := p.Apply(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	list, err := recon.NewFinancialService(sink).ListExceptions(context.Background(), env.TenantID, env.ConnectorID)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, ex := range list {
		if ex.EntityID == "rfnd_A" && ex.Reason == recon.ReasonRefundWithoutReverse {
			found = true
			if ex.ReconciliationResult != recon.ResultUnresolved || ex.VarianceAmount != 0 {
				t.Fatalf("%+v", ex)
			}
		}
	}
	if !found {
		t.Fatalf("seller A refund must flag refund_without_reverse_transfer: %+v", list)
	}
}

func TestApplyRefundReversalCustomerRefundIDWins(t *testing.T) {
	store := poll.NewMemoryStore()
	sink := recon.NewMemoryFinancialStore()
	p := NewProcessor(store)
	p.Refunds = sink
	p.Edges = sink
	p.Transfers = &stubTransfers{
		items: []razorpay.TransferResponse{{ID: "trf_A", Recipient: "acc_A", Amount: 600}},
		reversals: map[string][]razorpay.ReversalResponse{
			"trf_A": {{ID: "rvrsl_A", TransferID: "trf_A", CustomerRefundID: "rfnd_customer"}},
		},
	}
	env := capturedEnvelope(t)
	env.ProviderEventType = "refund.processed"
	env.ProviderEntityType = "refund"
	env.ProviderEntityID = "rfnd_obs"
	env.PaymentID = "pay_c"
	env.SellerID = "acc_A"
	if _, err := p.Apply(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	got, _ := sink.ListTransferEdges(context.Background(), env.TenantID, env.ConnectorID)
	if len(got) != 1 || got[0].RefundID != "rfnd_customer" {
		t.Fatalf("customer_refund_id must take precedence: %+v", got)
	}
}

// Split payment, refund without its own seller_id: must not guess a seller,
// must not stamp refund_id on any edge, and must not raise
// refund_without_reverse_transfer (empty seller never joins a cluster).
func TestRefundIngest_SplitPaymentNoSellerLeavesEmptyAndSkipsDetector(t *testing.T) {
	store := poll.NewMemoryStore()
	sink := recon.NewMemoryFinancialStore()
	p := NewProcessor(store)
	p.Refunds = sink
	p.Edges = sink
	p.Transfers = &stubTransfers{items: []razorpay.TransferResponse{
		{ID: "trf_A", Recipient: "acc_A", Amount: 600},
		{ID: "trf_B", Recipient: "acc_B", Amount: 400},
	}}
	env := capturedEnvelope(t)
	env.ProviderEventType = "refund.processed"
	env.ProviderEntityType = "refund"
	env.ProviderEntityID = "rfnd_split"
	env.PaymentID = "pay_split_ns"
	env.SellerID = ""
	env.Amount = 600
	if _, err := p.Apply(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	refunds, _ := sink.ListRefunds(context.Background(), env.TenantID, env.ConnectorID, "pay_split_ns")
	if len(refunds) != 1 || refunds[0].SellerID != "" || refunds[0].JoinsSellerCluster() {
		t.Fatalf("split payment must leave seller_id empty: %+v", refunds)
	}
	edges, _ := sink.ListTransferEdges(context.Background(), env.TenantID, env.ConnectorID)
	if len(edges) != 2 {
		t.Fatalf("edges still upsert: %+v", edges)
	}
	for _, e := range edges {
		if e.RefundID != "" {
			t.Fatalf("no seller → no refund_id stamp: %+v", e)
		}
	}
	if sigs := recon.DetectRefundsWithoutReverse(refunds, edges); len(sigs) != 0 {
		t.Fatalf("empty-seller refund must be skipped by detector: %+v", sigs)
	}
	list, err := recon.NewFinancialService(sink).ListExceptions(context.Background(), env.TenantID, env.ConnectorID)
	if err != nil {
		t.Fatal(err)
	}
	for _, ex := range list {
		if ex.Reason == recon.ReasonRefundWithoutReverse {
			t.Fatalf("unexpected refund_without_reverse_transfer: %+v", ex)
		}
	}
}

func TestEnrichRefundWithTransfersSplitPaymentLeavesEmpty(t *testing.T) {
	item := reconRefund{RefundID: "rfnd_s", PaymentID: "pay_s"}
	EnrichRefundWithTransfers(&item, []razorpay.TransferResponse{{Recipient: "acc_A"}, {Account: "acc_B"}})
	if item.SellerID != "" {
		t.Fatalf("seller_id=%q", item.SellerID)
	}
	EnrichRefundWithTransfers(&item, []razorpay.TransferResponse{{Recipient: "acc_A"}, {Account: "acc_A"}, {}})
	if item.SellerID != "acc_A" {
		t.Fatalf("seller_id=%q", item.SellerID)
	}
}
