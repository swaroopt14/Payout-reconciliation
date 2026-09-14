-- +goose Up
CREATE TABLE IF NOT EXISTS merchant_book_facts (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL,
    connector_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000',
    invoice_id TEXT NOT NULL DEFAULT '',
    order_id TEXT NOT NULL DEFAULT '',
    payment_id TEXT NOT NULL DEFAULT '',
    payout_id TEXT NOT NULL DEFAULT '',
    amount_minor BIGINT NOT NULL,
    currency TEXT NOT NULL,
    due_at TIMESTAMPTZ,
    batch_id TEXT NOT NULL DEFAULT '',
    row_hash TEXT NOT NULL,
    import_id UUID,
    fact_version INT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, connector_id, row_hash)
);

CREATE INDEX IF NOT EXISTS merchant_book_facts_payment_idx
    ON merchant_book_facts (tenant_id, payment_id)
    WHERE payment_id <> '';

CREATE INDEX IF NOT EXISTS merchant_book_facts_payout_idx
    ON merchant_book_facts (tenant_id, payout_id)
    WHERE payout_id <> '';

-- +goose Down
DROP TABLE IF EXISTS merchant_book_facts;
