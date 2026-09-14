-- +goose Up
DROP INDEX IF EXISTS canonical_outcome_events_dedupe_key_uq;
CREATE UNIQUE INDEX IF NOT EXISTS canonical_outcome_events_tenant_dedupe_uq
    ON canonical_outcome_events (tenant_id, dedupe_key);

-- +goose Down
DROP INDEX IF EXISTS canonical_outcome_events_tenant_dedupe_uq;
CREATE UNIQUE INDEX canonical_outcome_events_dedupe_key_uq ON canonical_outcome_events (dedupe_key);
