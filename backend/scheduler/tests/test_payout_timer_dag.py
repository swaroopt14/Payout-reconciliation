"""D26 timer pull: payout/settlement backup timers feed recon's intake, never a PSP.

The payout timer must hit POST /internal/backfill/payouts on recon — the job
that runs the SAME payout-truth processor as payout webhooks (de-dup by
provider payout_id, proven Go-side in
recon/internal/observe/timer_pull_dedup_test.go).
"""

from __future__ import annotations

import json
import os
import sys
import unittest
from datetime import timedelta

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, ".."))
for p in (os.path.join(ROOT, "plugins"), HERE):
    if p not in sys.path:
        sys.path.insert(0, p)

import airflow_stubs  # noqa: E402

TENANT = "11111111-1111-1111-1111-111111111111"
CONNECTOR = "22222222-2222-2222-2222-222222222222"
PSP_HOSTS = ("api.razorpay.com", "razorpay.com/v1", "api.cashfree.com", "api.payu")


class _Ti:
    def __init__(self):
        self.pushed = {}

    def xcom_push(self, key, value):
        self.pushed[key] = value

    def xcom_pull(self, task_ids=None, key=None):
        return self.pushed.get(key)


def _read(path):
    with open(path, encoding="utf-8") as f:
        return f.read()


def _dag_path(name):
    return os.path.join(ROOT, "dags", name)


class TestPayoutTimerDag(unittest.TestCase):
    def setUp(self):
        self.rec = airflow_stubs.install()
        self.rec.calls.clear()
        self.rec.variables = {"razorpay_tenant_id": TENANT, "razorpay_connector_id": CONNECTOR}
        sys.modules.pop("operators.zord_backfill_operator", None)
        self.dag = airflow_stubs.load_dag(_dag_path("razorpay_payout_backfill_dag.py"))

    def test_is_backup_timer_on_own_schedule(self):
        self.assertEqual(self.dag.dag_id, "razorpay_payout_backfill_dag")
        self.assertIn("backup", self.dag.kwargs["tags"])
        sched = self.dag.kwargs["schedule"]
        self.assertIsInstance(sched, timedelta)
        # Must run well inside Razorpay's 24h webhook auto-disable window.
        self.assertLess(sched, timedelta(hours=24))

    def test_timer_posts_to_recon_payout_intake_with_tenant(self):
        ti = _Ti()
        self.dag.tasks["create_payout_backfill_jobs"].python_callable(ti=ti)
        self.assertEqual(len(self.rec.calls), 1)
        call = self.rec.calls[0]
        self.assertEqual(call["conn_id"], "zord_outcome_engine_http")
        self.assertEqual(call["endpoint"], "/internal/backfill/payouts")
        body = json.loads(call["data"])
        self.assertEqual(body["tenant_id"], TENANT)
        self.assertEqual(body["connector_id"], CONNECTOR)
        self.assertEqual(call["headers"]["X-Relay-Tenant-ID"], TENANT)
        self.assertEqual(ti.pushed["job_id"], "job-1")

    def test_lookback_covers_webhook_disable_horizon(self):
        from datetime import datetime
        ti = _Ti()
        self.dag.tasks["create_payout_backfill_jobs"].python_callable(ti=ti)
        body = json.loads(self.rec.calls[0]["data"])
        parse = lambda s: datetime.fromisoformat(s.replace("Z", "+00:00"))
        self.assertGreater(parse(body["window_to"]) - parse(body["window_from"]), timedelta(hours=24))

    def test_wait_falls_back_to_payout_task_xcom(self):
        src = _read(os.path.join(ROOT, "plugins", "operators", "zord_backfill_operator.py"))
        self.assertIn('task_ids="create_payout_backfill_jobs"', src)

    def test_payout_dag_never_calls_psp(self):
        src = _read(_dag_path("razorpay_payout_backfill_dag.py"))
        for host in PSP_HOSTS:
            self.assertNotIn(host, src)


class TestSettlementTimerKeepsGate(unittest.TestCase):
    def setUp(self):
        self.rec = airflow_stubs.install()
        self.rec.calls.clear()
        self.rec.variables = {"razorpay_tenant_id": TENANT, "razorpay_connector_id": CONNECTOR}
        sys.modules.pop("operators.zord_backfill_operator", None)
        self.dag = airflow_stubs.load_dag(_dag_path("razorpay_settlement_backfill_dag.py"))

    def test_banking_day_gate_first(self):
        self.assertIn("banking_day_gate", self.dag.tasks)
        gate = self.dag.tasks["banking_day_gate"]
        self.assertEqual(gate.python_callable.__name__, "timer_should_run")
        self.assertIn(self.dag.tasks["check_outcome_engine_health"], gate.downstream)

    def test_settlement_timer_posts_to_recon(self):
        self.dag.tasks["create_settlement_day_jobs"].python_callable(ti=_Ti())
        self.assertEqual(self.rec.calls[-1]["endpoint"], "/internal/backfill/settlements")


if __name__ == "__main__":
    unittest.main()
