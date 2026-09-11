package recon

import "testing"

func TestSettlementNetUsesCreditMinusDebit(t *testing.T) {
	lines := []SettlementLine{
		{SettlementID: "s", LineType: "payment", AmountMinor: 100000, CreditMinor: 96578, FeeMinor: 2900, TaxMinor: 522},
		{SettlementID: "s", LineType: "refund", AmountMinor: 1000},
	}
	got := SettlementNetMinor(lines, "s")
	if got != 95578 {
		t.Fatalf("net=%d", got)
	}
	wf := Waterfall(lines, "s")
	if wf["refunds"] != 1000 || wf["derived_net"] != 95578 || wf["expected_net"] != 95578 {
		t.Fatalf("%v", wf)
	}
}

func TestSettlementNetWithoutCreditUsesAmountMinusFeeTax(t *testing.T) {
	lines := []SettlementLine{
		{SettlementID: "s", LineType: "payment", AmountMinor: 1000, FeeMinor: 10, TaxMinor: 2},
		{SettlementID: "s", LineType: "refund", AmountMinor: 100},
	}
	if SettlementNetMinor(lines, "s") != 888 {
		t.Fatalf("net=%d", SettlementNetMinor(lines, "s"))
	}
}
