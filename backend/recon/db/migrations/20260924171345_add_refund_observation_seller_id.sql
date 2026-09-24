-- +goose Up
-- Slice 1: optional marketplace seller on refund observations (fact shape only).
-- Null/empty seller_id is a normal refund and does not join any seller cluster.
ALTER TABLE provider_refund_observations
    ADD COLUMN IF NOT EXISTS seller_id TEXT NULL;

-- +goose Down
ALTER TABLE provider_refund_observations
    DROP COLUMN IF EXISTS seller_id;
