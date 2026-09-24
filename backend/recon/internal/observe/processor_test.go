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
	items []razorpay.TransferResponse
	err   error
	calls []string
}

func (s *stubTransfers) ListTransfersForPayment(_ context.Context, paymentID string) ([]razorpay.TransferResponse, error) {
	s.calls = append(s.calls, paymentID)
	return s.items, s.err
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
