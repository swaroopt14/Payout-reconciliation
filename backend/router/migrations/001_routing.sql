CREATE TABLE IF NOT EXISTS processors (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    priority INT NOT NULL DEFAULT 0,
    cost_percentage DOUBLE PRECISION NOT NULL DEFAULT 0.10,
    supported_currencies TEXT[] NOT NULL DEFAULT '{}',
    collect_rails TEXT[] NOT NULL DEFAULT '{}',
    payout_rails TEXT[] NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS routing_rules (
    id TEXT PRIMARY KEY,
    merchant_id TEXT,
    name TEXT NOT NULL,
    priority INT NOT NULL,
    condition JSONB NOT NULL,
    action JSONB NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS routing_rules_merchant_idx
    ON routing_rules (merchant_id, enabled, priority);

CREATE TABLE IF NOT EXISTS routing_decisions (
    id TEXT PRIMARY KEY,
    payment_id TEXT NOT NULL UNIQUE,
    merchant_id TEXT,
    selected_processor TEXT NOT NULL,
    rail TEXT NOT NULL,
    direction TEXT NOT NULL,
    score DOUBLE PRECISION,
    routing_strategy TEXT,
    decision_metadata JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS routing_outcomes (
    id BIGSERIAL PRIMARY KEY,
    routing_id TEXT NOT NULL,
    payment_id TEXT NOT NULL,
    processor TEXT NOT NULL,
    success BOOLEAN NOT NULL,
    latency_ms DOUBLE PRECISION,
    failure_class TEXT,
    counts_toward_circuit BOOLEAN NOT NULL DEFAULT FALSE,
    use_fallback BOOLEAN NOT NULL DEFAULT FALSE,
    triage JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS routing_outcomes_processor_idx
    ON routing_outcomes (processor, created_at DESC);

CREATE INDEX IF NOT EXISTS routing_outcomes_payment_idx
    ON routing_outcomes (payment_id, created_at DESC);
