package dataset

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"zord-outcome-engine/internal/recon"
	"zord-outcome-engine/internal/recon/eval"

	"github.com/google/uuid"
)

type SeedConfig struct {
	TenantID    string
	ConnectorID string
	BatchID     string
	AccountID   string
	Profile     string
	Limit       int
	Truncate    bool
}

type SeedResult struct {
	BatchID     string `json:"batch_id"`
	TenantID    string `json:"tenant_id"`
	ConnectorID string `json:"connector_id"`
	Records     int    `json:"records"`
	Clean       int    `json:"clean"`
	Exceptions  int    `json:"exceptions"`
	GroundTruth int    `json:"ground_truth"`
}

func Seed(ctx context.Context, db *sql.DB, cfg SeedConfig) (SeedResult, error) {
	if cfg.TenantID == "" {
		cfg.TenantID = uuid.Must(uuid.NewV7()).String()
	}
	if cfg.ConnectorID == "" {
		cfg.ConnectorID = uuid.Must(uuid.NewV7()).String()
	}
	if cfg.BatchID == "" {
		cfg.BatchID = fmt.Sprintf("batch_%d", time.Now().Unix())
	}
	if cfg.AccountID == "" {
		cfg.AccountID = "demo-acc-1"
	}
	if cfg.Profile == "" {
		cfg.Profile = "realistic"
	}

	cases := SelectCases(cfg.Profile, cfg.Limit)
	if len(cases) == 0 {
		return SeedResult{}, fmt.Errorf("no cases selected")
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return SeedResult{}, err
	}
	defer tx.Rollback()

	if cfg.Truncate {
		if err := truncateTenant(ctx, tx, cfg.TenantID, cfg.ConnectorID); err != nil {
			return SeedResult{}, err
		}
	}

	clean, exc := 0, 0
	for i, c := range cases {
		entityID, entityType, err := insertCase(ctx, tx, cfg, c, i)
		if err != nil {
			return SeedResult{}, fmt.Errorf("%s: %w", c.ID, err)
		}
		if err := insertTruth(ctx, tx, cfg, c, entityType, entityID); err != nil {
			return SeedResult{}, err
		}
		if c.Oracle.Exception {
			exc++
		} else {
			clean++
		}
	}

	if err := tx.Commit(); err != nil {
		return SeedResult{}, err
	}
	return SeedResult{
		BatchID:     cfg.BatchID,
		TenantID:    cfg.TenantID,
		ConnectorID: cfg.ConnectorID,
		Records:     len(cases),
		Clean:       clean,
		Exceptions:  exc,
		GroundTruth: len(cases),
	}, nil
}

func truncateTenant(ctx context.Context, tx *sql.Tx, tenantID, connectorID string) error {
	tables := []string{
		"synthetic_ground_truth",
		"finance_close_runs",
		"reconciliation_exceptions",
		"reconciliation_results",
		"reconciliation_runs",
		"investigation_records",
		"settlement_bank_match_decisions",
		"provider_settlement_line_observations",
		"bank_transaction_observations",
		"canonical_payouts",
		"canonical_payments",
	}
	for _, table := range tables {
		q := fmt.Sprintf("DELETE FROM %s WHERE tenant_id=$1 AND connector_id=$2", table)
		if _, err := tx.ExecContext(ctx, q, tenantID, connectorID); err != nil {
			return err
		}
	}
	return nil
}

func insertCase(ctx context.Context, tx *sql.Tx, cfg SeedConfig, c eval.Case, idx int) (entityID, entityType string, err error) {
	switch c.Kind {
	case eval.KindPayment:
		p := c.Payment.Payment
		entityID = scopedID(cfg.BatchID, p.PaymentID, idx)
		entityType = recon.EntityPayment
		cpID := uuid.Must(uuid.NewV7())
		status := p.CanonicalStatus
		if status == "" {
			status = p.ProviderStatus
		}
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO canonical_payments (
				id, tenant_id, connector_id, provider, payment_id, amount_minor, currency,
				provider_status, canonical_status, captured, fee_minor, tax_minor, sources, batch_id
			) VALUES ($1,$2,$3,'razorpay',$4,$5,$6,$7,$8,$9,$10,$11,'{seed}',$12)`,
			cpID, cfg.TenantID, cfg.ConnectorID, entityID, p.AmountMinor, nz(c.Currency, "INR"),
			status, status, p.Captured, 0, 0, cfg.BatchID,
		); err != nil {
			return "", "", err
		}
		lineIDs := map[string]string{}
		for _, l := range c.Payment.Lines {
			dbID, err := insertSettlementLine(ctx, tx, cfg, entityID, l, idx)
			if err != nil {
				return "", "", err
			}
			key := l.ID
			if key == "" {
				key = l.EntityID
			}
			lineIDs[key] = dbID
		}
		bankIDs := map[string]string{}
		for _, b := range c.Payment.Banks {
			dbID, err := insertBank(ctx, tx, cfg, b, idx)
			if err != nil {
				return "", "", err
			}
			bankIDs[b.ID] = dbID
		}
		for _, d := range c.Payment.Decisions {
			lineRef := lineIDs[d.SettlementLineID]
			bankRef := bankIDs[d.BankObservationID]
			if err = insertDecision(ctx, tx, cfg, d, lineRef, bankRef); err != nil {
				return "", "", err
			}
		}
		return entityID, entityType, nil
	case eval.KindPayout:
		po := c.Payout.Payout
		entityID = scopedID(cfg.BatchID, po.PayoutID, idx)
		entityType = recon.EntityPayout
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO canonical_payouts (
				id, tenant_id, connector_id, provider, payout_id, amount_minor, currency,
				provider_status, utr, mode, purpose, status_reason, batch_id
			) VALUES ($1,$2,$3,'razorpay',$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			uuid.Must(uuid.NewV7()), cfg.TenantID, cfg.ConnectorID, entityID, po.AmountMinor, nz(c.Currency, "INR"),
			po.ProviderStatus, po.UTR, po.Mode, po.Purpose, po.StatusReason, cfg.BatchID,
		); err != nil {
			return "", "", err
		}
		for _, b := range c.Payout.Banks {
			if _, err = insertBank(ctx, tx, cfg, b, idx); err != nil {
				return "", "", err
			}
		}
		return entityID, entityType, nil
	case eval.KindOrphan:
		entityID, err = insertBank(ctx, tx, cfg, c.Orphan, idx)
		if err != nil {
			return "", "", err
		}
		entityType = recon.EntityBank
		d := recon.SettlementBankDecision{
			State: recon.BankMatchOrphanBank, Confidence: 0.9, BankObservationID: entityID,
		}
		if err = insertDecision(ctx, tx, cfg, d, "", entityID); err != nil {
			return "", "", err
		}
		return entityID, entityType, nil
	default:
		return "", "", fmt.Errorf("unknown kind %s", c.Kind)
	}
}

func insertSettlementLine(ctx context.Context, tx *sql.Tx, cfg SeedConfig, paymentID string, l recon.SettlementLine, idx int) (string, error) {
	id := uuid.Must(uuid.NewV7()).String()
	lineKey := scopedID(cfg.BatchID, lineIDKey(l), idx)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO provider_settlement_line_observations (
			id, tenant_id, connector_id, provider, provider_mode, settlement_id, entity_id, line_type,
			payment_id, amount_minor, debit_minor, credit_minor, fee_minor, tax_minor, currency,
			settlement_utr, settled, settled_at, payload_hash, source, payment_link
		) VALUES ($1,$2,$3,'razorpay','test',$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,'seed','linked')`,
		id, cfg.TenantID, cfg.ConnectorID,
		nz(l.SettlementID, "setl_"+lineKey), nz(l.EntityID, paymentID), nz(l.LineType, "payment"), paymentID,
		l.AmountMinor, l.DebitMinor, l.CreditMinor, l.FeeMinor, l.TaxMinor, nz(l.Currency, "INR"),
		l.UTR, l.Settled, nullTime(l.SettledAt), hashOf(lineKey),
	)
	return id, err
}

func insertBank(ctx context.Context, tx *sql.Tx, cfg SeedConfig, b recon.BankTxn, idx int) (string, error) {
	id := uuid.Must(uuid.NewV7()).String()
	bankKey := scopedID(cfg.BatchID, b.ID, idx)
	cd := b.CreditDebit
	if cd == "" {
		if b.CreditMinor > 0 {
			cd = "CREDIT"
		} else {
			cd = "DEBIT"
		}
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO bank_transaction_observations (
			id, tenant_id, connector_id, account_id, description, credit_minor, debit_minor, currency,
			utr, credit_debit, source, row_hash, value_date
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'seed',$11,$12)`,
		id, cfg.TenantID, cfg.ConnectorID, cfg.AccountID,
		"seed "+bankKey, b.CreditMinor, b.DebitMinor, nz(b.Currency, "INR"), b.UTR, cd, hashOf(bankKey), nullTime(b.ValueDate),
	)
	return id, err
}

func insertDecision(ctx context.Context, tx *sql.Tx, cfg SeedConfig, d recon.SettlementBankDecision, lineID, bankID string) error {
	ev, _ := json.Marshal(d.Evidence)
	cands, _ := json.Marshal(d.Candidates)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO settlement_bank_match_decisions (
			id, tenant_id, connector_id, settlement_line_id, bank_observation_id, state, confidence, rule, candidates, evidence
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		uuid.Must(uuid.NewV7()), cfg.TenantID, cfg.ConnectorID,
		nullIfEmpty(lineID), nullIfEmpty(bankID), d.State, d.Confidence, nz(d.Rule, "seed"), string(cands), string(ev),
	)
	return err
}

func insertTruth(ctx context.Context, tx *sql.Tx, cfg SeedConfig, c eval.Case, entityType, entityID string) error {
	lab := c.Oracle
	_, err := tx.ExecContext(ctx, `
		INSERT INTO synthetic_ground_truth (
			id, tenant_id, connector_id, batch_id, entity_type, entity_id, family,
			expected_result, expected_reason, expected_exception, expected_variance, expected_bank_credit, amount_minor, currency
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		uuid.Must(uuid.NewV7()), cfg.TenantID, cfg.ConnectorID, cfg.BatchID,
		entityType, entityID, c.Family, lab.Result, lab.Reason, lab.Exception, lab.Variance, lab.BankCredit, c.Amount, nz(c.Currency, "INR"),
	)
	return err
}

func scopedID(batch, id string, idx int) string {
	base := strings.TrimSpace(id)
	if base == "" {
		base = fmt.Sprintf("row_%d", idx)
	}
	return fmt.Sprintf("%s__%s", batch, base)
}

func lineIDKey(l recon.SettlementLine) string {
	if l.ID != "" {
		return l.ID
	}
	return l.EntityID
}

func hashOf(s string) string {
	return "sha256:seed:" + s
}

func nz(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func nullIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}
