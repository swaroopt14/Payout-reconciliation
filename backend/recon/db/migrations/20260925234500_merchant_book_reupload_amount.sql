-- +goose Up
-- B5: merchant book row identity no longer includes amount_minor. An edited
-- re-upload of the same invoice/payment/payout keeps the original
-- amount_minor; the re-uploaded amount is kept here so recon can raise
-- VARIANCE (merchant_amount_changed_on_reupload). Additive, nullable.
ALTER TABLE merchant_book_facts ADD COLUMN IF NOT EXISTS reupload_amount_minor BIGINT;

-- +goose Down
ALTER TABLE merchant_book_facts DROP COLUMN IF EXISTS reupload_amount_minor;
