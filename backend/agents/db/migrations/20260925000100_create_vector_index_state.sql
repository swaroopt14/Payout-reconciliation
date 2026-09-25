-- +goose Up
-- vector_index_state was only created at runtime by
-- repositories/vector_index_state.go EnsureSchema. This migration mirrors it
-- exactly (IF NOT EXISTS, so it is safe where EnsureSchema already ran).
CREATE TABLE IF NOT EXISTS vector_index_state (
    vector_id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    source_service TEXT NOT NULL DEFAULT '',
    entity_type TEXT NOT NULL DEFAULT '',
    entity_id TEXT NOT NULL DEFAULT '',
    source_type TEXT NOT NULL DEFAULT '',
    content_hash TEXT NOT NULL DEFAULT '',
    content_version TEXT NOT NULL DEFAULT '',
    pinecone_namespace TEXT NOT NULL DEFAULT '',
    embedding_model TEXT NOT NULL DEFAULT '',
    embedding_dimension INTEGER NOT NULL DEFAULT 0,
    last_event_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'indexed',
    error_message TEXT NOT NULL DEFAULT '',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    next_retry_at TIMESTAMPTZ,
    last_indexed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_vector_index_state_entity
    ON vector_index_state (tenant_id, entity_type, entity_id);
CREATE INDEX IF NOT EXISTS idx_vector_index_state_hash
    ON vector_index_state (tenant_id, content_hash);
CREATE INDEX IF NOT EXISTS idx_vector_index_state_retry
    ON vector_index_state (status, next_retry_at)
    WHERE status = 'rate_limited';

-- +goose Down
DROP TABLE IF EXISTS vector_index_state;
