package services

import (
	"testing"

	"zord-edge/model"
)

// Recon observe classifies transfer / reversal envelopes ONLY by
// provider_entity_type ("transfer" / "reversal"); an envelope without it is
// ignored. These tests pin that the Edge webhook emitter always sets it for
// Route entities.

func parseMeta(t *testing.T, raw string) model.WebhookMetadata {
	t.Helper()
	meta, err := NewRazorpayWebhookService().ParseMetadata([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return meta
}

func TestParseMetadataTransferEntitySetsProviderEntityType(t *testing.T) {
	meta := parseMeta(t, `{"entity":"event","event":"transfer.processed","created_at":1725000000,
		"payload":{"transfer":{"entity":{"id":"trf_1","entity":"transfer","source":"pay_1","recipient":"acc_seller","amount":1000,"currency":"INR","status":"processed"}}}}`)
	if meta.EntityType != "transfer" || meta.EntityID != "trf_1" {
		t.Fatalf("%+v", meta)
	}
	if meta.TransferID != "trf_1" || meta.PaymentID != "pay_1" || meta.SellerID != "acc_seller" || meta.AmountMinor != 1000 {
		t.Fatalf("%+v", meta)
	}
	if meta.ReverseTransferID != "" {
		t.Fatalf("reverse id must be empty: %+v", meta)
	}
}

func TestParseMetadataTransferWinsOverAccompanyingPayment(t *testing.T) {
	meta := parseMeta(t, `{"entity":"event","event":"transfer.processed",
		"payload":{"payment":{"entity":{"id":"pay_9","entity":"payment","amount":5000}},
		"transfer":{"entity":{"id":"trf_9","entity":"transfer","source":"pay_9","account":"acc_9","amount":900}}}}`)
	if meta.EntityType != "transfer" || meta.EntityID != "trf_9" || meta.SellerID != "acc_9" || meta.PaymentID != "pay_9" {
		t.Fatalf("%+v", meta)
	}
}

func TestParseMetadataTransferNonPaymentSourceLeavesPaymentIDEmpty(t *testing.T) {
	meta := parseMeta(t, `{"entity":"event","event":"transfer.processed",
		"payload":{"transfer":{"entity":{"id":"trf_o","entity":"transfer","source":"order_1","amount":100}}}}`)
	if meta.EntityType != "transfer" || meta.PaymentID != "" || meta.SellerID != "" {
		t.Fatalf("never invent payment_id/seller_id: %+v", meta)
	}
}

// transfer.reversed: classified by payload entity. A reversal entity →
// "reversal" with parent transfer_id; only a transfer entity → "transfer".
func TestParseMetadataTransferReversedWithReversalEntity(t *testing.T) {
	meta := parseMeta(t, `{"entity":"event","event":"transfer.reversed",
		"payload":{"transfer":{"entity":{"id":"trf_1","entity":"transfer","amount":1000,"amount_reversed":1000}},
		"reversal":{"entity":{"id":"rvrsl_1","entity":"reversal","transfer_id":"trf_1","amount":1000,"currency":"INR"}}}}`)
	if meta.EntityType != "reversal" || meta.EntityID != "rvrsl_1" || meta.ReverseTransferID != "rvrsl_1" || meta.TransferID != "trf_1" {
		t.Fatalf("%+v", meta)
	}
	if meta.SellerID != "" {
		t.Fatalf("seller must not be invented on reversal: %+v", meta)
	}
}

func TestParseMetadataTransferReversedWithOnlyTransferEntity(t *testing.T) {
	meta := parseMeta(t, `{"entity":"event","event":"transfer.reversed",
		"payload":{"transfer":{"entity":{"id":"trf_2","entity":"transfer","source":"pay_2","recipient":"acc_2","amount":700,"amount_reversed":700}}}}`)
	if meta.EntityType != "transfer" || meta.TransferID != "trf_2" || meta.ReverseTransferID != "" {
		t.Fatalf("%+v", meta)
	}
}

func TestParseMetadataEntityFieldNormalizedCase(t *testing.T) {
	meta := parseMeta(t, `{"entity":"event","event":"transfer.failed",
		"payload":{"transfer":{"entity":{"id":"trf_3","entity":"Transfer","amount":1}}}}`)
	if meta.EntityType != "transfer" {
		t.Fatalf("%+v", meta)
	}
}

func TestParseMetadataPaymentUnchangedNoRouteRefs(t *testing.T) {
	meta := parseMeta(t, `{"entity":"event","event":"payment.captured",
		"payload":{"payment":{"entity":{"id":"pay_x","entity":"payment","amount":100}}}}`)
	if meta.EntityType != "payment" || meta.TransferID != "" || meta.PaymentID != "" || meta.SellerID != "" {
		t.Fatalf("%+v", meta)
	}
}

func TestAddRouteReferencesOnlyWhenPresent(t *testing.T) {
	p := map[string]any{"provider_entity_type": "reversal"}
	addRouteReferences(p, model.WebhookMetadata{TransferID: "trf_1", ReverseTransferID: "rvrsl_1"})
	if p["transfer_id"] != "trf_1" || p["reverse_transfer_id"] != "rvrsl_1" {
		t.Fatalf("%+v", p)
	}
	if _, ok := p["seller_id"]; ok {
		t.Fatalf("seller_id must be absent when empty: %+v", p)
	}
	if _, ok := p["payment_id"]; ok {
		t.Fatalf("payment_id must be absent when empty: %+v", p)
	}
	q := map[string]any{}
	addRouteReferences(q, model.WebhookMetadata{EntityType: "payment"})
	if len(q) != 0 {
		t.Fatalf("payment payload must be unchanged: %+v", q)
	}
}
