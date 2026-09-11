package recon

func SettlementNetMinor(lines []SettlementLine, settlementID string) int64 {
	var gross, refunds, transfers, fees, taxes, adjustments int64
	var creditSum, debitSum int64
	hasCredit := false
	for _, l := range lines {
		if l.SettlementID != settlementID {
			continue
		}
		creditSum += l.CreditMinor
		debitSum += l.DebitMinor
		if l.CreditMinor != 0 {
			hasCredit = true
		}
		switch l.LineType {
		case "refund":
			refunds += absInt(l.AmountMinor)
			fees += l.FeeMinor
			taxes += l.TaxMinor
		case "transfer":
			transfers += absInt(l.AmountMinor)
		case "adjustment":
			adjustments += l.CreditMinor - l.DebitMinor
			if l.CreditMinor == 0 && l.DebitMinor == 0 {
				adjustments += l.AmountMinor
			}
		default:
			gross += l.AmountMinor
			fees += l.FeeMinor
			taxes += l.TaxMinor
		}
	}
	if hasCredit {
		net := creditSum - debitSum
		for _, l := range lines {
			if l.SettlementID != settlementID {
				continue
			}
			if l.CreditMinor != 0 || l.DebitMinor != 0 {
				continue
			}
			switch l.LineType {
			case "refund":
				net -= absInt(l.AmountMinor)
			case "transfer":
				net -= absInt(l.AmountMinor)
			case "adjustment":
				net += l.AmountMinor
			}
		}
		return net
	}
	return gross - refunds - transfers - fees - taxes + adjustments
}

func Waterfall(lines []SettlementLine, settlementID string) map[string]int64 {
	out := map[string]int64{
		"gross": 0, "refunds": 0, "transfers": 0, "fees": 0, "taxes": 0, "adjustments": 0,
	}
	for _, l := range lines {
		if l.SettlementID != settlementID {
			continue
		}
		switch l.LineType {
		case "refund":
			out["refunds"] += absInt(l.AmountMinor)
			out["fees"] += l.FeeMinor
			out["taxes"] += l.TaxMinor
		case "transfer":
			out["transfers"] += absInt(l.AmountMinor)
		case "adjustment":
			adj := l.CreditMinor - l.DebitMinor
			if adj == 0 {
				adj = l.AmountMinor
			}
			out["adjustments"] += adj
		default:
			out["gross"] += l.AmountMinor
			out["fees"] += l.FeeMinor
			out["taxes"] += l.TaxMinor
		}
	}
	net := SettlementNetMinor(lines, settlementID)
	out["derived_net"] = net
	out["expected_net"] = net
	return out
}

func absInt(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func PaymentStateOf(p PaymentObs) string {
	s := p.Status
	switch s {
	case PaymentCaptured, PaymentAuthorized, PaymentFailed, PaymentRefunded, PaymentPartiallyRefunded, PaymentCreated:
		return s
	}
	if p.Captured {
		return PaymentCaptured
	}
	if s == "" {
		return PaymentUnknown
	}
	return s
}
