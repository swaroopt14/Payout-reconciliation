"""Canonical signal → Kafka/outbox mappings for the Clearline signal scheduler.

Names are taken from existing outcome-engine / relay surfaces — do not invent
topics that contradict relay/config or recon cmd/main.go defaults.

ERP commit: merchant import commit writes outbox events
  merchant.books.normalized.v1 and import.completed.v1
  (see backend/recon/internal/imports/service.go). Relay outcome-engine
  route_allow_list routes merchant.books.normalized.v1 (and import.completed.v1)
  → payments.outcome.events.v1 (see backend/relay/config.yaml).

Refund: provider observations on KAFKA_OBSERVATION_TOPIC
  default payments.ledger.events.v1 (recon/cmd/main.go), with
  provider_event_type refund.created / refund.processed / refund.*.

Bank file: KAFKA_BANK_TOPIC default payments.bank.events.v1,
  event_type bank.statement.received (models.EventTypeBankStatementReceived).
"""

from __future__ import annotations

# --- Signal ids (internal scheduler vocabulary) ---
SIGNAL_ERP_COMMIT = "erp_commit"
SIGNAL_REFUND = "refund"
SIGNAL_BANK_FILE = "bank_file"

ALL_SIGNALS = (SIGNAL_ERP_COMMIT, SIGNAL_REFUND, SIGNAL_BANK_FILE)

# --- Kafka topics that already exist in recon/relay ---
TOPIC_BANK_EVENTS = "payments.bank.events.v1"  # KAFKA_BANK_TOPIC default
TOPIC_OBSERVATION = "payments.ledger.events.v1"  # KAFKA_OBSERVATION_TOPIC default
TOPIC_OUTCOME_EVENTS = "payments.outcome.events.v1"  # relay outcome-engine default

# --- Event type strings from recon models / imports ---
EVENT_BANK_STATEMENT_RECEIVED = "bank.statement.received"
EVENT_IMPORT_COMPLETED = "import.completed.v1"
EVENT_MERCHANT_BOOKS_NORMALIZED = "merchant.books.normalized.v1"
EVENT_BANK_OBSERVATION_NORMALIZED = "bank.observation.normalized.v1"

# Refund provider_event_type prefixes (edge → observation consumer)
REFUND_PROVIDER_PREFIXES = ("refund.",)

# Asset URIs used by Airflow Asset triggers (stable, local vocabulary).
ASSET_URI_ERP_COMMIT = "asset://clearline/signals/erp.commit"
ASSET_URI_REFUND = "asset://clearline/signals/refund"
ASSET_URI_BANK_FILE = "asset://clearline/signals/bank.file"

SIGNAL_ASSET_URIS = {
    SIGNAL_ERP_COMMIT: ASSET_URI_ERP_COMMIT,
    SIGNAL_REFUND: ASSET_URI_REFUND,
    SIGNAL_BANK_FILE: ASSET_URI_BANK_FILE,
}

# Expected Kafka / source per signal (for docs + TODO stubs).
SIGNAL_SOURCES = {
    SIGNAL_ERP_COMMIT: {
        "event_types": (EVENT_MERCHANT_BOOKS_NORMALIZED, EVENT_IMPORT_COMPLETED),
        "topic": TOPIC_OUTCOME_EVENTS,
        "source": "outcome_outbox_via_relay",
        # Relay outcome-engine route_allow_list now includes merchant.books.normalized.v1.
        "kafka_ready": True,
        "todo": None,
    },
    SIGNAL_REFUND: {
        "event_types": ("refund.created", "refund.processed", "refund.failed"),
        "topic": TOPIC_OBSERVATION,
        "source": "provider_observation_kafka",
        "kafka_ready": True,
        "todo": None,
    },
    SIGNAL_BANK_FILE: {
        "event_types": (EVENT_BANK_STATEMENT_RECEIVED,),
        "topic": TOPIC_BANK_EVENTS,
        "source": "bank_statement_kafka",
        "kafka_ready": True,
        "todo": None,
    },
}
