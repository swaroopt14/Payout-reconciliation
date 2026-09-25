-- +goose Up
-- B5: canonical_payouts.amount_minor is first-observed and never overwritten
-- by a later observation. A later, different amount is kept here so recon
-- surfaces it (VARIANCE provider_payout_amount_changed). Additive, nullable.
ALTER TABLE canonical_payouts ADD COLUMN IF NOT EXISTS amount_conflict_minor BIGINT;

-- +goose Down
ALTER TABLE canonical_payouts DROP COLUMN IF EXISTS amount_conflict_minor;
