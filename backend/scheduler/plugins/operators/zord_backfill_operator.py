from airflow.providers.http.hooks.http import HttpHook
from airflow.sdk import Variable
import json
import os
import time

ZORD_OUTCOME_ENGINE_CONN_ID = "zord_outcome_engine_http"
BACKFILL_PAYMENTS_ENDPOINT = "/internal/backfill/payments"
BACKFILL_SETTLEMENTS_ENDPOINT = "/internal/backfill/settlements"
BACKFILL_JOB_ENDPOINT = "/internal/backfill/jobs/{job_id}"
FRESHNESS_ENDPOINT = "/internal/freshness/{job_id}"
RECON_RUN_ENDPOINT = "/internal/recon/run"
FINANCIAL_RECON_ENDPOINT = "/internal/reconciliation/run"  # signal / cash-schedule family


def _headers():
    token = os.environ.get("RELAY_AUTH_TOKEN") or Variable.get("relay_auth_token", default="")
    return {
        "Content-Type": "application/json",
        "X-Relay-Token": token,
        "X-Relay-Instance-ID": "airflow-razorpay-backfill",
    }


def create_and_trigger_backfill(resource_type, window_from, window_to, **context):
    tenant_id = Variable.get("razorpay_tenant_id")
    connector_id = Variable.get("razorpay_connector_id")
    mode = Variable.get("razorpay_mode", default="test")

    overlap_minutes = int(Variable.get("razorpay_payment_overlap_minutes", default="10"))
    endpoint = BACKFILL_PAYMENTS_ENDPOINT
    if resource_type == "settlements":
        endpoint = BACKFILL_SETTLEMENTS_ENDPOINT

    hook = HttpHook(method="POST", http_conn_id=ZORD_OUTCOME_ENGINE_CONN_ID)
    response = hook.run(
        endpoint=endpoint,
        data=json.dumps({
            "tenant_id": tenant_id,
            "connector_id": connector_id,
            "window_from": window_from,
            "window_to": window_to,
            "trigger_type": "airflow",
            "mode": mode,
            "overlap_minutes": overlap_minutes,
        }),
        headers=_headers(),
    )
    result = response.json()
    job_id = result.get("job_id")
    if not job_id:
        raise RuntimeError("outcome-engine did not return job_id")
    context["ti"].xcom_push(key="job_id", value=job_id)
    return result


def wait_for_backfill(**context):
    job_id = context["ti"].xcom_pull(key="job_id")
    if not job_id:
        job_id = context["ti"].xcom_pull(task_ids="create_payment_backfill_jobs", key="job_id")
    if not job_id:
        job_id = context["ti"].xcom_pull(task_ids="create_settlement_day_jobs", key="job_id")
    if not job_id:
        raise RuntimeError("missing job_id")

    hook = HttpHook(method="GET", http_conn_id=ZORD_OUTCOME_ENGINE_CONN_ID)
    deadline = time.time() + 15 * 60
    last = {}
    while time.time() < deadline:
        response = hook.run(
            endpoint=BACKFILL_JOB_ENDPOINT.format(job_id=job_id),
            headers=_headers(),
        )
        last = response.json()
        status = last.get("status")
        if status in ("succeeded", "partial"):
            return last
        if status in ("failed", "cancelled"):
            raise RuntimeError(f"backfill job {job_id} status={status}")
        time.sleep(5)
    raise RuntimeError(f"backfill job {job_id} timed out status={last.get('status')}")


def run_freshness_check(**context):
    job_id = context["ti"].xcom_pull(key="job_id")
    if not job_id:
        job_id = Variable.get("razorpay_last_backfill_job_id", default="")
    if not job_id:
        return {"skipped": True, "reason": "no job_id"}
    hook = HttpHook(method="GET", http_conn_id=ZORD_OUTCOME_ENGINE_CONN_ID)
    response = hook.run(
        endpoint=FRESHNESS_ENDPOINT.format(job_id=job_id),
        headers=_headers(),
    )
    return response.json()


def run_recon(**context):
    tenant_id = Variable.get("razorpay_tenant_id")
    connector_id = Variable.get("razorpay_connector_id")
    account_id = Variable.get("razorpay_bank_account_id", default="")
    hook = HttpHook(method="POST", http_conn_id=ZORD_OUTCOME_ENGINE_CONN_ID)
    response = hook.run(
        endpoint=RECON_RUN_ENDPOINT,
        data=json.dumps({
            "tenant_id": tenant_id,
            "connector_id": connector_id,
            "account_id": account_id,
        }),
        headers=_headers(),
    )
    return response.json()


def run_financial_recon(**context):
    """POST /internal/reconciliation/run (tenant+connector). Used by signal path too."""
    tenant_id = Variable.get("razorpay_tenant_id")
    connector_id = Variable.get("razorpay_connector_id")
    account_id = Variable.get("razorpay_bank_account_id", default="")
    hook = HttpHook(method="POST", http_conn_id=ZORD_OUTCOME_ENGINE_CONN_ID)
    response = hook.run(
        endpoint=FINANCIAL_RECON_ENDPOINT,
        data=json.dumps({
            "tenant_id": tenant_id,
            "connector_id": connector_id,
            "account_id": account_id,
        }),
        headers=_headers(),
    )
    return response.json()
