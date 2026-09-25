-- +goose Up
-- Baseline for relay's own Postgres (zord_relay_db), generated from
-- db/init.sql as of Slice 8. Relay previously had no goose migrations; the
-- schema was only applied by the embedded init.sql (db/connect.go) and the
-- Postgres docker-entrypoint mount. Every statement is IF NOT EXISTS, so this
-- is safe on databases already bootstrapped by init.sql.
-- Drift guard: db/migrations_test.go fails if init.sql and the migrations
-- stop defining the same tables and columns. Future schema changes need a
-- new migration file (and the matching init.sql edit).
-- +goose StatementBegin
-- dispatches: authoritative execution state table owned entirely by Service 4.
-- One row per (contract_id, attempt_count).
-- Service 2 is ACKed after Step 1 inserts this row — NOT after PSP ack.
-- All retry and failure decisions after Step 1 are owned here, not in Service 2.
CREATE TABLE IF NOT EXISTS dispatches (
    dispatch_id          TEXT        NOT NULL,
    contract_id          TEXT        NOT NULL,
    intent_id            TEXT        NOT NULL,
    tenant_id            TEXT        NOT NULL,
    trace_id             TEXT        NOT NULL,
    connector_id         TEXT        NOT NULL,
    corridor_id          TEXT,
    attempt_count        INTEGER     NOT NULL DEFAULT 1,
    status               TEXT        NOT NULL DEFAULT 'PENDING',
    provider_idempotency_key    TEXT NOT NULL DEFAULT '',
    correlation_carriers_json   JSONB NOT NULL DEFAULT '{}',
    payload_json                JSONB NOT NULL DEFAULT '{}',
    dispatch_governance_decision      TEXT,
    dispatch_governance_reason_codes  JSONB,
    retry_class                 TEXT,
    next_dispatch_attempt_at    TIMESTAMPTZ,
    provider_attempt_id         TEXT,
    provider_response_status    TEXT,
    provider_reference_last_seen TEXT,
    provider_request_fingerprint TEXT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at              TIMESTAMPTZ,
    acked_at             TIMESTAMPTZ,
    PRIMARY KEY (dispatch_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_dispatches_contract_attempt
    ON dispatches (contract_id, attempt_count);

CREATE INDEX IF NOT EXISTS idx_dispatches_intent_id
    ON dispatches (intent_id);

CREATE INDEX IF NOT EXISTS idx_dispatches_pending_recovery
    ON dispatches (sent_at)
    WHERE status IN ('SENT', 'AWAITING_PROVIDER_SIGNAL');

CREATE INDEX IF NOT EXISTS idx_dispatches_retry
    ON dispatches (next_dispatch_attempt_at)
    WHERE status = 'FAILED_RETRYABLE'
      AND next_dispatch_attempt_at IS NOT NULL;

-- relay_outbox: Service 4 event durability. No PII ever stored here.
CREATE TABLE IF NOT EXISTS relay_outbox (
    event_id     TEXT        NOT NULL PRIMARY KEY,
    event_type   TEXT        NOT NULL,
    dispatch_id  TEXT        NOT NULL,
    contract_id  TEXT        NOT NULL,
    intent_id    TEXT        NOT NULL,
    tenant_id    TEXT        NOT NULL,
    trace_id     TEXT        NOT NULL,
    payload      JSONB       NOT NULL,
    status       TEXT        NOT NULL DEFAULT 'PENDING',
    retry_count  INTEGER     NOT NULL DEFAULT 0,
    lease_id     TEXT,
    lease_until  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_relay_outbox_pending
    ON relay_outbox (created_at ASC)
    WHERE status = 'PENDING';

CREATE INDEX IF NOT EXISTS idx_relay_outbox_dispatch_id
    ON relay_outbox (dispatch_id);

-- connector_health_state: tracks circuit breaker state per connector.
CREATE TABLE IF NOT EXISTS connector_health_state (
    connector_id          TEXT        NOT NULL PRIMARY KEY,
    circuit_state         TEXT        NOT NULL DEFAULT 'CLOSED',
    consecutive_failures  INTEGER     NOT NULL DEFAULT 0,
    opened_at             TIMESTAMPTZ,
    next_probe_at         TIMESTAMPTZ,
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- relay_payload_conflicts: audit trail for payload_hash verification failures.
-- Every time we lease an event whose payload recomputed hash does not match
-- the upstream-provided payload_hash, we write one row here. This is the
-- persistence requirement of P0 6.1.2 and feeds the operational alert.
CREATE TABLE IF NOT EXISTS relay_payload_conflicts (
    conflict_id          BIGSERIAL   NOT NULL PRIMARY KEY,
    service_name         TEXT        NOT NULL,
    event_id             TEXT        NOT NULL,
    event_type           TEXT,
    lease_id             TEXT,
    expected_hash        TEXT        NOT NULL,     -- upstream payload_hash
    computed_hash        TEXT        NOT NULL,     -- our SHA-256 over raw payload bytes
    payload_snippet      TEXT,                     -- first ~256 chars of payload for triage
    conflict_count       INTEGER     NOT NULL DEFAULT 1,  -- counts repeated NACK attempts for same event
    is_permanent         BOOLEAN     NOT NULL DEFAULT FALSE,  -- TRUE after MaxConflictRetries: we ACKed it out
    detected_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at          TIMESTAMPTZ               -- NULL until ACKed or eventually succeeds
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_relay_payload_conflicts_event
    ON relay_payload_conflicts (service_name, event_id);

CREATE INDEX IF NOT EXISTS idx_relay_payload_conflicts_detected
    ON relay_payload_conflicts (detected_at DESC);

-- relay_publish_failures: durable record of every exhausted Kafka publish
-- attempt on the outbox-relay path (P0 6.1.3).
--
-- An exhausted publish attempt (poison event, or transient Kafka failure
-- after max retries) must never be reduced to a log line. A best-effort DLQ
-- Kafka message is not sufficient proof of durability on its own — Kafka may
-- be the very thing that is unreachable when the failure happens. The
-- upstream lease for the source event is only acknowledged after either the
-- publish itself succeeded, or a row has been committed here.
CREATE TABLE IF NOT EXISTS relay_publish_failures (
    failure_id        BIGSERIAL   NOT NULL PRIMARY KEY,
    source_event_id    TEXT        NOT NULL,
    source_service     TEXT        NOT NULL,
    topic              TEXT        NOT NULL DEFAULT '',  -- topic carried on the source event, if any
    destination_topic  TEXT        NOT NULL,              -- Kafka topic the publish was attempted against
    payload_hash       TEXT        NOT NULL,               -- SHA-256 over raw payload bytes
    failure_source     TEXT NOT NULL DEFAULT 'UPSTREAM_OUTBOX',  -- UPSTREAM_OUTBOX | RELAY_OUTBOX | BATCH
    publish_kind       TEXT NOT NULL DEFAULT 'generic',
    tenant_id          TEXT,
    trace_id           TEXT,
    message_key        TEXT NOT NULL DEFAULT '',
    message_value      BYTEA NOT NULL DEFAULT '',          -- replay source; never returned in list API
    headers_json       JSONB,
    attempt_count      INTEGER     NOT NULL DEFAULT 1,
    failure_class      TEXT        NOT NULL,               -- reason code, e.g. INVALID_PAYLOAD, KAFKA_MAX_RETRIES_EXCEEDED
    last_error         TEXT        NOT NULL,
    replay_status      TEXT        NOT NULL DEFAULT 'PENDING', -- PENDING | REPLAYING | REPLAYED | QUARANTINED
    first_failure_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_failure_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    detected_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    replayed_at        TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_relay_publish_failures_event
    ON relay_publish_failures (source_service, source_event_id, destination_topic);

CREATE INDEX IF NOT EXISTS idx_relay_publish_failures_pending
    ON relay_publish_failures (last_failure_at ASC)
    WHERE replay_status = 'PENDING';

CREATE INDEX IF NOT EXISTS idx_relay_publish_failures_search
    ON relay_publish_failures (replay_status, last_failure_at);

CREATE INDEX IF NOT EXISTS idx_relay_publish_failures_tenant
    ON relay_publish_failures (tenant_id);

CREATE TABLE IF NOT EXISTS relay_publish_failure_replays (
    replay_id     BIGSERIAL PRIMARY KEY,
    failure_id    BIGINT NOT NULL REFERENCES relay_publish_failures(failure_id),
    operator_id   TEXT NOT NULL,
    reason        TEXT NOT NULL,
    outcome       TEXT NOT NULL CHECK (outcome IN ('SUCCESS','FAILED')),
    error_message TEXT,
    replayed_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_relay_failure_replays_failure 
    ON relay_publish_failure_replays (failure_id, replayed_at DESC);

-- status values: PENDING | PUBLISHED | FAILED
-- retry_count already exists; wire it in RelayOutboxRepo
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS relay_publish_failure_replays;
DROP TABLE IF EXISTS relay_publish_failures;
DROP TABLE IF EXISTS relay_payload_conflicts;
DROP TABLE IF EXISTS connector_health_state;
DROP TABLE IF EXISTS relay_outbox;
DROP TABLE IF EXISTS dispatches;
