package recon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

type Store interface {
	InsertUpload(ctx context.Context, up BankUpload) (BankUpload, error)
	InsertBankTxns(ctx context.Context, tenantID, connectorID, uploadID string, rows []BankTxn) error
	ListBankTxns(ctx context.Context, tenantID, connectorID, accountID string) ([]BankTxn, error)
	ListPayments(ctx context.Context, tenantID, connectorID string) ([]PaymentObs, error)
	ListSettlementLines(ctx context.Context, tenantID, connectorID string) ([]SettlementLine, error)
	UpsertProof(ctx context.Context, sub ProofSubject) error
	InsertDecisions(ctx context.Context, tenantID, connectorID string, pairs []MatchDecision) error
	GetProof(ctx context.Context, tenantID, connectorID, paymentID string) (ProofSubject, error)
	ListProofs(ctx context.Context, tenantID, connectorID string) ([]ProofSubject, error)
	InsertLeaves(ctx context.Context, tenantID, connectorID string, leaves []EvidenceLeaf) error
	ListLeaves(ctx context.Context, tenantID, connectorID, paymentID string) ([]EvidenceLeaf, error)
}

type BankUpload struct {
	ID          string
	TenantID    string
	ConnectorID string
	AccountID   string
	Filename    string
	FileHash    string
	RowCount    int
	Status      string
	LastError   string
}

type Service struct {
	Store Store
	now   func() time.Time
}

func NewService(store Store) *Service {
	return &Service{Store: store, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) Run(ctx context.Context, tenantID, connectorID, accountID string) ([]ProofSubject, error) {
	pays, err := s.Store.ListPayments(ctx, tenantID, connectorID)
	if err != nil {
		return nil, err
	}
	lines, err := s.Store.ListSettlementLines(ctx, tenantID, connectorID)
	if err != nil {
		return nil, err
	}
	banks, err := s.Store.ListBankTxns(ctx, tenantID, connectorID, accountID)
	if err != nil {
		return nil, err
	}
	subjects := Match(Snapshot{
		TenantID: tenantID, ConnectorID: connectorID, AccountID: accountID,
		Payments: pays, Lines: lines, Banks: banks,
	})
	for i := range subjects {
		subjects[i].TenantID = tenantID
		subjects[i].ConnectorID = connectorID
		if subjects[i].PaymentID == "" && subjects[i].BankObservationID != "" {
			subjects[i].PaymentID = "unlinked-bank:" + subjects[i].BankObservationID
		}
		if err := s.Store.UpsertProof(ctx, subjects[i]); err != nil {
			return nil, err
		}
		if err := s.Store.InsertDecisions(ctx, tenantID, connectorID, subjects[i].MatchPairs); err != nil {
			return nil, err
		}
		leaves := LeavesFor(subjects[i], pays, lines, banks)
		if err := s.Store.InsertLeaves(ctx, tenantID, connectorID, leaves); err != nil {
			return nil, err
		}
	}
	return subjects, nil
}

func (s *Service) GetProof(ctx context.Context, tenantID, connectorID, paymentID string) (map[string]any, error) {
	sub, err := s.Store.GetProof(ctx, tenantID, connectorID, paymentID)
	if err != nil {
		return nil, err
	}
	leaves, _ := s.Store.ListLeaves(ctx, tenantID, connectorID, paymentID)
	return ProofJSON(sub, leaves), nil
}

func (s *Service) Summary(ctx context.Context, tenantID, connectorID string) (map[string]int, error) {
	list, err := s.Store.ListProofs(ctx, tenantID, connectorID)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{
		ReconFullyReconciled:                    0,
		ReconSettlementConfirmedBankPending:     0,
		ReconPaymentConfirmedSettlementPending:  0,
		ReconMissingWebhookRepairedByAPI:        0,
		ReconAmountMismatch:                     0,
		ReconAmbiguousMatch:                     0,
		ReconUnresolved:                         0,
		ReconBankCreditConfirmedProviderPending: 0,
	}
	for _, s := range list {
		counts[s.ReconciliationState]++
	}
	return counts, nil
}

func (s *Service) Gaps(ctx context.Context, tenantID, connectorID string) ([]ProofSubject, error) {
	list, err := s.Store.ListProofs(ctx, tenantID, connectorID)
	if err != nil {
		return nil, err
	}
	var gaps []ProofSubject
	for _, p := range list {
		if p.ReconciliationState != ReconFullyReconciled {
			gaps = append(gaps, p)
		}
	}
	return gaps, nil
}

func (s *Service) Breakdown(ctx context.Context, tenantID, connectorID, settlementID string) (map[string]int64, error) {
	lines, err := s.Store.ListSettlementLines(ctx, tenantID, connectorID)
	if err != nil {
		return nil, err
	}
	wf := Waterfall(lines, settlementID)
	proofs, _ := s.Store.ListProofs(ctx, tenantID, connectorID)
	for _, p := range proofs {
		if p.SettlementID == settlementID {
			wf["bank_credit"] = p.BankCreditMinor
			wf["difference"] = p.DifferenceMinor
			break
		}
	}
	return wf, nil
}

func (s *Service) VerifyEvidence(ctx context.Context, tenantID, connectorID, paymentID string) (map[string]any, error) {
	leaves, err := s.Store.ListLeaves(ctx, tenantID, connectorID, paymentID)
	if err != nil {
		return nil, err
	}
	ok := len(leaves) > 0
	for _, l := range leaves {
		if l.RawPayloadHash == "" {
			ok = false
		}
	}
	return map[string]any{
		"payment_id": paymentID,
		"leaf_count": len(leaves),
		"verified":   ok,
		"chain":      "raw_hash -> observation -> match_decision -> merkle_leaf",
		"message":    "Hash proves stored artifacts are unchanged. Cryptographic proof does not make a false provider record true.",
	}, nil
}

func ProofJSON(sub ProofSubject, leaves []EvidenceLeaf) map[string]any {
	captured := Unproven
	if sub.PaymentState == PaymentCaptured {
		captured = Proven
	}
	settled := Unproven
	if sub.ProviderSettlementState == SettlementSettled || sub.ProviderSettlementState == SettlementIncludedInRecon {
		settled = Proven
	}
	bank := Unproven
	if sub.BankCreditState == BankMatched {
		bank = Proven
	}
	overall := "unverified"
	if sub.ProofState == ProofVerified {
		overall = "verified"
	}
	msg := sub.Message
	if msg == "" && sub.ProofState == ProofVerified {
		msg = "Intent, payment, settlement, and bank credit agree within policy."
	}
	matchTypePay, matchTypeBank, conf := "", "", 0.0
	for _, m := range sub.MatchPairs {
		if m.MatchType == MatchExactPaymentID || m.MatchType == MatchExactEntityID {
			matchTypePay = m.MatchType
		}
		if m.RightSource == "bank_statement" {
			matchTypeBank = m.MatchType
			conf = m.Confidence
		}
	}
	return map[string]any{
		"data": map[string]any{
			"transaction_id": sub.PaymentID,
			"message":        msg,
			"states": map[string]string{
				"payment_state":             sub.PaymentState,
				"provider_settlement_state": sub.ProviderSettlementState,
				"bank_credit_state":         sub.BankCreditState,
				"reconciliation_state":      sub.ReconciliationState,
				"proof_state":               sub.ProofState,
			},
			"proof_summary": map[string]any{
				"overall":          overall,
				"payment_captured": captured,
				"provider_settled": settled,
				"bank_credited":    bank,
				"fully_reconciled": sub.ReconciliationState == ReconFullyReconciled,
			},
			"amounts": map[string]any{
				"derived_net":  sub.ExpectedNetMinor,
				"expected_net": sub.ExpectedNetMinor,
				"bank_credit":  sub.BankCreditMinor,
				"difference":   sub.DifferenceMinor,
				"currency":     sub.Currency,
			},
			"match": map[string]any{
				"settlement_to_payment": matchTypePay,
				"settlement_to_bank":    matchTypeBank,
				"confidence":            conf,
			},
			"evidence": map[string]any{
				"leaf_count":   len(leaves),
				"leaves":       leaves,
				"rule_version": RuleVersion,
			},
			"chips": map[string]bool{
				"intent":     hasMatch(sub, "intent"),
				"webhook":    !sub.MissingWebhook,
				"api":        true,
				"settlement": sub.SettlementID != "",
				"bank":       sub.BankCreditState == BankMatched,
				"verified":   sub.ProofState == ProofVerified,
			},
		},
	}
}

func hasMatch(sub ProofSubject, right string) bool {
	for _, m := range sub.MatchPairs {
		if m.RightSource == right {
			return true
		}
	}
	return false
}

func LeavesFor(sub ProofSubject, pays []PaymentObs, lines []SettlementLine, banks []BankTxn) []EvidenceLeaf {
	now := time.Now().UTC()
	var leaves []EvidenceLeaf
	for _, p := range pays {
		if p.PaymentID != sub.PaymentID {
			continue
		}
		src := p.Source
		if src == "" || src == "razorpay_api" {
			src = "api_backfill"
		}
		leaves = append(leaves, EvidenceLeaf{
			PaymentID: p.PaymentID, Source: src, SourceRecordID: p.PaymentID,
			RawPayloadHash: p.PayloadHash, ObservedAt: now,
		})
	}
	for _, l := range lines {
		if l.SettlementID == sub.SettlementID && (l.PaymentID == sub.PaymentID || l.EntityID == sub.PaymentID) {
			leaves = append(leaves, EvidenceLeaf{
				PaymentID: sub.PaymentID, Source: "razorpay_settlement_recon",
				SourceRecordID: l.SettlementID + ":" + l.EntityID,
				RawPayloadHash: l.PayloadHash, ObservedAt: now,
			})
		}
	}
	if sub.BankObservationID != "" {
		for _, b := range banks {
			if b.ID == sub.BankObservationID {
				leaves = append(leaves, EvidenceLeaf{
					PaymentID: sub.PaymentID, Source: "bank_statement",
					SourceRecordID: b.ID, RawPayloadHash: b.RowHash, ObservedAt: now,
				})
			}
		}
	}
	for _, m := range sub.MatchPairs {
		raw, _ := json.Marshal(m)
		sum := sha256.Sum256(raw)
		leaves = append(leaves, EvidenceLeaf{
			PaymentID: sub.PaymentID, Source: "recon_match_decision",
			SourceRecordID: m.MatchID, RawPayloadHash: "sha256:" + hex.EncodeToString(sum[:]), ObservedAt: now,
		})
	}
	return leaves
}
