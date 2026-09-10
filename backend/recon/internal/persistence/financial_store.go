package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"zord-outcome-engine/internal/paymenttruth"
	"zord-outcome-engine/internal/recon"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

func (s *ReconSQLStore) ListCanonicalPayments(ctx context.Context, tenantID, connectorID string) ([]recon.PaymentFact, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, tenant_id::text, connector_id::text, provider, payment_id, COALESCE(order_id,''),
			amount_minor, currency, COALESCE(method,''), provider_status, canonical_status, captured,
			fee_minor, tax_minor, provider_created_at, captured_at, first_observed_at, last_observed_at,
			COALESCE(sources, '{}'), COALESCE(intent_id::text,''), intent_link
		FROM canonical_payments
		WHERE tenant_id=$1 AND connector_id=$2
		ORDER BY last_observed_at ASC`, tenantID, connectorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []recon.PaymentFact
	for rows.Next() {
		pay, err := scanCanonicalPayment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, toPaymentFact(pay))
	}
	return out, rows.Err()
}

func (s *ReconSQLStore) GetCanonicalPayment(ctx context.Context, tenantID, connectorID, paymentID string) (recon.PaymentFact, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id::text, tenant_id::text, connector_id::text, provider, payment_id, COALESCE(order_id,''),
			amount_minor, currency, COALESCE(method,''), provider_status, canonical_status, captured,
			fee_minor, tax_minor, provider_created_at, captured_at, first_observed_at, last_observed_at,
			COALESCE(sources, '{}'), COALESCE(intent_id::text,''), intent_link
		FROM canonical_payments
		WHERE tenant_id=$1 AND connector_id=$2 AND payment_id=$3`,
		tenantID, connectorID, paymentID)
	pay, err := scanCanonicalPayment(row)
	if errors.Is(err, sql.ErrNoRows) {
		return recon.PaymentFact{}, false, nil
	}
	if err != nil {
		return recon.PaymentFact{}, false, err
	}
	return toPaymentFact(pay), true, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanCanonicalPayment(row scanner) (paymenttruth.CanonicalPayment, error) {
	var pay paymenttruth.CanonicalPayment
	var providerCreated, capturedAt sql.NullTime
	var sources pq.StringArray
	err := row.Scan(
		&pay.ID, &pay.TenantID, &pay.ConnectorID, &pay.Provider, &pay.PaymentID, &pay.OrderID,
		&pay.AmountMinor, &pay.Currency, &pay.Method, &pay.ProviderStatus, &pay.CanonicalStatus, &pay.Captured,
		&pay.FeeMinor, &pay.TaxMinor, &providerCreated, &capturedAt, &pay.FirstObservedAt, &pay.LastObservedAt,
		&sources, &pay.IntentID, &pay.IntentLink,
	)
	if err != nil {
		return paymenttruth.CanonicalPayment{}, err
	}
	if providerCreated.Valid {
		pay.ProviderCreatedAt = providerCreated.Time
	}
	if capturedAt.Valid {
		pay.CapturedAt = capturedAt.Time
	}
	pay.Sources = []string(sources)
	return pay, nil
}

func (s *ReconSQLStore) ListObservationEvents(ctx context.Context, tenantID, connectorID, paymentID string) ([]recon.ObservationFact, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT COALESCE(source,''), COALESCE(provider_status,''), COALESCE(canonical_status,''),
			COALESCE(source_event_id,''), COALESCE(source_hash, payload_hash), COALESCE(raw_reference,''), observed_at
		FROM provider_payment_observation_events
		WHERE tenant_id=$1 AND connector_id=$2 AND payment_id=$3
		ORDER BY observed_at ASC`,
		tenantID, connectorID, paymentID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []recon.ObservationFact
	for rows.Next() {
		var obs recon.ObservationFact
		if err := rows.Scan(&obs.Source, &obs.ProviderStatus, &obs.CanonicalStatus, &obs.SourceEventID, &obs.SourceHash, &obs.RawReference, &obs.ObservedAt); err != nil {
			return nil, err
		}
		out = append(out, obs)
	}
	return out, rows.Err()
}

func toPaymentFact(pay paymenttruth.CanonicalPayment) recon.PaymentFact {
	return recon.PaymentFact{
		ID:                pay.ID,
		PaymentID:         pay.PaymentID,
		CanonicalStatus:   pay.CanonicalStatus,
		ProviderStatus:    pay.ProviderStatus,
		Captured:          pay.Captured,
		AmountMinor:       pay.AmountMinor,
		Currency:          pay.Currency,
		Method:            pay.Method,
		ProviderCreatedAt: pay.ProviderCreatedAt,
		FirstObservedAt:   pay.FirstObservedAt,
		Sources:           pay.Sources,
		FeeMinor:          pay.FeeMinor,
		TaxMinor:          pay.TaxMinor,
	}
}

func (s *ReconSQLStore) ListSettlementBankDecisions(ctx context.Context, tenantID, connectorID string) ([]recon.SettlementBankDecision, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, tenant_id::text, connector_id::text, COALESCE(settlement_line_id,''), COALESCE(bank_observation_id,''),
			state, confidence, rule, candidates, evidence, decided_at
		FROM settlement_bank_match_decisions
		WHERE tenant_id=$1 AND connector_id=$2
		ORDER BY decided_at ASC`, tenantID, connectorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []recon.SettlementBankDecision
	for rows.Next() {
		var d recon.SettlementBankDecision
		var cands, ev []byte
		if err := rows.Scan(&d.ID, &d.TenantID, &d.ConnectorID, &d.SettlementLineID, &d.BankObservationID,
			&d.State, &d.Confidence, &d.Rule, &cands, &ev, &d.DecidedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(cands, &d.Candidates)
		_ = json.Unmarshal(ev, &d.Evidence)
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *ReconSQLStore) InsertReconciliationRun(ctx context.Context, run recon.ReconciliationRun) (recon.ReconciliationRun, error) {
	if run.ID == "" {
		run.ID = uuid.Must(uuid.NewV7()).String()
	}
	if run.CreatedAt.IsZero() {
		run.CreatedAt = time.Now().UTC()
	}
	counts, _ := json.Marshal(run.Counts)
	if run.Counts == nil {
		counts = []byte("{}")
	}
	eids, _ := json.Marshal(run.EntityIDs)
	if run.EntityIDs == nil {
		eids = []byte("[]")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO reconciliation_runs (
			id, tenant_id, connector_id, account_id, status, payment_count, matched_count, exception_count, counts, created_at, batch_id, entity_ids
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		run.ID, run.TenantID, run.ConnectorID, run.AccountID, run.Status,
		run.PaymentCount, run.MatchedCount, run.ExceptionCount, counts, run.CreatedAt,
		run.BatchID, eids,
	)
	return run, err
}

func (s *ReconSQLStore) CompleteReconciliationRun(ctx context.Context, run recon.ReconciliationRun) error {
	counts, _ := json.Marshal(run.Counts)
	if run.Counts == nil {
		counts = []byte("{}")
	}
	eids, _ := json.Marshal(run.EntityIDs)
	if run.EntityIDs == nil {
		eids = []byte("[]")
	}
	completed := run.CompletedAt
	if completed.IsZero() {
		completed = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE reconciliation_runs
		SET status=$2, payment_count=$3, matched_count=$4, exception_count=$5, counts=$6, completed_at=$7, entity_ids=$8
		WHERE id=$1`,
		run.ID, run.Status, run.PaymentCount, run.MatchedCount, run.ExceptionCount, counts, completed, eids,
	)
	return err
}

func (s *ReconSQLStore) GetReconciliationRun(ctx context.Context, tenantID, runID string) (recon.ReconciliationRun, error) {
	var run recon.ReconciliationRun
	var counts, eids []byte
	var completed sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT id::text, tenant_id::text, connector_id::text, account_id, status,
			payment_count, matched_count, exception_count, counts, created_at, completed_at,
			COALESCE(batch_id,''), COALESCE(entity_ids, '[]'::jsonb)
		FROM reconciliation_runs WHERE id=$1 AND tenant_id=$2`, runID, tenantID,
	).Scan(&run.ID, &run.TenantID, &run.ConnectorID, &run.AccountID, &run.Status,
		&run.PaymentCount, &run.MatchedCount, &run.ExceptionCount, &counts, &run.CreatedAt, &completed,
		&run.BatchID, &eids)
	if err != nil {
		return recon.ReconciliationRun{}, err
	}
	_ = json.Unmarshal(counts, &run.Counts)
	_ = json.Unmarshal(eids, &run.EntityIDs)
	if completed.Valid {
		run.CompletedAt = completed.Time
	}
	return run, nil
}

func (s *ReconSQLStore) GetLatestReconciliationRunByBatch(ctx context.Context, tenantID, connectorID, batchID string) (recon.ReconciliationRun, bool, error) {
	var run recon.ReconciliationRun
	var counts, eids []byte
	var completed sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT id::text, tenant_id::text, connector_id::text, account_id, status,
			payment_count, matched_count, exception_count, counts, created_at, completed_at,
			COALESCE(batch_id,''), COALESCE(entity_ids, '[]'::jsonb)
		FROM reconciliation_runs
		WHERE tenant_id=$1 AND connector_id=$2 AND batch_id=$3
		ORDER BY created_at DESC LIMIT 1`, tenantID, connectorID, batchID,
	).Scan(&run.ID, &run.TenantID, &run.ConnectorID, &run.AccountID, &run.Status,
		&run.PaymentCount, &run.MatchedCount, &run.ExceptionCount, &counts, &run.CreatedAt, &completed,
		&run.BatchID, &eids)
	if errors.Is(err, sql.ErrNoRows) {
		return recon.ReconciliationRun{}, false, nil
	}
	if err != nil {
		return recon.ReconciliationRun{}, false, err
	}
	_ = json.Unmarshal(counts, &run.Counts)
	_ = json.Unmarshal(eids, &run.EntityIDs)
	if completed.Valid {
		run.CompletedAt = completed.Time
	}
	return run, true, nil
}

func (s *ReconSQLStore) UpsertReconciliationResult(ctx context.Context, tenantID, connectorID, runID string, r recon.FinancialResult) (recon.FinancialResult, error) {
	if r.ID == "" {
		r.ID = uuid.Must(uuid.NewV7()).String()
	}
	r.RunID = runID
	cands, _ := json.Marshal(r.CandidateIDs)
	if r.CandidateIDs == nil {
		cands = []byte("[]")
	}
	refs, _ := json.Marshal(r.EvidenceRefs)
	if refs == nil {
		refs = []byte("{}")
	}
	var run any
	if runID != "" {
		run = runID
	}
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO reconciliation_results (
			id, run_id, tenant_id, connector_id, entity_type, entity_id, status, result,
			expected_amount_minor, observed_amount_minor, variance_amount_minor, confidence, reason,
			candidate_ids, evidence_refs, bank_credit_proven, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,now(),now())
		ON CONFLICT (tenant_id, connector_id, entity_type, entity_id) DO UPDATE SET
			run_id=EXCLUDED.run_id, status=EXCLUDED.status, result=EXCLUDED.result,
			expected_amount_minor=EXCLUDED.expected_amount_minor,
			observed_amount_minor=EXCLUDED.observed_amount_minor,
			variance_amount_minor=EXCLUDED.variance_amount_minor,
			confidence=EXCLUDED.confidence, reason=EXCLUDED.reason,
			candidate_ids=EXCLUDED.candidate_ids, evidence_refs=EXCLUDED.evidence_refs,
			bank_credit_proven=EXCLUDED.bank_credit_proven, updated_at=now()
		RETURNING id::text`,
		r.ID, run, tenantID, connectorID, r.EntityType, r.EntityID, r.Status, r.Result,
		r.ExpectedAmount, r.ObservedAmount, r.VarianceAmount, r.Confidence, r.Reason,
		cands, refs, r.BankCreditProven,
	).Scan(&r.ID)
	if err != nil {
		return recon.FinancialResult{}, err
	}
	if r.Exception != nil {
		r.Exception.RunID = runID
		r.Exception.TenantID = tenantID
		r.Exception.ConnectorID = connectorID
	}
	return r, nil
}

func (s *ReconSQLStore) GetReconciliationResult(ctx context.Context, tenantID, connectorID, entityType, entityID string) (recon.FinancialResult, bool, error) {
	var r recon.FinancialResult
	var cands, refs []byte
	var runID sql.NullString
	var created sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT id::text, COALESCE(run_id::text,''), entity_type, entity_id, status, result,
			expected_amount_minor, observed_amount_minor, variance_amount_minor, confidence, reason,
			candidate_ids, evidence_refs, bank_credit_proven, created_at
		FROM reconciliation_results
		WHERE tenant_id=$1 AND connector_id=$2 AND entity_type=$3 AND entity_id=$4`,
		tenantID, connectorID, entityType, entityID,
	).Scan(&r.ID, &runID, &r.EntityType, &r.EntityID, &r.Status, &r.Result,
		&r.ExpectedAmount, &r.ObservedAmount, &r.VarianceAmount, &r.Confidence, &r.Reason,
		&cands, &refs, &r.BankCreditProven, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return recon.FinancialResult{}, false, nil
	}
	if err != nil {
		return recon.FinancialResult{}, false, err
	}
	r.RunID = runID.String
	_ = json.Unmarshal(cands, &r.CandidateIDs)
	_ = json.Unmarshal(refs, &r.EvidenceRefs)
	if created.Valid {
		r.CreatedAt = created.Time
	}
	return r, true, nil
}

func (s *ReconSQLStore) ListReconciliationResults(ctx context.Context, tenantID, connectorID string) ([]recon.FinancialResult, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, COALESCE(run_id::text,''), entity_type, entity_id, status, result,
			expected_amount_minor, observed_amount_minor, variance_amount_minor, confidence, reason,
			candidate_ids, evidence_refs, bank_credit_proven, created_at
		FROM reconciliation_results
		WHERE tenant_id=$1 AND connector_id=$2
		ORDER BY entity_type, entity_id`, tenantID, connectorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []recon.FinancialResult
	for rows.Next() {
		var r recon.FinancialResult
		var cands, refs []byte
		var runID sql.NullString
		var created sql.NullTime
		if err := rows.Scan(&r.ID, &runID, &r.EntityType, &r.EntityID, &r.Status, &r.Result,
			&r.ExpectedAmount, &r.ObservedAmount, &r.VarianceAmount, &r.Confidence, &r.Reason,
			&cands, &refs, &r.BankCreditProven, &created); err != nil {
			return nil, err
		}
		r.RunID = runID.String
		_ = json.Unmarshal(cands, &r.CandidateIDs)
		_ = json.Unmarshal(refs, &r.EvidenceRefs)
		if created.Valid {
			r.CreatedAt = created.Time
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *ReconSQLStore) InsertReconciliationException(ctx context.Context, tenantID, connectorID, runID string, ex recon.ReconciliationException) (recon.ReconciliationException, error) {
	if ex.ID == "" {
		ex.ID = uuid.Must(uuid.NewV7()).String()
	}
	ex.TenantID = tenantID
	ex.ConnectorID = connectorID
	ex.RunID = runID
	cands, _ := json.Marshal(ex.CandidateIDs)
	if ex.CandidateIDs == nil {
		cands = []byte("[]")
	}
	eids, _ := json.Marshal(ex.EvidenceIDs)
	if ex.EvidenceIDs == nil {
		eids = []byte("[]")
	}
	refs, _ := json.Marshal(ex.EvidenceRefs)
	if refs == nil {
		refs = []byte("{}")
	}
	var run any
	if runID != "" {
		run = runID
	}
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM reconciliation_exceptions
		WHERE tenant_id=$1 AND connector_id=$2 AND entity_type=$3 AND entity_id=$4`,
		tenantID, connectorID, ex.EntityType, ex.EntityID); err != nil {
		return recon.ReconciliationException{}, err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO reconciliation_exceptions (
			id, run_id, tenant_id, connector_id, entity_type, entity_id, status, reconciliation_result,
			reason, expected_amount, observed_amount, variance_amount, candidate_ids, confidence,
			evidence_ids, evidence_refs, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,now(),now())`,
		ex.ID, run, tenantID, connectorID, ex.EntityType, ex.EntityID, ex.Status, ex.ReconciliationResult,
		ex.Reason, ex.ExpectedAmount, ex.ObservedAmount, ex.VarianceAmount, cands, ex.Confidence,
		eids, refs,
	)
	return ex, err
}

func (s *ReconSQLStore) ListReconciliationExceptions(ctx context.Context, tenantID, connectorID string) ([]recon.ReconciliationException, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, COALESCE(run_id::text,''), tenant_id::text, connector_id::text, entity_type, entity_id,
			status, reconciliation_result, reason, expected_amount, observed_amount, variance_amount,
			candidate_ids, confidence, evidence_ids, evidence_refs, created_at, updated_at
		FROM reconciliation_exceptions
		WHERE tenant_id=$1 AND connector_id=$2
		ORDER BY created_at DESC`, tenantID, connectorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []recon.ReconciliationException
	for rows.Next() {
		ex, err := scanException(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ex)
	}
	return out, rows.Err()
}

func (s *ReconSQLStore) GetReconciliationException(ctx context.Context, tenantID, connectorID, id string) (recon.ReconciliationException, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id::text, COALESCE(run_id::text,''), tenant_id::text, connector_id::text, entity_type, entity_id,
			status, reconciliation_result, reason, expected_amount, observed_amount, variance_amount,
			candidate_ids, confidence, evidence_ids, evidence_refs, created_at, updated_at
		FROM reconciliation_exceptions
		WHERE id=$1 AND tenant_id=$2 AND connector_id=$3`, id, tenantID, connectorID)
	ex, err := scanException(row)
	if errors.Is(err, sql.ErrNoRows) {
		return recon.ReconciliationException{}, false, nil
	}
	if err != nil {
		return recon.ReconciliationException{}, false, err
	}
	return ex, true, nil
}

func scanException(row scanner) (recon.ReconciliationException, error) {
	var ex recon.ReconciliationException
	var cands, eids, refs []byte
	err := row.Scan(&ex.ID, &ex.RunID, &ex.TenantID, &ex.ConnectorID, &ex.EntityType, &ex.EntityID,
		&ex.Status, &ex.ReconciliationResult, &ex.Reason, &ex.ExpectedAmount, &ex.ObservedAmount, &ex.VarianceAmount,
		&cands, &ex.Confidence, &eids, &refs, &ex.CreatedAt, &ex.UpdatedAt)
	if err != nil {
		return recon.ReconciliationException{}, err
	}
	_ = json.Unmarshal(cands, &ex.CandidateIDs)
	_ = json.Unmarshal(eids, &ex.EvidenceIDs)
	_ = json.Unmarshal(refs, &ex.EvidenceRefs)
	return ex, nil
}

func (s *ReconSQLStore) InsertInvestigation(ctx context.Context, rec recon.InvestigationRecord) (recon.InvestigationRecord, error) {
	if rec.ID == "" {
		rec.ID = uuid.Must(uuid.NewV7()).String()
	}
	now := time.Now().UTC()
	rec.CreatedAt = now
	rec.UpdatedAt = now
	eids, _ := json.Marshal(rec.EvidenceIDs)
	if rec.EvidenceIDs == nil {
		eids = []byte("[]")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO investigation_records (
			id, tenant_id, connector_id, exception_id, entity_type, entity_id, status,
			root_cause, recommendation, confidence, financial_impact, evidence_ids, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13)`,
		rec.ID, rec.TenantID, rec.ConnectorID, nullIfEmpty(rec.ExceptionID), rec.EntityType, rec.EntityID, rec.Status,
		rec.RootCause, rec.Recommendation, rec.Confidence, rec.FinancialImpact, eids, now,
	)
	return rec, err
}

func (s *ReconSQLStore) GetInvestigation(ctx context.Context, tenantID, connectorID, id string) (recon.InvestigationRecord, bool, error) {
	var rec recon.InvestigationRecord
	var eids []byte
	var exID sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id::text, tenant_id::text, connector_id::text, COALESCE(exception_id,''), entity_type, entity_id, status,
			root_cause, recommendation, confidence, financial_impact, evidence_ids, created_at, updated_at
		FROM investigation_records
		WHERE id=$1 AND tenant_id=$2 AND connector_id=$3`, id, tenantID, connectorID,
	).Scan(&rec.ID, &rec.TenantID, &rec.ConnectorID, &exID, &rec.EntityType, &rec.EntityID, &rec.Status,
		&rec.RootCause, &rec.Recommendation, &rec.Confidence, &rec.FinancialImpact, &eids, &rec.CreatedAt, &rec.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return recon.InvestigationRecord{}, false, nil
	}
	if err != nil {
		return recon.InvestigationRecord{}, false, err
	}
	rec.ExceptionID = exID.String
	_ = json.Unmarshal(eids, &rec.EvidenceIDs)
	return rec, true, nil
}

func (s *ReconSQLStore) ListInvestigations(ctx context.Context, tenantID, connectorID string) ([]recon.InvestigationRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, tenant_id::text, connector_id::text, COALESCE(exception_id,''), entity_type, entity_id, status,
			root_cause, recommendation, confidence, financial_impact, evidence_ids, created_at, updated_at
		FROM investigation_records
		WHERE tenant_id=$1 AND connector_id=$2
		ORDER BY created_at DESC`, tenantID, connectorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []recon.InvestigationRecord
	for rows.Next() {
		var rec recon.InvestigationRecord
		var eids []byte
		var exID sql.NullString
		if err := rows.Scan(&rec.ID, &rec.TenantID, &rec.ConnectorID, &exID, &rec.EntityType, &rec.EntityID, &rec.Status,
			&rec.RootCause, &rec.Recommendation, &rec.Confidence, &rec.FinancialImpact, &eids, &rec.CreatedAt, &rec.UpdatedAt); err != nil {
			return nil, err
		}
		rec.ExceptionID = exID.String
		_ = json.Unmarshal(eids, &rec.EvidenceIDs)
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *ReconSQLStore) ListRefunds(ctx context.Context, tenantID, connectorID, paymentID string) ([]recon.RefundFact, error) {
	q := `
		SELECT id::text, refund_id, COALESCE(payment_id,''), amount_minor, currency, COALESCE(provider_status,''), COALESCE(source,'')
		FROM provider_refund_observations
		WHERE tenant_id=$1 AND connector_id=$2`
	args := []any{tenantID, connectorID}
	if paymentID != "" {
		q += ` AND payment_id=$3`
		args = append(args, paymentID)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []recon.RefundFact
	for rows.Next() {
		var r recon.RefundFact
		if err := rows.Scan(&r.ID, &r.RefundID, &r.PaymentID, &r.AmountMinor, &r.Currency, &r.ProviderStatus, &r.Source); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *ReconSQLStore) UpsertRefund(ctx context.Context, tenantID, connectorID string, r recon.RefundFact) (recon.RefundFact, error) {
	if r.ID == "" {
		r.ID = uuid.Must(uuid.NewV7()).String()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO provider_refund_observations (
			id, tenant_id, connector_id, refund_id, payment_id, amount_minor, currency, provider_status, source
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (tenant_id, connector_id, refund_id) DO UPDATE SET
			payment_id=EXCLUDED.payment_id, amount_minor=EXCLUDED.amount_minor, currency=EXCLUDED.currency,
			provider_status=EXCLUDED.provider_status, source=EXCLUDED.source, updated_at=now()`,
		r.ID, tenantID, connectorID, r.RefundID, nullIfEmpty(r.PaymentID), r.AmountMinor, nzCur(r.Currency), r.ProviderStatus, nzCurSrc(r.Source),
	)
	return r, err
}

func nzCur(s string) string {
	if s == "" {
		return "INR"
	}
	return s
}

func nzCurSrc(s string) string {
	if s == "" {
		return "webhook"
	}
	return s
}
