package persistence

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"zord-outcome-engine/internal/payouttruth"
	"zord-outcome-engine/internal/recon"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

var _ payouttruth.Store = (*SQLStore)(nil)

func (s *SQLStore) InsertPayoutObservationEvent(ctx context.Context, obs payouttruth.Observation) (bool, error) {
	if obs.IdentityHash == "" {
		obs.IdentityHash = payouttruth.IdentityHash(obs.TenantID, obs.ConnectorID, obs.Provider, obs.PayoutID, obs.Source, obs.SourceEventID, obs.SourceHash)
	}
	id := uuid.Must(uuid.NewV7()).String()
	observedAt := obs.ObservedAt
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	var returned string
	err := s.runner(ctx).QueryRowContext(ctx, `
		INSERT INTO provider_payout_observation_events (
			id, tenant_id, connector_id, payout_id, source, status, provider_status,
			source_event_id, source_hash, payload_hash, amount_minor, currency, utr, mode, purpose,
			status_reason, raw_reference, observation_identity_hash, observed_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
		ON CONFLICT (observation_identity_hash) DO NOTHING
		RETURNING id`,
		id, obs.TenantID, obs.ConnectorID, obs.PayoutID, obs.Source, obs.ProviderStatus, obs.ProviderStatus,
		obs.SourceEventID, nullIfEmpty(obs.SourceHash), nullIfEmpty(obs.SourceHash),
		obs.AmountMinor, nullIfEmpty(obs.Currency), nullIfEmpty(obs.UTR), nullIfEmpty(obs.Mode), nullIfEmpty(obs.Purpose),
		nullIfEmpty(obs.StatusReason), nullIfEmpty(obs.RawReference), obs.IdentityHash, observedAt,
	).Scan(&returned)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return returned != "", nil
}

func (s *SQLStore) GetCanonicalPayout(ctx context.Context, tenantID, connectorID, payoutID string) (payouttruth.CanonicalPayout, bool, error) {
	var pay payouttruth.CanonicalPayout
	var created sql.NullTime
	var sources pq.StringArray
	err := s.runner(ctx).QueryRowContext(ctx, `
		SELECT id::text, tenant_id::text, connector_id::text, provider, payout_id, amount_minor, currency,
			provider_status, COALESCE(utr,''), COALESCE(mode,''), COALESCE(purpose,''), COALESCE(status_reason,''),
			provider_created_at, first_observed_at, last_observed_at, COALESCE(sources, '{}')
		FROM canonical_payouts
		WHERE tenant_id=$1 AND connector_id=$2 AND payout_id=$3
		FOR UPDATE`, tenantID, connectorID, payoutID,
	).Scan(&pay.ID, &pay.TenantID, &pay.ConnectorID, &pay.Provider, &pay.PayoutID, &pay.AmountMinor, &pay.Currency,
		&pay.ProviderStatus, &pay.UTR, &pay.Mode, &pay.Purpose, &pay.StatusReason,
		&created, &pay.FirstObservedAt, &pay.LastObservedAt, &sources)
	if errors.Is(err, sql.ErrNoRows) {
		return payouttruth.CanonicalPayout{}, false, nil
	}
	if err != nil {
		return payouttruth.CanonicalPayout{}, false, err
	}
	if created.Valid {
		pay.ProviderCreatedAt = created.Time
	}
	pay.Sources = []string(sources)
	return pay, true, nil
}

func (s *SQLStore) UpsertCanonicalPayout(ctx context.Context, pay payouttruth.CanonicalPayout) error {
	if pay.ID == "" {
		pay.ID = uuid.Must(uuid.NewV7()).String()
	}
	if pay.Provider == "" {
		pay.Provider = "razorpay"
	}
	now := time.Now().UTC()
	if pay.FirstObservedAt.IsZero() {
		pay.FirstObservedAt = now
	}
	if pay.LastObservedAt.IsZero() {
		pay.LastObservedAt = now
	}
	_, err := s.runner(ctx).ExecContext(ctx, `
		INSERT INTO canonical_payouts (
			id, tenant_id, connector_id, provider, payout_id, amount_minor, currency, provider_status,
			utr, mode, purpose, status_reason, provider_created_at, first_observed_at, last_observed_at, sources, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$17)
		ON CONFLICT (tenant_id, connector_id, provider, payout_id) DO UPDATE SET
			amount_minor=EXCLUDED.amount_minor, currency=EXCLUDED.currency,
			provider_status=EXCLUDED.provider_status, utr=EXCLUDED.utr, mode=EXCLUDED.mode,
			purpose=EXCLUDED.purpose, status_reason=EXCLUDED.status_reason,
			last_observed_at=EXCLUDED.last_observed_at, sources=EXCLUDED.sources, updated_at=now()`,
		pay.ID, pay.TenantID, pay.ConnectorID, pay.Provider, pay.PayoutID, pay.AmountMinor, pay.Currency, pay.ProviderStatus,
		pay.UTR, pay.Mode, pay.Purpose, pay.StatusReason, nullTime(pay.ProviderCreatedAt),
		pay.FirstObservedAt, pay.LastObservedAt, pq.StringArray(pay.Sources), now,
	)
	return err
}

func (s *SQLStore) ListPayoutObservationEvents(ctx context.Context, tenantID, connectorID, payoutID string) ([]payouttruth.Observation, error) {
	rows, err := s.runner(ctx).QueryContext(ctx, `
		SELECT payout_id, COALESCE(amount_minor,0), COALESCE(currency,''), COALESCE(provider_status, status),
			COALESCE(utr,''), COALESCE(mode,''), COALESCE(purpose,''), COALESCE(status_reason,''),
			COALESCE(source,''), COALESCE(source_event_id,''), COALESCE(source_hash,''), observed_at
		FROM provider_payout_observation_events
		WHERE tenant_id=$1 AND connector_id=$2 AND payout_id=$3
		ORDER BY observed_at ASC`, tenantID, connectorID, payoutID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []payouttruth.Observation
	for rows.Next() {
		var obs payouttruth.Observation
		obs.TenantID, obs.ConnectorID = tenantID, connectorID
		if err := rows.Scan(&obs.PayoutID, &obs.AmountMinor, &obs.Currency, &obs.ProviderStatus,
			&obs.UTR, &obs.Mode, &obs.Purpose, &obs.StatusReason,
			&obs.Source, &obs.SourceEventID, &obs.SourceHash, &obs.ObservedAt); err != nil {
			return nil, err
		}
		out = append(out, obs)
	}
	return out, rows.Err()
}

func (s *ReconSQLStore) ListCanonicalPayouts(ctx context.Context, tenantID, connectorID string) ([]recon.PayoutFact, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, payout_id, provider_status, amount_minor, currency, COALESCE(utr,''), COALESCE(mode,''),
			COALESCE(purpose,''), COALESCE(status_reason,''), provider_created_at, first_observed_at, COALESCE(batch_id,'')
		FROM canonical_payouts WHERE tenant_id=$1 AND connector_id=$2 ORDER BY last_observed_at ASC`,
		tenantID, connectorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []recon.PayoutFact
	for rows.Next() {
		p, err := scanPayoutFact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *ReconSQLStore) GetCanonicalPayoutFact(ctx context.Context, tenantID, connectorID, payoutID string) (recon.PayoutFact, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id::text, payout_id, provider_status, amount_minor, currency, COALESCE(utr,''), COALESCE(mode,''),
			COALESCE(purpose,''), COALESCE(status_reason,''), provider_created_at, first_observed_at, COALESCE(batch_id,'')
		FROM canonical_payouts WHERE tenant_id=$1 AND connector_id=$2 AND payout_id=$3`,
		tenantID, connectorID, payoutID)
	p, err := scanPayoutFact(row)
	if errors.Is(err, sql.ErrNoRows) {
		return recon.PayoutFact{}, false, nil
	}
	if err != nil {
		return recon.PayoutFact{}, false, err
	}
	return p, true, nil
}

func (s *ReconSQLStore) ListPayoutObservationFacts(ctx context.Context, tenantID, connectorID, payoutID string) ([]recon.ObservationFact, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT COALESCE(source,''), COALESCE(provider_status, status, ''), COALESCE(status,''),
			COALESCE(source_event_id,''), COALESCE(source_hash,''),
			COALESCE(NULLIF(utr,''), COALESCE(raw_reference,'')), observed_at
		FROM provider_payout_observation_events
		WHERE tenant_id=$1 AND connector_id=$2 AND payout_id=$3 ORDER BY observed_at ASC`,
		tenantID, connectorID, payoutID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []recon.ObservationFact
	for rows.Next() {
		var f recon.ObservationFact
		if err := rows.Scan(&f.Source, &f.ProviderStatus, &f.CanonicalStatus, &f.SourceEventID, &f.SourceHash, &f.RawReference, &f.ObservedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func scanPayoutFact(row scanner) (recon.PayoutFact, error) {
	var p recon.PayoutFact
	var created sql.NullTime
	err := row.Scan(&p.ID, &p.PayoutID, &p.ProviderStatus, &p.AmountMinor, &p.Currency,
		&p.UTR, &p.Mode, &p.Purpose, &p.StatusReason, &created, &p.FirstObservedAt, &p.BatchID)
	if err != nil {
		return recon.PayoutFact{}, err
	}
	if created.Valid {
		p.ProviderCreatedAt = created.Time
	}
	return p, nil
}
