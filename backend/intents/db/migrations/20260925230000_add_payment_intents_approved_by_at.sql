-- +goose Up
-- Slice 8 (D39): record who approved a held (REQUIRES_REVIEW) payout intent
-- and when. Additive, nullable, no backfill: rows approved before this
-- migration keep NULL (approver unknown), and unapproved rows stay NULL.
-- approved_by is the verified PAYOUT_APPROVER user id from the access token
-- (never an API key). Set by PaymentIntentRepo.ApproveHeldIntent.
ALTER TABLE payment_intents
    ADD COLUMN IF NOT EXISTS approved_by TEXT NULL,
    ADD COLUMN IF NOT EXISTS approved_at TIMESTAMPTZ NULL;

-- +goose Down
ALTER TABLE payment_intents
    DROP COLUMN IF EXISTS approved_at,
    DROP COLUMN IF EXISTS approved_by;
