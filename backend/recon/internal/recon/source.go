package recon

import "time"

// Source kinds are open. Adding a fourth kind (merchant books, tax, dispute)
// does not require new fields on FinancialInput rule functions.
const (
	SourceKindPayment    = "payment"
	SourceKindPayout     = "payout"
	SourceKindSettlement = "settlement"
	SourceKindBank       = "bank"
	SourceKindMerchant   = "merchant_books"
	SourceKindTax        = "tax_line"
	SourceKindRefund     = "refund"
	SourceKindDispute    = "dispute"
	SourceKindEvent      = "observation"
)

// SourceObservation is one fact from one source. Rules select by Kind.
type SourceObservation struct {
	Kind        string
	ID          string
	EntityID    string
	AmountMinor int64
	CreditMinor int64
	DebitMinor  int64
	FeeMinor    int64
	TaxMinor    int64
	Currency    string
	UTR         string
	Status      string
	Captured    bool
	ValueDate   time.Time
	BatchID     string
	InvoiceID   string
	OrderID     string
	PayoutID    string
	DueAt       time.Time
	PayloadHash string
	FactVersion int
}

// SourceRef is the additive evidence slot. Legacy EvidenceRefs fields stay.
type SourceRef struct {
	Kind   string `json:"kind"`
	ID     string `json:"id,omitempty"`
	Amount int64  `json:"amount_minor,omitempty"`
}

// LegSpec declares which source kinds form a recon layer.
type LegSpec struct {
	Name  string
	Kinds []string
}

func CollectLegSpecs() []LegSpec {
	return []LegSpec{
		{Name: "two_way", Kinds: []string{SourceKindMerchant, SourceKindPayment, SourceKindSettlement}},
		{Name: "three_way", Kinds: []string{SourceKindMerchant, SourceKindPayment, SourceKindSettlement, SourceKindBank, SourceKindTax}},
	}
}

func PayoutLegSpecs() []LegSpec {
	return []LegSpec{
		{Name: "two_way", Kinds: []string{SourceKindMerchant, SourceKindPayout}},
		{Name: "three_way", Kinds: []string{SourceKindMerchant, SourceKindPayout, SourceKindBank}},
	}
}

func SourcesOfKind(obs []SourceObservation, kind string) []SourceObservation {
	var out []SourceObservation
	for _, o := range obs {
		if o.Kind == kind {
			out = append(out, o)
		}
	}
	return out
}

func HasSourceKind(obs []SourceObservation, kind string) bool {
	return len(SourcesOfKind(obs, kind)) > 0
}
