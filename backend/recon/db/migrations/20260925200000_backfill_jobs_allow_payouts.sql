-- +goose Up
-- D26: timer pull of payouts. Additive: widen the resource_type check only.
ALTER TABLE backfill_jobs DROP CONSTRAINT IF EXISTS backfill_jobs_resource_type_check;
ALTER TABLE backfill_jobs ADD CONSTRAINT backfill_jobs_resource_type_check
    CHECK (resource_type IN ('payments', 'settlements', 'refunds', 'all', 'payouts'));

-- +goose Down
ALTER TABLE backfill_jobs DROP CONSTRAINT IF EXISTS backfill_jobs_resource_type_check;
ALTER TABLE backfill_jobs ADD CONSTRAINT backfill_jobs_resource_type_check
    CHECK (resource_type IN ('payments', 'settlements', 'refunds', 'all')) NOT VALID;
