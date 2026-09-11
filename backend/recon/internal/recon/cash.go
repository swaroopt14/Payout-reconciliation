package recon

import "time"

type CashSnapshot struct {
	GrossCapturedMinor           int64     `json:"gross_captured_minor"`
	SettlementExpectedNetMinor   int64     `json:"settlement_expected_net_minor"`
	SettlementDerivedNetMinor    int64     `json:"settlement_derived_net_minor"`
	BankCreditedProvenMinor      int64     `json:"bank_credited_proven_minor"`
	InFlightMinor                int64     `json:"in_flight_minor"`
	UnresolvedExposureMinor      int64     `json:"unresolved_exposure_minor"`
	Currency                     string    `json:"currency"`
	AsOf                         time.Time `json:"as_of"`
}

func CashPosition(results []FinancialResult, lines []SettlementLine, exceptions []ReconciliationException) CashSnapshot {
	out := CashSnapshot{Currency: "INR", AsOf: time.Now().UTC()}
	netByPayment := map[string]int64{}
	for _, l := range lines {
		pid := l.PaymentID
		if pid == "" {
			pid = l.EntityID
		}
		if pid == "" {
			continue
		}
		if l.LineType == "" || l.LineType == "payment" {
			netByPayment[pid] += lineNet(l)
		}
	}
	for _, r := range results {
		if r.EntityType != EntityPayment {
			continue
		}
		out.GrossCapturedMinor += r.ExpectedAmount
		if net, ok := netByPayment[r.EntityID]; ok {
			out.SettlementExpectedNetMinor += net
			out.SettlementDerivedNetMinor += net
		}
		if r.BankCreditProven {
			out.BankCreditedProvenMinor += r.ObservedAmount
		}
	}
	settledNotBank := out.SettlementExpectedNetMinor - out.BankCreditedProvenMinor
	if settledNotBank > 0 {
		out.InFlightMinor = settledNotBank
	}
	for _, ex := range exceptions {
		out.UnresolvedExposureMinor += ex.VarianceAmount
	}
	return out
}
