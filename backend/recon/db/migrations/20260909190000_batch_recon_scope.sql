-- +goose Up
ALTER TABLE reconciliation_runs ADD COLUMN IF NOT EXISTS batch_id TEXT NOT NULL DEFAULT '';
ALTER TABLE reconciliation_runs ADD COLUMN IF NOT EXISTS entity_ids JSONB NOT NULL DEFAULT '[]'::jsonb;

CREATE INDEX IF NOT EXISTS reconciliation_runs_batch_idx
    ON reconciliation_runs (tenant_id, connector_id, batch_id, created_at DESC);

-- +goose Down
DROP INDEX IF EXISTS reconciliation_runs_batch_idx;
ALTER TABLE reconciliation_runs DROP COLUMN IF EXISTS entity_ids;
ALTER TABLE reconciliation_runs DROP COLUMN IF EXISTS batch_id;
