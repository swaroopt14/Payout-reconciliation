-- +goose Up
ALTER TABLE canonical_payments ADD COLUMN IF NOT EXISTS batch_id TEXT NOT NULL DEFAULT '';
ALTER TABLE canonical_payouts ADD COLUMN IF NOT EXISTS batch_id TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS canonical_payments_batch_idx
    ON canonical_payments (tenant_id, connector_id, batch_id)
    WHERE batch_id <> '';

CREATE INDEX IF NOT EXISTS canonical_payouts_batch_idx
    ON canonical_payouts (tenant_id, connector_id, batch_id)
    WHERE batch_id <> '';

-- +goose Down
DROP INDEX IF EXISTS canonical_payouts_batch_idx;
DROP INDEX IF EXISTS canonical_payments_batch_idx;
ALTER TABLE canonical_payouts DROP COLUMN IF EXISTS batch_id;
ALTER TABLE canonical_payments DROP COLUMN IF EXISTS batch_id;
