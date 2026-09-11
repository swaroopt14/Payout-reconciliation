package recon

import (
	"strconv"
	"strings"
)

// Ask tools: structured answers that never collapse provider settlement into bank credit.

func GetTransactionProofAnswer(proof map[string]any) string {
	data, _ := proof["data"].(map[string]any)
	if data == nil {
		return "No proof record found."
	}
	summary, _ := data["proof_summary"].(map[string]any)
	msg, _ := data["message"].(string)
	if v, _ := summary["fully_reconciled"].(bool); v {
		return "Yes. Razorpay settlement reconciliation includes this payment and a matching bank credit was found, so the payment is fully reconciled. " + msg
	}
	if settled, _ := summary["provider_settled"].(string); settled == Proven {
		if bank, _ := summary["bank_credited"].(string); bank != Proven {
			return "Razorpay included this payment in settlement, but bank credit is not proven. " + msg
		}
	}
	if msg != "" {
		return msg
	}
	return "Evidence is insufficient."
}

func GetBankMatchAnswer(proof map[string]any) string {
	data, _ := proof["data"].(map[string]any)
	if data == nil {
		return "No proof record found."
	}
	summary, _ := data["proof_summary"].(map[string]any)
	bank, _ := summary["bank_credited"].(string)
	if bank == Proven {
		return "Yes. A bank statement row is matched to this settlement, so bank credit is proven."
	}
	msg, _ := data["message"].(string)
	if strings.TrimSpace(msg) == "" {
		msg = "Bank credit is not proven."
	}
	return "Did the money reach the bank? No matching bank observation is proven. " + msg
}

func formatInt(v int64) string {
	return strconv.FormatInt(v, 10)
}

func GetSettlementBreakdownAnswer(wf map[string]int64) string {
	return "Settlement waterfall uses recon lines only. Derived net is PSP arithmetic, not an independent expectation or bank credit. derived_net=" + formatInt(wf["derived_net"])
}
