-- +goose Up
ALTER TABLE reconciliation_results ADD COLUMN IF NOT EXISTS rule_version TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE reconciliation_results DROP COLUMN IF EXISTS rule_version;
