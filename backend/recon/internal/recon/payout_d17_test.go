package recon

import (
	"context"
	"testing"

	"zord-outcome-engine/internal/poll/providers/razorpay"
)

func processedPayout(utr string) PayoutFact {
	return PayoutFact{
		ID: "cpo_1", PayoutID: "pout_D17TEST", ProviderStatus: razorpay.PayoutProcessed,
		AmountMinor: 50000, Currency: "INR", UTR: utr,
	}
}

// TestPayoutMatch_AmountOnlyDebitIsAmbiguousNotMatched guards D17/L4: a
// single same-amount debit with no UTR/reference is AMBIGUOUS, never MATCHED.
func TestPayoutMatch_AmountOnlyDebitIsAmbiguousNotMatched(t *testing.T) {
	for _, utr := range []string{"", "PUTR-OTHER"} {
		fr := ReconcilePayout(PayoutInput{
			Payout: processedPayout(utr),
			Banks:  []BankTxn{{ID: "b1", DebitMinor: 50000, CreditDebit: "DEBIT", Currency: "INR", Description: "NEFT TRANSFER"}},
		})
		if fr.Result == ResultMatched {
			t.Fatalf("utr=%q: amount-only debit MATCHED: %+v", utr, fr)
		}
		if fr.Result != ResultAmbiguous || fr.Reason != ReasonAmountOnlyNoReference {
			t.Fatalf("utr=%q: got %s/%s, want AMBIGUOUS/%s", utr, fr.Result, fr.Reason, ReasonAmountOnlyNoReference)
		}
		if len(fr.CandidateIDs) != 1 || fr.CandidateIDs[0] != "b1" || fr.BankCreditProven {
			t.Fatalf("utr=%q: candidates=%v proven=%v", utr, fr.CandidateIDs, fr.BankCreditProven)
		}
	}
	// Two same-amount debits, no reference: still AMBIGUOUS.
	fr := ReconcilePayout(PayoutInput{
		Payout: processedPayout(""),
		Banks: []BankTxn{
			{ID: "b1", DebitMinor: 50000, CreditDebit: "DEBIT", Currency: "INR"},
			{ID: "b2", DebitMinor: 50000, CreditDebit: "DEBIT", Currency: "INR"},
		},
	})
	if fr.Result != ResultAmbiguous {
		t.Fatalf("two amount-only candidates: %s/%s", fr.Result, fr.Reason)
	}
}

func TestPayoutMatch_ReferencePlusAmountMatched(t *testing.T) {
	byUTR := ReconcilePayout(PayoutInput{
		Payout: processedPayout("PUTR-1"),
		Banks: []BankTxn{
			{ID: "b1", UTR: "putr-1", DebitMinor: 50000, CreditDebit: "DEBIT", Currency: "INR"},
			{ID: "b2", DebitMinor: 50000, CreditDebit: "DEBIT", Currency: "INR"}, // same amount, no ref
		},
	})
	if byUTR.Result != ResultMatched || byUTR.Reason != "processed_exact_debit" || byUTR.EvidenceRefs.BankObservationID != "b1" {
		t.Fatalf("UTR+amount: %+v", byUTR)
	}
	byRef := ReconcilePayout(PayoutInput{
		Payout: processedPayout(""),
		Banks:  []BankTxn{{ID: "b3", DebitMinor: 50000, CreditDebit: "DEBIT", Currency: "INR", Description: "RAZORPAYX POUT_D17TEST"}},
	})
	if byRef.Result != ResultMatched || byRef.EvidenceRefs.BankObservationID != "b3" {
		t.Fatalf("reference+amount: %+v", byRef)
	}
	// Reference but different amount is VARIANCE, not MATCHED.
	mismatch := ReconcilePayout(PayoutInput{
		Payout: processedPayout("PUTR-1"),
		Banks:  []BankTxn{{ID: "b1", UTR: "PUTR-1", DebitMinor: 49999, CreditDebit: "DEBIT", Currency: "INR"}},
	})
	if mismatch.Result != ResultVariance {
		t.Fatalf("UTR with wrong amount: %s/%s", mismatch.Result, mismatch.Reason)
	}
}

func TestPayoutMatch_NoCandidateUnresolved(t *testing.T) {
	fr := ReconcilePayout(PayoutInput{
		Payout: processedPayout("PUTR-1"),
		Banks:  []BankTxn{{ID: "b1", DebitMinor: 123, CreditDebit: "DEBIT", Currency: "INR"}},
	})
	if fr.Result != ResultUnresolved || fr.Reason != "payout_missing_bank" {
		t.Fatalf("got %s/%s", fr.Result, fr.Reason)
	}
	fr = ReconcilePayout(PayoutInput{Payout: processedPayout("")})
	if fr.Result != ResultUnresolved {
		t.Fatalf("no banks: got %s/%s", fr.Result, fr.Reason)
	}
}

// TestFinancialRun_AmountOnlyPayoutDebitNotMatched covers the financial
// service path: relatedPayoutBanks (financial_service.go) still gathers
// same-amount debits as candidates, but the run must not mark them MATCHED.
func TestFinancialRun_AmountOnlyPayoutDebitNotMatched(t *testing.T) {
	store := NewMemoryFinancialStore()
	store.Payouts = []PayoutFact{processedPayout("")}
	store.Banks = []BankTxn{{ID: "b1", DebitMinor: 50000, CreditDebit: "DEBIT", Currency: "INR"}}
	svc := NewFinancialService(store)
	_, results, err := svc.Run(context.Background(), FinancialRunRequest{
		TenantID: "11111111-1111-1111-1111-111111111111", ConnectorID: "c",
	})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range results {
		if r.EntityType != EntityPayout {
			continue
		}
		found = true
		if r.Result == ResultMatched {
			t.Fatalf("amount-only payout debit MATCHED via financial run: %+v", r)
		}
		if r.Result != ResultAmbiguous || r.Reason != ReasonAmountOnlyNoReference {
			t.Fatalf("got %s/%s, want AMBIGUOUS/%s", r.Result, r.Reason, ReasonAmountOnlyNoReference)
		}
	}
	if !found {
		t.Fatal("no payout result")
	}
}
