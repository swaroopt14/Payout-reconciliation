-- +goose Up
-- Marketplace refund graph: reference-only transfer ↔ reverse_transfer edges.
-- Not a second money ledger. Amounts are int64 minor units for correlation.
-- Provider settled / refund.processed remain non-cash.
CREATE TABLE IF NOT EXISTS marketplace_transfer_edges (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL,
    connector_id UUID NOT NULL,
    transfer_id TEXT NOT NULL,
    reverse_transfer_id TEXT NULL,
    payment_id TEXT NOT NULL DEFAULT '',
    refund_id TEXT NULL,
    seller_id TEXT NOT NULL DEFAULT '',
    amount_minor BIGINT NOT NULL DEFAULT 0,
    currency TEXT NOT NULL DEFAULT 'INR',
    transfer_at TIMESTAMPTZ NULL,
    reverse_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, connector_id, transfer_id)
);

CREATE INDEX IF NOT EXISTS marketplace_transfer_edges_payment_seller_idx
    ON marketplace_transfer_edges (tenant_id, connector_id, payment_id, seller_id);

CREATE INDEX IF NOT EXISTS marketplace_transfer_edges_refund_idx
    ON marketplace_transfer_edges (tenant_id, connector_id, refund_id)
    WHERE refund_id IS NOT NULL AND refund_id <> '';

-- +goose Down
DROP INDEX IF EXISTS marketplace_transfer_edges_refund_idx;
DROP INDEX IF EXISTS marketplace_transfer_edges_payment_seller_idx;
DROP TABLE IF EXISTS marketplace_transfer_edges;
