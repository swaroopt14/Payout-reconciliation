"""Signal → DAG bridge: map inbound events to Clearline signals.

Provides a working local trigger path tests (and a future Kafka→Asset
notifier) can exercise without calling PSP APIs.

Production wiring: signals.kafka_intake.SignalKafkaIntake consumes the
kafka_ready topics and calls handle_inbound_event for each record; its
on_trigger callback hands the payload to signal_recon_dag.
"""

from __future__ import annotations

from typing import Any, Dict, Mapping, Optional

from .banking_calendar import BankingCalendar, default_banking_calendar, gate_banking_day
from .topics import (
    ALL_SIGNALS,
    EVENT_BANK_STATEMENT_RECEIVED,
    EVENT_IMPORT_COMPLETED,
    EVENT_MERCHANT_BOOKS_NORMALIZED,
    REFUND_PROVIDER_PREFIXES,
    SIGNAL_ASSET_URIS,
    SIGNAL_BANK_FILE,
    SIGNAL_ERP_COMMIT,
    SIGNAL_REFUND,
    SIGNAL_SOURCES,
    TOPIC_BANK_EVENTS,
    TOPIC_OBSERVATION,
)


def resolve_signal(
    *,
    event_type: str = "",
    provider_event_type: str = "",
    topic: str = "",
) -> Optional[str]:
    """Map Kafka/outbox fields onto SIGNAL_* or None."""
    et = (event_type or "").strip().lower()
    pet = (provider_event_type or "").strip().lower()
    top = (topic or "").strip()

    if et == EVENT_BANK_STATEMENT_RECEIVED or top == TOPIC_BANK_EVENTS:
        return SIGNAL_BANK_FILE
    if et in (EVENT_MERCHANT_BOOKS_NORMALIZED, EVENT_IMPORT_COMPLETED):
        return SIGNAL_ERP_COMMIT
    if pet.startswith(REFUND_PROVIDER_PREFIXES) or et.startswith(REFUND_PROVIDER_PREFIXES):
        return SIGNAL_REFUND
    if top == TOPIC_OBSERVATION and (
        pet.startswith(REFUND_PROVIDER_PREFIXES) or "refund" in et
    ):
        return SIGNAL_REFUND
    return None


def trigger_signal(
    signal: str,
    *,
    tenant_id: str,
    connector_id: str,
    account_id: str = "",
    event_type: str = "",
    topic: str = "",
    when=None,
    calendar: Optional[BankingCalendar] = None,
    respect_banking_day: bool = True,
) -> Dict[str, Any]:
    """Build a local trigger payload for signal_recon_dag.

    Does not call HTTP itself — operator/DAG performs /internal/reconciliation/run.
    When respect_banking_day and day is non-banking, status=deferred (skip run).
    """
    if signal not in ALL_SIGNALS:
        raise ValueError(f"unknown signal {signal!r}; expected one of {ALL_SIGNALS}")
    if not tenant_id or not connector_id:
        raise ValueError("tenant_id and connector_id are required")

    source = SIGNAL_SOURCES[signal]
    gate = gate_banking_day(when, calendar or default_banking_calendar())
    payload: Dict[str, Any] = {
        "signal": signal,
        "asset_uri": SIGNAL_ASSET_URIS[signal],
        "tenant_id": tenant_id,
        "connector_id": connector_id,
        "account_id": account_id or "",
        "event_type": event_type,
        "topic": topic or source["topic"],
        "kafka_ready": source["kafka_ready"],
        "todo": source["todo"],
        "trigger_type": "signal",
        "kind": "schedule_projection",  # cash-schedule family only — never MATCHED/bank cash
        "banking_gate": gate,
        "status": "ready",
    }
    if respect_banking_day and not gate["allow"]:
        payload["status"] = "deferred"
        payload["action"] = None
        return payload

    payload["action"] = {
        "method": "POST",
        "endpoint": RECON_RUN_ENDPOINT,
        "body": {
            "tenant_id": tenant_id,
            "connector_id": connector_id,
            "account_id": account_id or "",
        },
    }
    return payload


# The ONLY endpoint the signal scheduler may call. It starts a normal recon run
# (FinancialHandler.InternalRun -> FinancialService.Run -> ReconcilePayment).
# The scheduler never writes match results or cash numbers itself.
RECON_RUN_ENDPOINT = "/internal/reconciliation/run"
_RUN_BODY_KEYS = ("tenant_id", "connector_id", "account_id")


def build_run_request(payload: Mapping[str, Any]) -> Dict[str, Any]:
    """Pinned request for run_signal_recon.

    Ignores any endpoint or extra body fields carried in the payload/conf, so
    a crafted DAG conf cannot redirect the scheduler to another internal path
    or smuggle batch_id / payout_ids / result fields into the run.
    """
    tenant_id = str(payload.get("tenant_id") or "")
    connector_id = str(payload.get("connector_id") or "")
    if not tenant_id or not connector_id:
        raise ValueError("tenant_id and connector_id are required")
    body = {
        "tenant_id": tenant_id,
        "connector_id": connector_id,
        "account_id": str(payload.get("account_id") or ""),
    }
    assert tuple(body) == _RUN_BODY_KEYS
    return {"method": "POST", "endpoint": RECON_RUN_ENDPOINT, "body": body}


def handle_inbound_event(message: Mapping[str, Any], *, when=None, calendar=None) -> Optional[Dict[str, Any]]:
    """Kafka/outbox message → trigger payload. Fed by signals.kafka_intake."""
    signal = resolve_signal(
        event_type=str(message.get("event_type") or ""),
        provider_event_type=str(message.get("provider_event_type") or ""),
        topic=str(message.get("topic") or ""),
    )
    if not signal:
        return None
    return trigger_signal(
        signal,
        tenant_id=str(message.get("tenant_id") or ""),
        connector_id=str(message.get("connector_id") or ""),
        account_id=str(message.get("account_id") or ""),
        event_type=str(message.get("event_type") or message.get("provider_event_type") or ""),
        topic=str(message.get("topic") or ""),
        when=when,
        calendar=calendar,
    )
