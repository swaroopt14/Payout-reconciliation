-- +goose Up
-- D52: record whether payment->transfers enrichment ran for a refund, so a
-- refund skipped for missing credentials (or edge down) is not mistaken for
-- "no marketplace". Status only; no amounts. NULL = not attempted (legacy).
ALTER TABLE provider_refund_observations
    ADD COLUMN IF NOT EXISTS enrichment_status TEXT NULL;

ALTER TABLE provider_refund_observations
    DROP CONSTRAINT IF EXISTS provider_refund_observations_enrichment_status_check;
ALTER TABLE provider_refund_observations
    ADD CONSTRAINT provider_refund_observations_enrichment_status_check
    CHECK (enrichment_status IS NULL OR enrichment_status IN
        ('enriched', 'no_transfers', 'skipped_no_creds', 'skipped_unavailable'));

-- Bounded re-enrich scans and the data-gap count read only skipped rows.
CREATE INDEX IF NOT EXISTS provider_refund_observations_enrichment_skipped_idx
    ON provider_refund_observations (tenant_id, connector_id)
    WHERE enrichment_status IN ('skipped_no_creds', 'skipped_unavailable');

-- +goose Down
DROP INDEX IF EXISTS provider_refund_observations_enrichment_skipped_idx;
ALTER TABLE provider_refund_observations
    DROP CONSTRAINT IF EXISTS provider_refund_observations_enrichment_status_check;
ALTER TABLE provider_refund_observations
    DROP COLUMN IF EXISTS enrichment_status;
