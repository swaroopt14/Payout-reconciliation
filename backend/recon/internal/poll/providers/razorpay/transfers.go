package razorpay

import (
	"context"
	"net/http"
	"strings"
)

// TransferResponse is a Razorpay Route/transfer DTO for a payment.
// Create payloads use "account"; list/fetch responses commonly expose "recipient".
type TransferResponse struct {
	ID        string `json:"id"`
	Entity    string `json:"entity"`
	Source    string `json:"source"`
	Recipient string `json:"recipient"`
	Account   string `json:"account"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
	Status    string `json:"status"`
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
