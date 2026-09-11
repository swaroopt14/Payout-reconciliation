package recon

import (
	"strings"
	"time"

	"zord-outcome-engine/internal/poll/providers/razorpay"
)

func ReconcilePayout(in PayoutInput) FinancialResult {
	out := reconcilePayout(in)
	if out.Rail == "" {
		out.Rail = NormalizeRail(in.Payout.Mode)
	}
	AnnotateCashFlow(&out)
	if out.RuleVersion == "" {
		out.RuleVersion = FinancialRuleVersion
	}
	return out
}

func reconcilePayout(in PayoutInput) FinancialResult {
	p := in.Payout
	status := razorpay.NormalizePayoutStatus(p.ProviderStatus)
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	sla := in.StuckAfter
	if sla <= 0 {
		sla = DefaultPayoutSLA
	}
	out := FinancialResult{
		EntityType:     EntityPayout,
		EntityID:       p.PayoutID,
		Status:         status,
		ExpectedAmount: p.AmountMinor,
		EvidenceRefs: EvidenceRefs{
			CanonicalPaymentID: p.ID,
			PaymentAmountMinor: p.AmountMinor,
		},
	}
	for _, ev := range in.Events {
		if ev.SourceEventID != "" {
			out.EvidenceRefs.ObservationEventIDs = append(out.EvidenceRefs.ObservationEventIDs, ev.SourceEventID)
		}
		if ev.SourceHash != "" {
			out.EvidenceRefs.PayloadHashes = append(out.EvidenceRefs.PayloadHashes, ev.SourceHash)
		}
	}
	debits := debitBanks(in.Banks)
	moved := HasBankMovement(in.Banks)
	exact := exactDebit(p, debits)

	switch {
	case razorpay.IsPayoutFailedLike(status):
		if moved {
			amt := BankMovementMinor(in.Banks)
			out.ObservedAmount = amt
			out.VarianceAmount = amt
			return withException(out, ResultUnresolved, "payout_failed_with_bank_movement", 0.9)
		}
		out.Result = ResultMatched
		out.Reason = "failed_no_money_movement"
		out.Confidence = 0.95
		out.BankCreditProven = false
		return out
	case razorpay.IsPayoutProcessed(status):
		if currencySidesConflict(p.Currency, bankCurrencies(in.Banks)) {
			if exact != nil {
				out.ObservedAmount = exact.DebitMinor
				out.EvidenceRefs.BankObservationID = exact.ID
			}
			return withException(out, ResultVariance, "currency_mismatch", 0.95)
		}
		if exact != nil {
			out.Result = ResultMatched
			out.Reason = "processed_exact_debit"
			out.Confidence = 0.99
			out.ObservedAmount = exact.DebitMinor
			out.EvidenceRefs.BankObservationID = exact.ID
			out.EvidenceRefs.BankCreditMinor = exact.DebitMinor
			return out
		}
		if mismatch := utrAmountMismatch(p, debits); mismatch != nil {
			out.ObservedAmount = mismatch.DebitMinor
			out.VarianceAmount = p.AmountMinor - mismatch.DebitMinor
			out.EvidenceRefs.BankObservationID = mismatch.ID
			out.EvidenceRefs.BankCreditMinor = mismatch.DebitMinor
			return withException(out, ResultVariance, "amount_mismatch", 0.9)
		}
		if len(debits) > 1 {
			ids := bankIDs(debits)
			out.CandidateIDs = ids
			return withException(out, ResultAmbiguous, "ambiguous_bank_candidates", 0.5)
		}
		return withException(out, ResultUnresolved, "payout_missing_bank", 0.8)
	case status == razorpay.PayoutReversed:
		if !moved {
			return withException(out, ResultUnresolved, "payout_reversed_unexplained", 0.7)
		}
		out.ObservedAmount = BankMovementMinor(in.Banks)
		out.VarianceAmount = p.AmountMinor - out.ObservedAmount
		if out.VarianceAmount != 0 {
			return withException(out, ResultVariance, "amount_mismatch", 0.7)
		}
		return withException(out, ResultUnresolved, "payout_reversed_unexplained", 0.6)
	case razorpay.IsPayoutOpen(status):
		age := now.Sub(p.ProviderCreatedAt)
		if p.ProviderCreatedAt.IsZero() {
			age = now.Sub(p.FirstObservedAt)
		}
		if age >= sla {
			return withException(out, ResultUnresolved, "payout_open_past_sla", 0.85)
		}
		out.Result = ResultUnresolved
		out.Reason = "payout_open"
		out.Confidence = 0.3
		return out
	default:
		out.Result = ResultUnresolved
		out.Reason = "insufficient_evidence"
		out.Confidence = 0.2
		return out
	}
}

func debitBanks(banks []BankTxn) []BankTxn {
	var out []BankTxn
	for _, b := range banks {
		if strings.EqualFold(b.CreditDebit, "CREDIT") {
			continue
		}
		if b.DebitMinor > 0 || strings.EqualFold(b.CreditDebit, "DEBIT") {
			out = append(out, b)
		}
	}
	return out
}

func exactDebit(p PayoutFact, debits []BankTxn) *BankTxn {
	utr := strings.ToUpper(strings.TrimSpace(p.UTR))
	var utrHits []BankTxn
	if utr != "" {
		for _, b := range debits {
			if strings.ToUpper(strings.TrimSpace(b.UTR)) == utr || strings.ToUpper(strings.TrimSpace(b.UTRRaw)) == utr {
				utrHits = append(utrHits, b)
			}
		}
		if len(utrHits) == 1 && utrHits[0].DebitMinor == p.AmountMinor {
			hit := utrHits[0]
			return &hit
		}
	}
	var amt []BankTxn
	for _, b := range debits {
		if b.DebitMinor == p.AmountMinor && (b.Currency == "" || p.Currency == "" || strings.EqualFold(b.Currency, p.Currency)) {
			amt = append(amt, b)
		}
	}
	if len(amt) == 1 {
		hit := amt[0]
		return &hit
	}
	return nil
}

func utrAmountMismatch(p PayoutFact, debits []BankTxn) *BankTxn {
	utr := strings.ToUpper(strings.TrimSpace(p.UTR))
	if utr == "" {
		return nil
	}
	var hits []BankTxn
	for _, b := range debits {
		if strings.ToUpper(strings.TrimSpace(b.UTR)) == utr || strings.ToUpper(strings.TrimSpace(b.UTRRaw)) == utr {
			hits = append(hits, b)
		}
	}
	if len(hits) == 1 && hits[0].DebitMinor != p.AmountMinor {
		hit := hits[0]
		return &hit
	}
	return nil
}
