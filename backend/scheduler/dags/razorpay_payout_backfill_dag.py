"""
Razorpay payout timer pull (backup to payout webhooks).

D26: Razorpay switches a webhook off after 24h of failed deliveries, so this
timer pulls payouts on its own schedule, independent of webhooks. It asks
recon (zord-outcome-engine) to run a "payouts" backfill job via
POST /internal/backfill/payouts. Recon pulls the provider and feeds the SAME
payout-truth intake as payout webhooks, which de-duplicates by provider
payout_id, so a payout seen by both is counted once.

BACKUP TIMER ONLY: primary wake stays signal_recon_dag / webhooks.
Never talks to Razorpay APIs directly.

No banking-day gate here: IMPS/UPI payouts move 24x7 (and D16: a pending or
deemed-approved payout can resolve on a holiday). Skipping weekends would
leave a >24h webhook outage uncovered until Monday. The settlement timer
keeps its gate because settlements only land on banking days.
"""


from datetime import datetime, timedelta, timezone
from airflow.sdk import DAG, Variable
from airflow.providers.standard.operators.python import PythonOperator
from airflow.providers.http.sensors.http import HttpSensor

import sys
import os

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), '..')))
sys.path.insert(0, '/opt/airflow/plugins')
sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), '..', 'plugins')))

from operators.zord_backfill_operator import (
    ZORD_OUTCOME_ENGINE_CONN_ID,
    create_and_trigger_backfill,
    wait_for_backfill,
    run_freshness_check,
)

default_args = {
    "owner": "clearline-platform",
    "depends_on_past": False,
    "retries": 2,
    "retry_delay": timedelta(seconds=30),
    "retry_exponential_backoff": True,
}

with DAG(
    dag_id="razorpay_payout_backfill_dag",
    default_args=default_args,
    description="Razorpay payout timer pull via recon (same de-dup intake as webhooks)",
    # BACKUP timer — independent of webhook delivery.
    schedule=timedelta(minutes=30),
    start_date=datetime(2026, 1, 1),
    catchup=False,
    max_active_runs=1,
    tags=["backup", "timer", "clearline", "razorpay", "payouts", "backfill"],
) as dag:

    check_health = HttpSensor(
        task_id="check_outcome_engine_health",
        http_conn_id=ZORD_OUTCOME_ENGINE_CONN_ID,
        endpoint="/v1/health",
        poke_interval=10,
        timeout=60,
        mode="reschedule",
    )

    def _create(**context):
        # Overlapping window: must exceed the 24h webhook auto-disable horizon.
        lookback_hours = int(Variable.get("razorpay_payout_lookback_hours", default="26"))
        now = datetime.now(timezone.utc)
        window_to = now.isoformat().replace("+00:00", "Z")
        window_from = (now - timedelta(hours=lookback_hours)).isoformat().replace("+00:00", "Z")
        return create_and_trigger_backfill(
            resource_type="payouts",
            window_from=window_from,
            window_to=window_to,
            **context,
        )

    create_job = PythonOperator(
        task_id="create_payout_backfill_jobs",
        python_callable=_create,
    )

    wait_job = PythonOperator(
        task_id="wait_for_job_completion",
        python_callable=wait_for_backfill,
    )

    freshness = PythonOperator(
        task_id="run_payout_freshness_check",
        python_callable=run_freshness_check,
    )

    check_health >> create_job >> wait_job >> freshness
