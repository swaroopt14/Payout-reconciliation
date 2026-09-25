"""
signal_recon_dag — EVENT-FIRST Clearline recon wake.

PRIMARY path: Airflow Assets for ERP commit, refund, and bank file.
Timers in razorpay_* / reconciliation_freshness_dag are BACKUP only.

Signals (see plugins/signals/topics.py):
  - erp_commit  ← merchant.books.normalized.v1 / import.completed.v1
                  (outbox; Kafka route TODO — local bridge works today)
  - refund      ← refund.* on payments.ledger.events.v1
  - bank_file   ← bank.statement.received on payments.bank.events.v1

Actions: POST /internal/reconciliation/run (tenant_id + connector_id).
Never calls Razorpay/PSP APIs. Never writes MATCHED or bank cash —
cash-schedule projections stay schedule_projection in Go.

Banking-day gate: weekends + ReferenceIndiaHolidays; non-banking days
are deferred to NextBankingDay (skip branch).
"""

from datetime import datetime, timedelta
from airflow.sdk import DAG
from airflow.providers.standard.operators.python import PythonOperator, BranchPythonOperator
from airflow.providers.standard.operators.empty import EmptyOperator
from airflow.providers.http.sensors.http import HttpSensor

import sys
import os

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..")))
sys.path.insert(0, "/opt/airflow/plugins")

from operators.signal_operator import (
    ZORD_OUTCOME_ENGINE_CONN_ID,
    gate_or_skip,
    run_signal_recon,
    skip_non_banking_day,
)
from signals.assets import signal_schedule

default_args = {
    "owner": "zord-platform",
    "depends_on_past": False,
    "retries": 2,
    "retry_delay": timedelta(minutes=1),
}

_schedule = signal_schedule()

with DAG(
    dag_id="signal_recon_dag",
    default_args=default_args,
    description=(
        "EVENT-FIRST recon wake on ERP commit / refund / bank file Assets. "
        "Timers are backup only."
    ),
    # AssetAny of the three signals when Airflow Assets are available;
    # None = manual / bridge-triggered until Asset watcher is wired.
    schedule=_schedule,
    start_date=datetime(2026, 1, 1),
    catchup=False,
    max_active_runs=3,
    tags=["zord", "clearline", "signal", "event-first", "recon"],
) as dag:

    check_health = HttpSensor(
        task_id="check_outcome_engine_health",
        http_conn_id=ZORD_OUTCOME_ENGINE_CONN_ID,
        endpoint="/v1/health",
        poke_interval=10,
        timeout=60,
        mode="reschedule",
    )

    branch = BranchPythonOperator(
        task_id="banking_day_gate",
        python_callable=gate_or_skip,
    )

    run_recon = PythonOperator(
        task_id="run_signal_recon",
        python_callable=run_signal_recon,
    )

    skip = PythonOperator(
        task_id="skip_non_banking_day",
        python_callable=skip_non_banking_day,
    )

    done = EmptyOperator(
        task_id="done",
        trigger_rule="none_failed_min_one_success",
    )

    check_health >> branch
    branch >> run_recon >> done
    branch >> skip >> done
