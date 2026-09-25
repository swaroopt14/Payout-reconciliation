package razorpay

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// TransferResponse is a Razorpay Route/transfer DTO for a payment.
// Create payloads use "account"; list/fetch responses commonly expose "recipient".
type TransferResponse struct {
	ID             string `json:"id"`
	Entity         string `json:"entity"`
	Source         string `json:"source"`
	Recipient      string `json:"recipient"`
	Account        string `json:"account"`
	Amount         int64  `json:"amount"`
	Currency       string `json:"currency"`
	Status         string `json:"status"`
	AmountReversed int64  `json:"amount_reversed"`
	CreatedAt      int64  `json:"created_at"`
}

// ReversalResponse is a Razorpay Route reversal (reverse_transfer) DTO.
// id is typically rvrsl_…; transfer_id links back to the forward transfer.
type ReversalResponse struct {
	ID               string `json:"id"`
	Entity           string `json:"entity"`
	TransferID       string `json:"transfer_id"`
	Amount           int64  `json:"amount"`
	Currency         string `json:"currency"`
	CustomerRefundID string `json:"customer_refund_id"`
	CreatedAt        int64  `json:"created_at"`
}

// SellerIDFromTransfer returns the Linked Account id (acc_…) preferring recipient,
// else account. Empty when neither is present — never invent a sentinel.
func SellerIDFromTransfer(t TransferResponse) string {
	if s := strings.TrimSpace(t.Recipient); s != "" {
		return s
	}
	return strings.TrimSpace(t.Account)
}

// SellerIDFromTransfers returns the first non-empty seller id across transfers.
func SellerIDFromTransfers(items []TransferResponse) string {
	for _, t := range items {
		if s := SellerIDFromTransfer(t); s != "" {
			return s
		}
	}
	return ""
}

// ListTransfersForPayment calls GET /payments/{id}/transfers.
func (c *Client) ListTransfersForPayment(ctx context.Context, paymentID string) ([]TransferResponse, error) {
	paymentID = strings.TrimSpace(paymentID)
	if paymentID == "" {
		return nil, nil
	}
	var result ListResponse[TransferResponse]
	if err := c.do(ctx, http.MethodGet, "/payments/"+paymentID+"/transfers", nil, &result); err != nil {
		return nil, err
	}
	return result.Items, nil
}

// ListReversalsForTransfer calls GET /transfers/{id}/reversals.
func (c *Client) ListReversalsForTransfer(ctx context.Context, transferID string) ([]ReversalResponse, error) {
	transferID = strings.TrimSpace(transferID)
	if transferID == "" {
		return nil, nil
	}
	var result ListResponse[ReversalResponse]
	if err := c.do(ctx, http.MethodGet, "/transfers/"+transferID+"/reversals", nil, &result); err != nil {
		return nil, err
	}
	return result.Items, nil
}

// TransferCreatedAt converts Razorpay unix created_at to UTC time.Time; zero if unset.
func TransferCreatedAt(unix int64) time.Time {
	if unix <= 0 {
		return time.Time{}
	}
	return time.Unix(unix, 0).UTC()
}
