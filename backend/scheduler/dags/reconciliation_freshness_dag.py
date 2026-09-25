"""
Reconciliation freshness DAG.

BACKUP TIMER ONLY — primary recon wake is signal_recon_dag on ERP commit,
refund, and bank-file Assets. This 30-minute timer is a safety net only.

Compares API observations against webhook receipts via outcome-engine.
Does not fetch Razorpay.
"""


from datetime import datetime, timedelta
from airflow.sdk import DAG
from airflow.providers.standard.operators.python import PythonOperator, ShortCircuitOperator
from airflow.providers.http.sensors.http import HttpSensor

import sys
import os

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), '..')))
sys.path.insert(0, '/opt/airflow/plugins')
sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), '..', 'plugins')))

from signals.banking_calendar import timer_should_run

from operators.zord_backfill_operator import (
    ZORD_OUTCOME_ENGINE_CONN_ID,
    run_freshness_check,
    run_recon,
)

default_args = {
    "owner": "zord-platform",
    "depends_on_past": False,
    "retries": 1,
    "retry_delay": timedelta(minutes=1),
}

with DAG(
    dag_id="reconciliation_freshness_dag",
    default_args=default_args,
    description="API vs webhook freshness via outcome-engine",
    # BACKUP timer — prefer signal_recon_dag Assets
    schedule=timedelta(minutes=30),
    start_date=datetime(2026, 1, 1),
    catchup=False,
    max_active_runs=1,
    tags=["backup", "timer", "zord", "razorpay", "freshness"],
) as dag:

    # Banking-day gate: weekends + reference holidays skip the whole run,
    # same rule as signal_recon_dag.
    banking_day_gate = ShortCircuitOperator(
        task_id="banking_day_gate",
        python_callable=timer_should_run,
    )

    check_health = HttpSensor(
        task_id="check_outcome_engine_health",
        http_conn_id=ZORD_OUTCOME_ENGINE_CONN_ID,
        endpoint="/v1/health",
        poke_interval=10,
        timeout=60,
        mode="reschedule",
    )

    freshness = PythonOperator(
        task_id="run_freshness",
        python_callable=run_freshness_check,
    )

    recon = PythonOperator(
        task_id="run_recon",
        python_callable=run_recon,
    )

    banking_day_gate >> check_health >> freshness >> recon
