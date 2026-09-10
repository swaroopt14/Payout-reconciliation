package recon

type BatchClose struct {
	BatchID          string         `json:"batch_id"`
	RunID            string         `json:"run_id"`
	Records          int            `json:"records"`
	Requested        int            `json:"requested"`
	Unlinked         int            `json:"unlinked"`
	Counts           map[string]int `json:"counts"`
	ExpectedMinor    int64          `json:"expected_minor"`
	BankDebitedMinor int64          `json:"bank_debited_minor"`
	ExposureMinor    int64          `json:"exposure_minor"`
	MatchRate        float64        `json:"match_rate"`
	Currency         string         `json:"currency"`
	RuleVersion      string         `json:"rule_version"`
}

func BatchCloseFromResults(batchID, runID string, results []FinancialResult, requested int) BatchClose {
	out := BatchClose{
		BatchID:     batchID,
		RunID:       runID,
		Requested:   requested,
		Counts:      map[string]int{},
		Currency:    "INR",
		RuleVersion: FinancialRuleVersion,
	}
	found := map[string]struct{}{}
	for _, r := range results {
		if r.EntityType != EntityPayout {
			continue
		}
		found[r.EntityID] = struct{}{}
		out.Records++
		out.Counts[r.Result]++
		out.ExpectedMinor += r.ExpectedAmount
		if r.BankCreditProven || r.Reason == "processed_exact_debit" {
			out.BankDebitedMinor += r.ObservedAmount
		}
		if r.Exception != nil {
			out.ExposureMinor += r.VarianceAmount
		} else if r.Result != ResultMatched {
			out.ExposureMinor += r.VarianceAmount
		}
	}
	if requested > out.Records {
		out.Unlinked = requested - out.Records
	}
	matched := out.Counts[ResultMatched]
	if out.Records > 0 {
		out.MatchRate = float64(matched) / float64(out.Records)
	}
	return out
}
