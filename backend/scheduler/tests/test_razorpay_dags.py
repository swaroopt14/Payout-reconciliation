import os
import unittest


ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))


class TestRazorpayDags(unittest.TestCase):
    def _read(self, rel):
        with open(os.path.join(ROOT, rel), encoding="utf-8") as f:
            return f.read()

    def test_dags_call_outcome_engine_not_razorpay(self):
        files = [
            "dags/razorpay_payment_backfill_dag.py",
            "dags/razorpay_settlement_backfill_dag.py",
            "dags/reconciliation_freshness_dag.py",
            "dags/signal_recon_dag.py",
            "plugins/operators/zord_backfill_operator.py",
            "plugins/operators/signal_operator.py",
        ]
        for rel in files:
            body = self._read(rel)
            self.assertNotIn("api.razorpay.com", body)
            self.assertNotIn("https://razorpay.com", body)

    def test_operator_sends_relay_token_and_internal_paths(self):
        body = self._read("plugins/operators/zord_backfill_operator.py")
        self.assertIn("X-Relay-Token", body)
        self.assertIn("/internal/backfill/payments", body)
        self.assertIn("/internal/backfill/settlements", body)
        self.assertIn("/internal/freshness/{job_id}", body)
        self.assertIn("/internal/recon/run", body)
        self.assertIn("/internal/reconciliation/run", body)
        self.assertIn("zord_outcome_engine_http", body)

    def test_freshness_dag_runs_recon_after_backfill(self):
        body = self._read("dags/reconciliation_freshness_dag.py")
        self.assertIn("run_recon", body)
        self.assertIn("check_health >> freshness >> recon", body)

    def test_payment_dag_has_bounded_window(self):
        body = self._read("dags/razorpay_payment_backfill_dag.py")
        self.assertIn("lookback_hours", body)
        self.assertIn("create_and_trigger_backfill", body)
        self.assertIn("wait_for_backfill", body)
        self.assertIn("run_freshness_check", body)


if __name__ == "__main__":
    unittest.main()
