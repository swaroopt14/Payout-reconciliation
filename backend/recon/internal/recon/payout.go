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

	if in.Merchant != nil {
		out.MerchantObserved = true
		out.EvidenceRefs.MerchantFactID = in.Merchant.ID
	}
	if c := p.AmountConflictMinor; c != nil && *c != p.AmountMinor {
		gap := *c - p.AmountMinor
		if gap < 0 {
			gap = -gap
		}
		out.ObservedAmount = *c
		out.VarianceAmount = gap
		return withException(out, ResultVariance, ReasonProviderPayoutAmountChanged, 0.95)
	}
	if gap, reason, ok := payoutMerchantGap(p, in.Merchant); ok {
		out.MerchantAgreed = false
		out.ExpectedAmount = in.Merchant.AmountMinor
		out.ObservedAmount = p.AmountMinor
		out.VarianceAmount = gap
		return withException(out, ResultVariance, reason, 0.95)
	}
	if in.Merchant != nil {
		out.MerchantAgreed = true
	}

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
		// D17 / L4: a same-amount debit without a UTR or reference is
		// AMBIGUOUS at most — never MATCHED, even as the only candidate.
		if cands := amountOnlyDebits(p, debits); len(cands) == 1 {
			out.CandidateIDs = bankIDs(cands)
			out.ObservedAmount = cands[0].DebitMinor
			return withException(out, ResultAmbiguous, ReasonAmountOnlyNoReference, 0.5)
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

// ReasonAmountOnlyNoReference: a bank debit matches the payout amount but no
// UTR or bank/provider reference links it. AMBIGUOUS at most (D17).
const ReasonAmountOnlyNoReference = "amount_only_no_reference"

// payoutRefMatches reports whether a bank row carries a strong key for the
// payout: its UTR (UTR or raw UTR column) or the provider payout reference in
// the bank narration. Amount is never a key (D17).
func payoutRefMatches(p PayoutFact, b BankTxn) bool {
	if utr := strings.ToUpper(strings.TrimSpace(p.UTR)); utr != "" {
		if strings.ToUpper(strings.TrimSpace(b.UTR)) == utr || strings.ToUpper(strings.TrimSpace(b.UTRRaw)) == utr {
			return true
		}
	}
	if ref := strings.ToUpper(strings.TrimSpace(p.PayoutID)); len(ref) >= 6 && strings.Contains(strings.ToUpper(b.Description), ref) {
		return true
	}
	return false
}

// exactDebit returns the single debit linked to the payout by a strong key
// (UTR / reference) whose amount equals the payout amount. There is no
// amount-only fallback: MATCHED needs a strong key plus the amount (D17).
func exactDebit(p PayoutFact, debits []BankTxn) *BankTxn {
	var refHits []BankTxn
	for _, b := range debits {
		if payoutRefMatches(p, b) {
			refHits = append(refHits, b)
		}
	}
	if len(refHits) == 1 && refHits[0].DebitMinor == p.AmountMinor {
		hit := refHits[0]
		return &hit
	}
	return nil
}

// amountOnlyDebits returns debits with the payout's amount (and compatible
// currency) that carry no strong key for the payout.
func amountOnlyDebits(p PayoutFact, debits []BankTxn) []BankTxn {
	var out []BankTxn
	for _, b := range debits {
		if payoutRefMatches(p, b) {
			continue
		}
		if b.DebitMinor == p.AmountMinor && (b.Currency == "" || p.Currency == "" || strings.EqualFold(b.Currency, p.Currency)) {
			out = append(out, b)
		}
	}
	return out
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
