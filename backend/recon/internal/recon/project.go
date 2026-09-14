package recon

// ProjectFinancialInput maps an open observation list onto the existing
// engine input. A new source kind is appended to the slice; rule functions
// keep reading named fields.
func ProjectFinancialInput(obs []SourceObservation) FinancialInput {
	in := FinancialInput{}
	for _, o := range obs {
		switch o.Kind {
		case SourceKindPayment:
			in.Payment = PaymentFact{
				ID: o.ID, PaymentID: firstNonEmpty(o.EntityID, o.ID),
				CanonicalStatus: o.Status, ProviderStatus: o.Status,
				Captured: o.Captured, AmountMinor: o.AmountMinor, Currency: o.Currency,
				FeeMinor: o.FeeMinor, TaxMinor: o.TaxMinor, BatchID: o.BatchID,
			}
		case SourceKindPayout:
			// Payout is reconciled via ReconcileFromSources, not FinancialInput.
		case SourceKindSettlement:
			in.Lines = append(in.Lines, SettlementLine{
				ID: o.ID, PaymentID: o.EntityID, LineType: "payment",
				AmountMinor: o.AmountMinor, CreditMinor: o.CreditMinor, DebitMinor: o.DebitMinor,
				FeeMinor: o.FeeMinor, TaxMinor: o.TaxMinor, Currency: o.Currency, UTR: o.UTR,
				SettledAt: o.ValueDate, PayloadHash: o.PayloadHash,
			})
		case SourceKindBank:
			in.Banks = append(in.Banks, BankTxn{
				ID: o.ID, UTR: o.UTR, CreditMinor: o.CreditMinor, DebitMinor: o.DebitMinor,
				Currency: o.Currency, ValueDate: o.ValueDate, RowHash: o.PayloadHash,
			})
		case SourceKindMerchant:
			m := MerchantBookFact{
				ID: o.ID, InvoiceID: o.InvoiceID, OrderID: o.OrderID,
				PaymentID: o.EntityID, PayoutID: o.PayoutID, AmountMinor: o.AmountMinor,
				Currency: o.Currency, DueAt: o.DueAt, BatchID: o.BatchID, TaxMinor: o.TaxMinor,
			}
			in.Merchant = &m
		case SourceKindDispute:
			in.Disputes = append(in.Disputes, DisputeFact{
				ID: o.ID, PaymentID: o.EntityID, AmountMinor: o.AmountMinor,
				Currency: o.Currency, Status: o.Status,
			})
		case SourceKindRefund:
			in.Refunds = append(in.Refunds, RefundFact{
				ID: o.ID, RefundID: o.ID, PaymentID: o.EntityID,
				AmountMinor: o.AmountMinor, Currency: o.Currency, ProviderStatus: o.Status,
			})
		case SourceKindTax:
			in.TaxLines = append(in.TaxLines, TaxLineFact{
				ID: o.ID, PaymentID: o.EntityID, InvoiceID: o.InvoiceID,
				Component: o.Status, AmountMinor: o.AmountMinor, Currency: o.Currency, HSN: o.OrderID,
			})
		case SourceKindEvent:
			in.Events = append(in.Events, ObservationFact{
				SourceEventID: o.ID, SourceHash: o.PayloadHash,
				CanonicalStatus: o.Status, ProviderStatus: o.Status, ObservedAt: o.ValueDate,
			})
		}
	}
	return in
}

// ReconcileFromSources is the single entry for any number of source kinds.
func ReconcileFromSources(obs []SourceObservation, opts FinancialInput) FinancialResult {
	obs = PickLatestObservations(obs)
	in := ProjectFinancialInput(obs)
	if !opts.Now.IsZero() {
		in.Now = opts.Now
	}
	if opts.StuckAfter > 0 {
		in.StuckAfter = opts.StuckAfter
	}
	if len(opts.Decisions) > 0 {
		in.Decisions = opts.Decisions
	}
	if in.Merchant == nil {
		in.Merchant = opts.Merchant
	}
	if len(opts.Disputes) > 0 {
		in.Disputes = append(in.Disputes, opts.Disputes...)
	}
	if len(opts.TaxLines) > 0 {
		in.TaxLines = append(in.TaxLines, opts.TaxLines...)
	}
	if in.Payment.PaymentID != "" {
		return ReconcilePayment(in)
	}
	var pout PayoutFact
	for _, o := range obs {
		if o.Kind == SourceKindPayout {
			pout = PayoutFact{
				ID: o.ID, PayoutID: firstNonEmpty(o.EntityID, o.ID),
				ProviderStatus: o.Status, AmountMinor: o.AmountMinor,
				Currency: o.Currency, UTR: o.UTR, BatchID: o.BatchID,
			}
		}
	}
	if pout.PayoutID != "" {
		return ReconcilePayout(PayoutInput{
			Payout: pout, Events: in.Events, Banks: in.Banks,
			Now: in.Now, StuckAfter: in.StuckAfter, Merchant: in.Merchant,
		})
	}
	return FinancialResult{
		Result: ResultUnresolved, Reason: "insufficient_evidence",
		Confidence: 0.2, RuleVersion: FinancialRuleVersion,
	}
}

func ObservationsFromInput(in FinancialInput) []SourceObservation {
	var obs []SourceObservation
	if in.Payment.PaymentID != "" {
		obs = append(obs, SourceObservation{
			Kind: SourceKindPayment, ID: in.Payment.ID, EntityID: in.Payment.PaymentID,
			AmountMinor: in.Payment.AmountMinor, Currency: in.Payment.Currency,
			Status: in.Payment.CanonicalStatus, Captured: in.Payment.Captured,
			FeeMinor: in.Payment.FeeMinor, TaxMinor: in.Payment.TaxMinor, BatchID: in.Payment.BatchID,
		})
	}
	for _, d := range in.Disputes {
		obs = append(obs, SourceObservation{
			Kind: SourceKindDispute, ID: d.ID, EntityID: d.PaymentID,
			AmountMinor: d.AmountMinor, Currency: d.Currency, Status: d.Status,
		})
	}
	for _, l := range in.Lines {
		obs = append(obs, SourceObservation{
			Kind: SourceKindSettlement, ID: lineID(l), EntityID: l.PaymentID,
			AmountMinor: l.AmountMinor, CreditMinor: l.CreditMinor, DebitMinor: l.DebitMinor,
			FeeMinor: l.FeeMinor, TaxMinor: l.TaxMinor, Currency: l.Currency,
			UTR: l.UTR, ValueDate: l.SettledAt,
		})
	}
	for _, b := range in.Banks {
		obs = append(obs, SourceObservation{
			Kind: SourceKindBank, ID: b.ID, AmountMinor: b.CreditMinor,
			CreditMinor: b.CreditMinor, DebitMinor: b.DebitMinor,
			Currency: b.Currency, UTR: b.UTR, ValueDate: b.ValueDate,
		})
	}
	if in.Merchant != nil {
		obs = append(obs, SourceObservation{
			Kind: SourceKindMerchant, ID: in.Merchant.ID, EntityID: in.Merchant.PaymentID,
			AmountMinor: in.Merchant.AmountMinor, Currency: in.Merchant.Currency,
			InvoiceID: in.Merchant.InvoiceID, OrderID: in.Merchant.OrderID,
			PayoutID: in.Merchant.PayoutID, DueAt: in.Merchant.DueAt, BatchID: in.Merchant.BatchID,
			TaxMinor: in.Merchant.TaxMinor,
		})
		if gst, ok := invoiceGSTMinor(in.Merchant, nil); ok {
			obs = append(obs, SourceObservation{
				Kind: SourceKindTax, ID: in.Merchant.ID + ":gst", EntityID: in.Merchant.PaymentID,
				InvoiceID: in.Merchant.InvoiceID, AmountMinor: gst, Currency: in.Merchant.Currency,
				Status: "gst",
			})
		}
	}
	for _, tl := range in.TaxLines {
		obs = append(obs, SourceObservation{
			Kind: SourceKindTax, ID: tl.ID, EntityID: tl.PaymentID, InvoiceID: tl.InvoiceID,
			AmountMinor: tl.AmountMinor, Currency: tl.Currency, Status: tl.Component, OrderID: tl.HSN,
		})
	}
	return obs
}
