package observe

import (
	"fmt"
	"strings"
	"time"

	"zord-outcome-engine/internal/poll/providers/razorpay"
	"zord-outcome-engine/internal/recon"
)

func isPayoutEvent(eventType, entityType string) bool {
	et := strings.ToLower(strings.TrimSpace(eventType))
	ent := strings.ToLower(strings.TrimSpace(entityType))
	if strings.HasPrefix(et, "payout.") {
		return true
	}
	return ent == "payout"
}

func isPaymentEvent(eventType, entityType string) bool {
	et := strings.ToLower(strings.TrimSpace(eventType))
	ent := strings.ToLower(strings.TrimSpace(entityType))
	if strings.HasPrefix(et, "payment.") {
		return true
	}
	return ent == "payment"
}

func statusFromEvent(eventType, payloadStatus string) (status string, captured bool) {
	status = strings.ToLower(strings.TrimSpace(payloadStatus))
	switch strings.ToLower(strings.TrimSpace(eventType)) {
	case "payment.captured":
		if status == "" {
			status = "captured"
		}
		captured = true
	case "payment.authorized":
		if status == "" {
			status = "authorized"
		}
	case "payment.failed":
		if status == "" {
			status = "failed"
		}
	}
	if status == "captured" {
		captured = true
	}
	return status, captured
}

// NormalizePayment maps a webhook observation onto the same NeutralPayment
// shape used by API backfill. It does not rank captured vs failed.
func NormalizePayment(env Envelope) (razorpay.NeutralPayment, bool, error) {
	if !isPaymentEvent(env.ProviderEventType, env.ProviderEntityType) {
		return razorpay.NeutralPayment{}, false, nil
	}
	paymentID := strings.TrimSpace(env.ProviderEntityID)
	if paymentID == "" {
		return razorpay.NeutralPayment{}, false, fmt.Errorf("missing provider_entity_id")
	}
	status, captured := statusFromEvent(env.ProviderEventType, env.Status)
	status = razorpay.NormalizePaymentStatus(status)
	if env.Captured {
		captured = true
	}
	currency := strings.TrimSpace(env.Currency)
	if currency == "" {
		currency = "INR"
	}
	created := time.Time{}
	if env.ProviderCreatedAt != nil {
		created = env.ProviderCreatedAt.UTC()
	}
	item := razorpay.NeutralPayment{
		PaymentID:   paymentID,
		OrderID:     strings.TrimSpace(env.OrderID),
		AmountMinor: env.Amount,
		Currency:    currency,
		Status:      status,
		Captured:    captured,
		FeeMinor:    env.Fee,
		TaxMinor:    env.Tax,
		CreatedAt:   created,
	}
	if captured && !created.IsZero() {
		item.CapturedAt = created
	}
	canonical, err := razorpay.CanonicalizeForHash(map[string]any{
		"payment_id":     item.PaymentID,
		"order_id":       item.OrderID,
		"amount":         item.AmountMinor,
		"currency":       item.Currency,
		"status":         item.Status,
		"captured":       item.Captured,
		"fee":            item.FeeMinor,
		"tax":            item.TaxMinor,
		"event_type":     env.ProviderEventType,
		"raw_body_hash":  env.RawBodyHash,
		"provider_event": env.ProviderEventID,
	})
	if err != nil {
		return razorpay.NeutralPayment{}, false, err
	}
	item.PayloadHash = razorpay.HashRawResponse(canonical)
	return item, true, nil
}

// NormalizePayout maps payout.* webhook observations onto NeutralPayout.
// Provider status is stored exactly as Razorpay sent it (normalized spelling only).
func NormalizePayout(env Envelope) (razorpay.NeutralPayout, bool, error) {
	if !isPayoutEvent(env.ProviderEventType, env.ProviderEntityType) {
		return razorpay.NeutralPayout{}, false, nil
	}
	payoutID := strings.TrimSpace(env.ProviderEntityID)
	if payoutID == "" {
		return razorpay.NeutralPayout{}, false, fmt.Errorf("missing provider_entity_id")
	}
	status := razorpay.NormalizePayoutStatus(env.Status)
	if status == "" {
		status = razorpay.NormalizePayoutStatus(strings.TrimPrefix(strings.ToLower(env.ProviderEventType), "payout."))
	}
	currency := strings.TrimSpace(env.Currency)
	if currency == "" {
		currency = "INR"
	}
	created := time.Time{}
	if env.ProviderCreatedAt != nil {
		created = env.ProviderCreatedAt.UTC()
	}
	item := razorpay.NeutralPayout{
		PayoutID:    payoutID,
		AmountMinor: env.Amount,
		Currency:    currency,
		Status:      status,
		CreatedAt:   created,
	}
	canonical, err := razorpay.CanonicalizeForHash(map[string]any{
		"payout_id":      item.PayoutID,
		"amount":         item.AmountMinor,
		"currency":       item.Currency,
		"status":         item.Status,
		"event_type":     env.ProviderEventType,
		"raw_body_hash":  env.RawBodyHash,
		"provider_event": env.ProviderEventID,
	})
	if err != nil {
		return razorpay.NeutralPayout{}, false, err
	}
	item.PayloadHash = razorpay.HashRawResponse(canonical)
	return item, true, nil
}

func isRefundEvent(eventType, entityType string) bool {
	et := strings.ToLower(strings.TrimSpace(eventType))
	ent := strings.ToLower(strings.TrimSpace(entityType))
	if strings.HasPrefix(et, "refund.") {
		return true
	}
	return ent == "refund"
}

func NormalizeRefund(env Envelope) (reconRefund, bool, error) {
	if !isRefundEvent(env.ProviderEventType, env.ProviderEntityType) {
		return reconRefund{}, false, nil
	}
	id := strings.TrimSpace(env.ProviderEntityID)
	if id == "" {
		return reconRefund{}, false, fmt.Errorf("missing refund id")
	}
	status := strings.ToLower(strings.TrimSpace(env.Status))
	if status == "" {
		status = strings.TrimPrefix(strings.ToLower(env.ProviderEventType), "refund.")
	}
	cur := strings.TrimSpace(env.Currency)
	if cur == "" {
		cur = "INR"
	}
	return reconRefund{
		RefundID:       id,
		PaymentID:      strings.TrimSpace(env.PaymentID),
		AmountMinor:    env.Amount,
		Currency:       cur,
		ProviderStatus: status,
		Source:         SourceWebhook,
		SellerID:       strings.TrimSpace(env.SellerID),
	}, true, nil
}

type reconRefund struct {
	RefundID       string
	PaymentID      string
	AmountMinor    int64
	Currency       string
	ProviderStatus string
	Source         string
	SellerID       string
}

// EnrichRefundWithTransfers sets SellerID from payment→transfers when present.
// Leaves SellerID untouched when already set or when no transfer recipient/account exists.
// Never invents a sentinel like "unknown".
func EnrichRefundWithTransfers(item *reconRefund, transfers []razorpay.TransferResponse) {
	if item == nil {
		return
	}
	if strings.TrimSpace(item.SellerID) != "" {
		return
	}
	if sid := razorpay.SellerIDFromTransfers(transfers); sid != "" {
		item.SellerID = sid
	}
}

// MapRefundFact maps a normalized refund observation onto recon.RefundFact.
func MapRefundFact(item reconRefund) recon.RefundFact {
	return recon.RefundFact{
		RefundID:       item.RefundID,
		PaymentID:      item.PaymentID,
		AmountMinor:    item.AmountMinor,
		Currency:       item.Currency,
		ProviderStatus: item.ProviderStatus,
		Source:         item.Source,
		SellerID:       strings.TrimSpace(item.SellerID),
	}
}
