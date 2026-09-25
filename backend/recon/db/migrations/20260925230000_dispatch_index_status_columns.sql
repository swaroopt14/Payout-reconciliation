-- +goose Up
-- D44 / L7: dispatch_index must record every relay dispatch and its dispatch
-- state. Additive only: nullable columns, no drops, no type changes.
-- status/status_reason are relay dispatch state (HELD, FAILED, ...), never a
-- recon verdict (D7). connector_ref is the connector slug (e.g.
-- 'razorpayx-v1') carried for display only — never a key; connector_id stays
-- the tenant's connectors.id UUID.
ALTER TABLE dispatch_index ADD COLUMN IF NOT EXISTS status TEXT;
ALTER TABLE dispatch_index ADD COLUMN IF NOT EXISTS status_reason TEXT;
ALTER TABLE dispatch_index ADD COLUMN IF NOT EXISTS rail TEXT;
ALTER TABLE dispatch_index ADD COLUMN IF NOT EXISTS routing_id TEXT;
ALTER TABLE dispatch_index ADD COLUMN IF NOT EXISTS connector_ref TEXT;
ALTER TABLE dispatch_index ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE dispatch_index DROP COLUMN IF EXISTS updated_at;
ALTER TABLE dispatch_index DROP COLUMN IF EXISTS connector_ref;
ALTER TABLE dispatch_index DROP COLUMN IF EXISTS routing_id;
ALTER TABLE dispatch_index DROP COLUMN IF EXISTS rail;
ALTER TABLE dispatch_index DROP COLUMN IF EXISTS status_reason;
ALTER TABLE dispatch_index DROP COLUMN IF EXISTS status;
