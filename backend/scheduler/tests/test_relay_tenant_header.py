"""Recon internal calls must carry X-Relay-Tenant-ID matching the body tenant.

Recon's relayTenantMustMatch / bindJobTenant answer 403 relay_tenant_required
without it and 403 tenant_mismatch when it differs from body/query tenant_id.
"""

from __future__ import annotations

import json
import os
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, ".."))
for p in (os.path.join(ROOT, "plugins"), HERE):
    if p not in sys.path:
        sys.path.insert(0, p)

import airflow_stubs  # noqa: E402

TENANT = "11111111-1111-1111-1111-111111111111"
CONNECTOR = "22222222-2222-2222-2222-222222222222"


class _Ctx:
    def __init__(self):
        self.pushed = {}

    def xcom_push(self, key, value):
        self.pushed[key] = value

    def xcom_pull(self, task_ids=None, key=None):
        return self.pushed.get(key)


class TestBackfillOperatorTenantHeader(unittest.TestCase):
    def setUp(self):
        self.rec = airflow_stubs.install()
        self.rec.calls.clear()
        self.rec.variables = {
            "razorpay_tenant_id": TENANT,
            "razorpay_connector_id": CONNECTOR,
            "relay_auth_token": "test-only-relay-token",
        }
        self.op = airflow_stubs.fresh_import("operators.zord_backfill_operator")

    def _last(self):
        self.assertTrue(self.rec.calls, "no HTTP call recorded")
        return self.rec.calls[-1]

    def test_headers_include_tenant(self):
        h = self.op._headers(TENANT)
        self.assertEqual(h["X-Relay-Tenant-ID"], TENANT)
        self.assertIn("X-Relay-Token", h)

    def test_headers_default_to_variable_tenant(self):
        self.assertEqual(self.op._headers()["X-Relay-Tenant-ID"], TENANT)

    def test_headers_refuse_empty_tenant(self):
        self.rec.variables["razorpay_tenant_id"] = ""
        with self.assertRaises(ValueError):
            self.op._headers()

    def test_create_backfill_header_matches_body_tenant(self):
        for resource, endpoint in (
            ("payments", "/internal/backfill/payments"),
            ("settlements", "/internal/backfill/settlements"),
            ("payouts", "/internal/backfill/payouts"),
        ):
            ti = _Ctx()
            self.op.create_and_trigger_backfill(resource, "2026-09-24T00:00:00Z", "2026-09-25T00:00:00Z", ti=ti)
            call = self._last()
            body = json.loads(call["data"])
            self.assertEqual(call["endpoint"], endpoint)
            self.assertEqual(call["headers"]["X-Relay-Tenant-ID"], body["tenant_id"])
            self.assertEqual(ti.pushed["job_id"], "job-1")

    def test_unknown_resource_rejected(self):
        with self.assertRaises(ValueError):
            self.op.create_and_trigger_backfill("refunds", "a", "b", ti=_Ctx())

    def test_recon_and_freshness_calls_carry_tenant(self):
        ti = _Ctx()
        ti.pushed["job_id"] = "job-1"
        self.op.run_freshness_check(ti=ti)
        self.assertEqual(self._last()["headers"]["X-Relay-Tenant-ID"], TENANT)
        self.op.run_recon(ti=ti)
        self.assertEqual(self._last()["headers"]["X-Relay-Tenant-ID"], TENANT)


class TestSignalOperatorTenantHeader(unittest.TestCase):
    def setUp(self):
        self.rec = airflow_stubs.install()
        self.op = airflow_stubs.fresh_import("operators.signal_operator")

    def test_header_is_run_body_tenant(self):
        h = self.op._headers(TENANT)
        self.assertEqual(h["X-Relay-Tenant-ID"], TENANT)
        self.assertEqual(h["X-Relay-Instance-ID"], "airflow-signal-recon")

    def test_empty_tenant_rejected(self):
        with self.assertRaises(ValueError):
            self.op._headers("")

    def test_run_uses_body_tenant_for_header(self):
        src_path = os.path.join(ROOT, "plugins", "operators", "signal_operator.py")
        with open(src_path, encoding="utf-8") as f:
            src = f.read()
        self.assertIn('_headers(req["body"]["tenant_id"])', src)


if __name__ == "__main__":
    unittest.main()
