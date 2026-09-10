package recon

import "strings"

const (
	DirectionInbound  = "INBOUND"
	DirectionOutbound = "OUTBOUND"
	DirectionBoth     = "BOTH"
)

const (
	RailUPI          = "UPI"
	RailCard         = "CARD"
	RailNetbanking   = "NETBANKING"
	RailWallet       = "WALLET"
	RailIMPS         = "IMPS"
	RailNEFT         = "NEFT"
	RailRTGS         = "RTGS"
	RailBankTransfer = "BANK_TRANSFER"
	RailEMI          = "EMI"
	RailPaylater     = "PAYLATER"
	RailUnknown      = "UNKNOWN"
)

const (
	KindCollect = "collect"
	KindPayout  = "payout"
	KindBoth    = "both"
)

// ReconLeg is one book-match layer. Top-level Result stays the close/eval oracle.
// Two-way = merchant books vs PSP books. Three-way = that plus bank cash movement.
type ReconLeg struct {
	Result     string  `json:"result"`
	Reason     string  `json:"reason"`
	Confidence float64 `json:"confidence,omitempty"`
}

// CashInstrument is a Razorpay-first rail/method catalog entry.
// Cards, UPI collect, wallets, netbanking are cash-in. Payout modes are cash-out.
// UPI/IMPS/NEFT/RTGS exist on both legs.
type CashInstrument struct {
	Rail        string   `json:"rail"`
	Kind        string   `json:"kind"`
	Direction   string   `json:"direction"`
	RazorpayIDs []string `json:"razorpay_ids"`
	CashIn      bool     `json:"cash_in"`
	CashOut     bool     `json:"cash_out"`
}

func RazorpayInstruments() []CashInstrument {
	return []CashInstrument{
		{Rail: RailUPI, Kind: KindBoth, Direction: DirectionBoth, RazorpayIDs: []string{"upi"}, CashIn: true, CashOut: true},
		{Rail: RailCard, Kind: KindCollect, Direction: DirectionInbound, RazorpayIDs: []string{"card", "debit", "credit"}, CashIn: true},
		{Rail: RailNetbanking, Kind: KindCollect, Direction: DirectionInbound, RazorpayIDs: []string{"netbanking"}, CashIn: true},
		{Rail: RailWallet, Kind: KindCollect, Direction: DirectionInbound, RazorpayIDs: []string{"wallet"}, CashIn: true},
		{Rail: RailEMI, Kind: KindCollect, Direction: DirectionInbound, RazorpayIDs: []string{"emi", "cardless_emi"}, CashIn: true},
		{Rail: RailPaylater, Kind: KindCollect, Direction: DirectionInbound, RazorpayIDs: []string{"paylater"}, CashIn: true},
		{Rail: RailBankTransfer, Kind: KindCollect, Direction: DirectionInbound, RazorpayIDs: []string{"bank_transfer", "emandate", "nach"}, CashIn: true},
		{Rail: RailIMPS, Kind: KindBoth, Direction: DirectionBoth, RazorpayIDs: []string{"imps"}, CashIn: true, CashOut: true},
		{Rail: RailNEFT, Kind: KindBoth, Direction: DirectionBoth, RazorpayIDs: []string{"neft"}, CashIn: true, CashOut: true},
		{Rail: RailRTGS, Kind: KindBoth, Direction: DirectionBoth, RazorpayIDs: []string{"rtgs"}, CashIn: true, CashOut: true},
	}
}

func NormalizeRail(raw string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "", "UNKNOWN":
		return ""
	case "UPI":
		return RailUPI
	case "CARD", "DEBIT", "CREDIT", "CREDITCARD", "DEBITCARD":
		return RailCard
	case "NETBANKING", "NB":
		return RailNetbanking
	case "WALLET":
		return RailWallet
	case "IMPS":
		return RailIMPS
	case "NEFT":
		return RailNEFT
	case "RTGS":
		return RailRTGS
	case "BANK_TRANSFER", "BANKTRANSFER":
		return RailBankTransfer
	case "EMI", "CARDLESS_EMI":
		return RailEMI
	case "PAYLATER":
		return RailPaylater
	default:
		return strings.ToUpper(strings.TrimSpace(raw))
	}
}

func DirectionForEntity(entityType string) string {
	switch entityType {
	case EntityPayout:
		return DirectionOutbound
	case EntityPayment, EntityBank:
		return DirectionInbound
	default:
		return ""
	}
}

// AnnotateCashFlow fills direction, rail (if known), and 2-way / 3-way legs.
// It never changes top-level Result, Reason, Confidence, or BankCreditProven.
func AnnotateCashFlow(fr *FinancialResult) {
	if fr == nil {
		return
	}
	if fr.Direction == "" {
		fr.Direction = DirectionForEntity(fr.EntityType)
	}
	if fr.Rail != "" {
		fr.Rail = NormalizeRail(fr.Rail)
	}
	if fr.Result == "" || fr.Reason == "not_run" {
		return
	}
	fr.TwoWay, fr.ThreeWay = deriveLegs(*fr)
}

func deriveLegs(fr FinancialResult) (ReconLeg, ReconLeg) {
	two := ReconLeg{Result: fr.Result, Reason: fr.Reason, Confidence: fr.Confidence}
	three := ReconLeg{Result: fr.Result, Reason: fr.Reason, Confidence: fr.Confidence}

	if pspBooksAgree(fr.Reason) {
		two = ReconLeg{
			Result:     ResultMatched,
			Reason:     twoWayReason(fr),
			Confidence: nonZeroConf(fr.Confidence, 0.85),
		}
	}

	if pspBooksAgree(fr.Reason) && bankMovementProven(fr) {
		three = ReconLeg{
			Result:     ResultMatched,
			Reason:     threeWayMatchedReason(fr),
			Confidence: nonZeroConf(fr.Confidence, 0.99),
		}
		return two, three
	}

	if pspBooksAgree(fr.Reason) && !bankMovementProven(fr) {
		three = ReconLeg{
			Result:     ResultUnresolved,
			Reason:     threeWayGapReason(fr),
			Confidence: nonZeroConf(fr.Confidence, 0.7),
		}
	}
	return two, three
}

func pspBooksAgree(reason string) bool {
	switch reason {
	case "captured_settlement_exact_bank",
		"captured_settlement_high_confidence_bank",
		"settlement_without_bank",
		"processed_exact_debit",
		"payout_missing_bank",
		"failed_no_money_movement",
		"failed_refund_no_bank_movement":
		return true
	default:
		return false
	}
}

func bankMovementProven(fr FinancialResult) bool {
	switch fr.Reason {
	case "captured_settlement_exact_bank", "processed_exact_debit":
		return true
	case "failed_no_money_movement", "failed_refund_no_bank_movement":
		return true
	default:
		return fr.BankCreditProven
	}
}

func twoWayReason(fr FinancialResult) string {
	switch fr.EntityType {
	case EntityPayout:
		if fr.Reason == "failed_no_money_movement" || fr.Reason == "failed_refund_no_bank_movement" {
			return "merchant_psp_no_movement"
		}
		return "merchant_psp_payout"
	default:
		if fr.Reason == "failed_no_money_movement" || fr.Reason == "failed_refund_no_bank_movement" {
			return "merchant_psp_no_movement"
		}
		return "merchant_psp_settled"
	}
}

func threeWayMatchedReason(fr FinancialResult) string {
	switch fr.Reason {
	case "failed_no_money_movement", "failed_refund_no_bank_movement":
		return "merchant_psp_bank_no_movement"
	default:
		return "merchant_psp_bank"
	}
}

func threeWayGapReason(fr FinancialResult) string {
	switch fr.Reason {
	case "settlement_without_bank", "payout_missing_bank":
		return fr.Reason
	default:
		return "bank_not_proven"
	}
}

func nonZeroConf(got, fallback float64) float64 {
	if got > 0 {
		return got
	}
	return fallback
}
