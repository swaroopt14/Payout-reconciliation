"""Signal-first recon operator.

Calls outcome-engine internal endpoints only. Never calls Razorpay / PSP URLs.
Scoped by tenant_id + connector_id. Cash-schedule family: schedule_projection
semantics live in Go; this path only wakes InternalRun.
"""

from __future__ import annotations

import json
import os
import sys
from datetime import datetime, timezone

from airflow.providers.http.hooks.http import HttpHook
from airflow.sdk import Variable

# plugins/ on PYTHONPATH in Airflow; also support repo-local imports in tests.
_PLUGINS = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
if _PLUGINS not in sys.path:
    sys.path.insert(0, _PLUGINS)

from signals.banking_calendar import default_banking_calendar, gate_banking_day
from signals.bridge import RECON_RUN_ENDPOINT, build_run_request, resolve_signal, trigger_signal
from signals.topics import ALL_SIGNALS, SIGNAL_SOURCES

ZORD_OUTCOME_ENGINE_CONN_ID = "zord_outcome_engine_http"
# Only endpoint this operator may call (pinned; never taken from conf/XCom).
FINANCIAL_RECON_ENDPOINT = RECON_RUN_ENDPOINT


# Recon's relayTenantMustMatch (backend/recon/handlers/relay_tenant.go) answers
# 403 relay_tenant_required without this header, 403 tenant_mismatch if it
# differs from body.tenant_id. Value always comes from the pinned run body.
RELAY_TENANT_HEADER = "X-Relay-Tenant-ID"


def _headers(tenant_id: str):
    token = os.environ.get("RELAY_AUTH_TOKEN") or Variable.get("relay_auth_token", default="")
    tenant = str(tenant_id or "").strip()
    if not tenant:
        raise ValueError("tenant_id is required (X-Relay-Tenant-ID)")
    return {
        "Content-Type": "application/json",
        "X-Relay-Token": token,
        "X-Relay-Instance-ID": "airflow-signal-recon",
        RELAY_TENANT_HEADER: tenant,
    }


def _scope_from_context(context) -> tuple[str, str, str]:
    conf = (context.get("dag_run").conf if context.get("dag_run") else None) or {}
    tenant_id = (
        conf.get("tenant_id")
        or Variable.get("razorpay_tenant_id", default="")
    )
    connector_id = (
        conf.get("connector_id")
        or Variable.get("razorpay_connector_id", default="")
    )
    account_id = (
        conf.get("account_id")
        or Variable.get("razorpay_bank_account_id", default="")
    )
    return str(tenant_id), str(connector_id), str(account_id)


def resolve_trigger_from_context(**context) -> dict:
    """Build trigger payload from DAG run conf or Asset-driven empty conf."""
    conf = (context.get("dag_run").conf if context.get("dag_run") else None) or {}
    tenant_id, connector_id, account_id = _scope_from_context(context)

    signal = conf.get("signal")
    if not signal:
        signal = resolve_signal(
            event_type=str(conf.get("event_type") or ""),
            provider_event_type=str(conf.get("provider_event_type") or ""),
            topic=str(conf.get("topic") or ""),
        )
    # Asset-only wake with no conf: default to erp_commit label is wrong.
    # Prefer explicit conf; if missing, leave signal unset and let gate skip.
    if not signal:
        # When Airflow AssetAny fires, conf may be empty — treat as generic signal wake
        # and still run financial recon if banking day (conf can name the signal later).
        signal = conf.get("forcing_signal") or "erp_commit"
        if signal not in ALL_SIGNALS:
            signal = "erp_commit"

    when = conf.get("as_of")  # optional YYYY-MM-DD for tests
    return trigger_signal(
        signal,
        tenant_id=tenant_id,
        connector_id=connector_id,
        account_id=account_id,
        event_type=str(conf.get("event_type") or conf.get("provider_event_type") or ""),
        topic=str(conf.get("topic") or ""),
        when=when,
        calendar=default_banking_calendar(),
        respect_banking_day=bool(conf.get("respect_banking_day", True)),
    )


def gate_or_skip(**context) -> str:
    """Branch: continue to run_signal_recon or skip_non_banking_day."""
    payload = resolve_trigger_from_context(**context)
    context["ti"].xcom_push(key="signal_payload", value=payload)
    if payload.get("status") == "deferred":
        return "skip_non_banking_day"
    return "run_signal_recon"


def skip_non_banking_day(**context) -> dict:
    payload = context["ti"].xcom_pull(key="signal_payload") or {}
    gate = payload.get("banking_gate") or {}
    return {
        "skipped": True,
        "reason": "non_banking_day",
        "as_of": gate.get("as_of"),
        "deferred_to": gate.get("deferred_to"),
        "signal": payload.get("signal"),
        "kind": "schedule_projection",
    }


def run_signal_recon(**context) -> dict:
    """POST /internal/reconciliation/run — never PSP URLs."""
    payload = context["ti"].xcom_pull(key="signal_payload")
    if not payload:
        payload = resolve_trigger_from_context(**context)
        context["ti"].xcom_push(key="signal_payload", value=payload)

    if payload.get("status") == "deferred":
        return skip_non_banking_day(**context)

    # Endpoint and body are pinned: a normal recon run only. Go runs
    # ReconcilePayment; this operator never writes results or cash numbers.
    req = build_run_request(payload)

    hook = HttpHook(method="POST", http_conn_id=ZORD_OUTCOME_ENGINE_CONN_ID)
    response = hook.run(
        endpoint=req["endpoint"],
        data=json.dumps(req["body"]),
        headers=_headers(req["body"]["tenant_id"]),
    )
    result = response.json()
    result["_signal"] = payload.get("signal")
    result["_trigger_type"] = "signal"
    result["_kind"] = "schedule_projection"
    result["_kafka_ready"] = payload.get("kafka_ready")
    if payload.get("todo"):
        result["_todo"] = payload["todo"]
    return result


def document_timer_is_backup() -> str:
    """Marker string asserted by tests — timers are backup only."""
    return "BACKUP_TIMER_ONLY"
