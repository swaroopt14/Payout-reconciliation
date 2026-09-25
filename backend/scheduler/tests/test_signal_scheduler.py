"""Focused tests for Clearline event-first signal scheduler."""

from __future__ import annotations

import os
import sys
import unittest
from datetime import date

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
PLUGINS = os.path.join(ROOT, "plugins")
if PLUGINS not in sys.path:
    sys.path.insert(0, PLUGINS)

from signals.banking_calendar import (  # noqa: E402
    BankingCalendar,
    default_banking_calendar,
    gate_banking_day,
    reference_india_holidays,
)
from signals.bridge import handle_inbound_event, resolve_signal, trigger_signal  # noqa: E402
from signals.topics import (  # noqa: E402
    ALL_SIGNALS,
    ASSET_URI_BANK_FILE,
    ASSET_URI_ERP_COMMIT,
    ASSET_URI_REFUND,
    EVENT_BANK_STATEMENT_RECEIVED,
    EVENT_IMPORT_COMPLETED,
    EVENT_MERCHANT_BOOKS_NORMALIZED,
    SIGNAL_BANK_FILE,
    SIGNAL_ERP_COMMIT,
    SIGNAL_REFUND,
    SIGNAL_SOURCES,
    TOPIC_BANK_EVENTS,
    TOPIC_OBSERVATION,
)


class TestSignalTriggersExist(unittest.TestCase):
    def _read(self, rel):
        with open(os.path.join(ROOT, rel), encoding="utf-8") as f:
            return f.read()

    def test_three_signal_constants(self):
        self.assertEqual(
            set(ALL_SIGNALS),
            {SIGNAL_ERP_COMMIT, SIGNAL_REFUND, SIGNAL_BANK_FILE},
        )

    def test_dag_wires_all_three_signals(self):
        body = self._read("dags/signal_recon_dag.py")
        self.assertIn("signal_recon_dag", body)
        self.assertIn("EVENT-FIRST", body)
        self.assertIn("erp_commit", body)
        self.assertIn("refund", body)
        self.assertIn("bank file", body.lower().replace("_", " "))
        self.assertIn("signal_schedule", body)
        self.assertIn("banking_day_gate", body)
        self.assertIn("run_signal_recon", body)
        self.assertIn("skip_non_banking_day", body)

    def test_assets_define_three_uris(self):
        assets = self._read("plugins/signals/assets.py")
        topics = self._read("plugins/signals/topics.py")
        self.assertIn("AssetAny", assets)
        self.assertIn("ASSET_URI_ERP_COMMIT", assets)
        self.assertIn("ASSET_URI_REFUND", assets)
        self.assertIn("ASSET_URI_BANK_FILE", assets)
        self.assertIn(ASSET_URI_ERP_COMMIT, topics)
        self.assertIn(ASSET_URI_REFUND, topics)
        self.assertIn(ASSET_URI_BANK_FILE, topics)

    def test_resolve_erp_commit_events(self):
        self.assertEqual(
            resolve_signal(event_type=EVENT_MERCHANT_BOOKS_NORMALIZED),
            SIGNAL_ERP_COMMIT,
        )
        self.assertEqual(
            resolve_signal(event_type=EVENT_IMPORT_COMPLETED),
            SIGNAL_ERP_COMMIT,
        )

    def test_resolve_refund_provider_events(self):
        self.assertEqual(
            resolve_signal(provider_event_type="refund.created"),
            SIGNAL_REFUND,
        )
        self.assertEqual(
            resolve_signal(provider_event_type="refund.processed", topic=TOPIC_OBSERVATION),
            SIGNAL_REFUND,
        )

    def test_resolve_bank_file(self):
        self.assertEqual(
            resolve_signal(event_type=EVENT_BANK_STATEMENT_RECEIVED),
            SIGNAL_BANK_FILE,
        )
        self.assertEqual(
            resolve_signal(topic=TOPIC_BANK_EVENTS),
            SIGNAL_BANK_FILE,
        )

    def test_trigger_payload_for_each_signal(self):
        for signal in ALL_SIGNALS:
            payload = trigger_signal(
                signal,
                tenant_id="t1",
                connector_id="c1",
                when=date(2026, 9, 24),  # Thursday
            )
            self.assertEqual(payload["signal"], signal)
            self.assertEqual(payload["status"], "ready")
            self.assertEqual(payload["kind"], "schedule_projection")
            self.assertEqual(payload["action"]["endpoint"], "/internal/reconciliation/run")
            self.assertEqual(payload["action"]["body"]["tenant_id"], "t1")
            self.assertEqual(payload["action"]["body"]["connector_id"], "c1")

    def test_handle_inbound_erp_and_bank(self):
        erp = handle_inbound_event(
            {
                "event_type": EVENT_MERCHANT_BOOKS_NORMALIZED,
                "tenant_id": "t1",
                "connector_id": "c1",
            },
            when=date(2026, 9, 24),
        )
        self.assertIsNotNone(erp)
        self.assertEqual(erp["signal"], SIGNAL_ERP_COMMIT)

        bank = handle_inbound_event(
            {
                "event_type": EVENT_BANK_STATEMENT_RECEIVED,
                "topic": TOPIC_BANK_EVENTS,
                "tenant_id": "t1",
                "connector_id": "c1",
            },
            when=date(2026, 9, 24),
        )
        self.assertEqual(bank["signal"], SIGNAL_BANK_FILE)


class TestTimersAreBackup(unittest.TestCase):
    def _read(self, rel):
        with open(os.path.join(ROOT, rel), encoding="utf-8") as f:
            return f.read()

    def test_timer_dags_marked_backup(self):
        files = [
            "dags/razorpay_payment_backfill_dag.py",
            "dags/razorpay_settlement_backfill_dag.py",
            "dags/reconciliation_freshness_dag.py",
        ]
        for rel in files:
            body = self._read(rel)
            self.assertIn("BACKUP TIMER ONLY", body)
            self.assertIn("signal_recon_dag", body)
            self.assertIn('"backup"', body)
            self.assertIn('"timer"', body)
            self.assertIn("# BACKUP timer", body)

    def test_signal_dag_claims_event_first_primary(self):
        body = self._read("dags/signal_recon_dag.py")
        self.assertIn("EVENT-FIRST", body)
        self.assertIn("backup", body.lower())


class TestBankingDayGate(unittest.TestCase):
    def test_weekend_not_banking(self):
        cal = default_banking_calendar()
        self.assertFalse(cal.is_banking_day(date(2026, 9, 26)))  # Sat
        self.assertFalse(cal.is_banking_day(date(2026, 9, 27)))  # Sun
        self.assertTrue(cal.is_banking_day(date(2026, 9, 24)))  # Thu

    def test_reference_holiday(self):
        cal = default_banking_calendar()
        # Republic Day 2026 is Monday
        self.assertFalse(cal.is_banking_day(date(2026, 1, 26)))
        self.assertEqual(cal.next_banking_day(date(2026, 1, 26)), date(2026, 1, 27))

    def test_next_banking_day_same_or_next(self):
        cal = BankingCalendar(holidays=reference_india_holidays(2026))
        self.assertEqual(cal.next_banking_day(date(2026, 9, 24)), date(2026, 9, 24))
        self.assertEqual(cal.next_banking_day(date(2026, 9, 26)), date(2026, 9, 28))

    def test_gate_defers_non_banking(self):
        gate = gate_banking_day(date(2026, 9, 26))  # Saturday
        self.assertFalse(gate["allow"])
        self.assertEqual(gate["deferred_to"], "2026-09-28")
        self.assertEqual(gate["reason"], "non_banking_day")

    def test_trigger_defers_on_holiday_without_running_action(self):
        # Inject stub calendar — do not reimplement holiday money logic.
        cal = BankingCalendar(holidays={"2026-09-24": "stub-holiday"})
        payload = trigger_signal(
            SIGNAL_REFUND,
            tenant_id="t1",
            connector_id="c1",
            when=date(2026, 9, 24),
            calendar=cal,
        )
        self.assertEqual(payload["status"], "deferred")
        self.assertIsNone(payload["action"])
        self.assertEqual(payload["banking_gate"]["deferred_to"], "2026-09-25")

    def test_erp_kafka_ready_via_relay_allow_list(self):
        src = SIGNAL_SOURCES[SIGNAL_ERP_COMMIT]
        self.assertTrue(src["kafka_ready"])
        self.assertIsNone(src["todo"])
        self.assertIn(EVENT_MERCHANT_BOOKS_NORMALIZED, src["event_types"])
        self.assertTrue(SIGNAL_SOURCES[SIGNAL_REFUND]["kafka_ready"])
        self.assertTrue(SIGNAL_SOURCES[SIGNAL_BANK_FILE]["kafka_ready"])


class TestNoPspUrlsInSignalCode(unittest.TestCase):
    def _read(self, rel):
        with open(os.path.join(ROOT, rel), encoding="utf-8") as f:
            return f.read()

    def test_no_razorpay_urls(self):
        files = [
            "dags/signal_recon_dag.py",
            "plugins/operators/signal_operator.py",
            "plugins/signals/bridge.py",
            "plugins/signals/topics.py",
            "plugins/signals/banking_calendar.py",
            "plugins/signals/assets.py",
            "plugins/operators/zord_backfill_operator.py",
        ]
        for rel in files:
            body = self._read(rel)
            self.assertNotIn("api.razorpay.com", body, rel)
            self.assertNotIn("https://razorpay.com", body, rel)

    def test_signal_operator_uses_internal_recon_only(self):
        body = self._read("plugins/operators/signal_operator.py")
        self.assertIn("/internal/reconciliation/run", body)
        self.assertIn("X-Relay-Token", body)
        self.assertNotIn("/v1/payments", body)


if __name__ == "__main__":
    unittest.main()


class TestRunRequestPinned(unittest.TestCase):
    def test_endpoint_and_body_are_pinned(self):
        from signals.bridge import build_run_request, RECON_RUN_ENDPOINT
        payload = {
            "tenant_id": "t1", "connector_id": "c1", "account_id": "a1",
            "action": {"endpoint": "/internal/recon/results", "body": {"status": "MATCHED"}},
            "payout_ids": ["po_1"], "batch_id": "b1", "status": "MATCHED",
        }
        req = build_run_request(payload)
        self.assertEqual(RECON_RUN_ENDPOINT, "/internal/reconciliation/run")
        self.assertEqual(req["endpoint"], RECON_RUN_ENDPOINT)
        self.assertEqual(req["method"], "POST")
        self.assertEqual(req["body"], {"tenant_id": "t1", "connector_id": "c1", "account_id": "a1"})

    def test_requires_scope(self):
        from signals.bridge import build_run_request
        with self.assertRaises(ValueError):
            build_run_request({"tenant_id": "t1"})

    def test_operator_has_no_other_endpoints(self):
        import os, re
        src = open(os.path.join(os.path.dirname(__file__), "..", "plugins", "operators", "signal_operator.py")).read()
        paths = set(re.findall(r'"(/internal/[^"]*)"', src))
        self.assertEqual(paths, set())  # only via RECON_RUN_ENDPOINT import
        self.assertNotIn('action.get("endpoint")', src)


class TestTimerBankingDayGate(unittest.TestCase):
    TIMER_DAGS = (
        "dags/razorpay_payment_backfill_dag.py",
        "dags/razorpay_settlement_backfill_dag.py",
        "dags/reconciliation_freshness_dag.py",
    )

    def test_timer_dags_defer_on_holiday(self):
        from datetime import datetime, timezone
        from signals.banking_calendar import timer_should_run

        class _TI:
            def __init__(self):
                self.xcom = {}

            def xcom_push(self, key, value):
                self.xcom[key] = value

        # Republic Day (Monday) is a reference holiday: timer run is skipped.
        ti = _TI()
        self.assertFalse(timer_should_run(logical_date=datetime(2026, 1, 26, 6, tzinfo=timezone.utc), ti=ti))
        self.assertEqual(ti.xcom["banking_gate"]["deferred_to"], "2026-01-27")
        # 25 Jan 20:00 UTC is already 26 Jan in India: still deferred.
        self.assertFalse(timer_should_run(logical_date=datetime(2026, 1, 25, 20, tzinfo=timezone.utc)))
        # Saturday defers; an ordinary Thursday runs.
        self.assertFalse(timer_should_run(logical_date=datetime(2026, 9, 26, 6, tzinfo=timezone.utc)))
        self.assertTrue(timer_should_run(logical_date=datetime(2026, 9, 24, 6, tzinfo=timezone.utc)))
        # Stub calendar honoured.
        cal = BankingCalendar(holidays={"2026-09-24": "stub-holiday"})
        self.assertFalse(timer_should_run(logical_date=datetime(2026, 9, 24, 6, tzinfo=timezone.utc), banking_calendar=cal))

        # Every timer DAG gates first, before health/backfill/recon.
        for rel in self.TIMER_DAGS:
            with open(os.path.join(ROOT, rel), encoding="utf-8") as f:
                body = f.read()
            self.assertIn("ShortCircuitOperator", body, rel)
            self.assertIn("python_callable=timer_should_run", body, rel)
            self.assertIn("banking_day_gate >> check_health", body, rel)


class TestInboundRefund(unittest.TestCase):
    def test_handle_inbound_refund(self):
        msg = {
            "provider_event_type": "refund.processed",
            "topic": TOPIC_OBSERVATION,
            "tenant_id": "t1",
            "connector_id": "c1",
            "account_id": "a1",
        }
        out = handle_inbound_event(msg, when=date(2026, 9, 24))
        self.assertIsNotNone(out)
        self.assertEqual(out["signal"], SIGNAL_REFUND)
        self.assertEqual(out["status"], "ready")
        self.assertEqual(out["topic"], TOPIC_OBSERVATION)
        self.assertEqual(out["asset_uri"], ASSET_URI_REFUND)
        self.assertEqual(out["action"]["endpoint"], "/internal/reconciliation/run")
        self.assertEqual(out["action"]["body"], {"tenant_id": "t1", "connector_id": "c1", "account_id": "a1"})
        self.assertEqual(out["kind"], "schedule_projection")

        # Same refund on Republic Day defers with no action.
        held = handle_inbound_event(msg, when=date(2026, 1, 26))
        self.assertEqual(held["status"], "deferred")
        self.assertIsNone(held["action"])

        # Non-refund observation does not wake recon.
        self.assertIsNone(handle_inbound_event({"provider_event_type": "payment.captured", "topic": TOPIC_OBSERVATION, "tenant_id": "t1", "connector_id": "c1"}))


class TestHolidayParityWithGo(unittest.TestCase):
    """Fails if Python ReferenceIndiaHolidays drifts from the Go source of truth."""

    GO_FILE = os.path.join(ROOT, "..", "recon", "internal", "recon", "banking_calendar.go")

    def _go_source(self):
        with open(self.GO_FILE, encoding="utf-8") as f:
            return f.read()

    def _go_rows(self):
        import re
        src = self._go_source()
        body = src[src.index("func ReferenceIndiaHolidays"):]
        body = body[: body.index("\n}\n")]
        rows = re.findall(r'time\.Date\(y,\s*(\d+),\s*(\d+),[^)]*\)\.Format\("2006-01-02"\)\]\s*=\s*"([^"]+)"', body)
        self.assertTrue(rows, "could not parse Go ReferenceIndiaHolidays")
        return {(int(m), int(d), name) for m, d, name in rows}

    def _go_default_years(self):
        import re
        src = self._go_source()
        m = re.search(r"func DefaultBankingCalendar\(\).*?ReferenceIndiaHolidays\(([\d,\s]+)\)", src, re.S)
        self.assertIsNotNone(m, "could not parse Go DefaultBankingCalendar years")
        return tuple(int(x) for x in m.group(1).split(","))

    def test_holiday_rows_match_go(self):
        from signals.banking_calendar import REFERENCE_INDIA_HOLIDAY_ROWS
        py = {(m, d, name) for (m, d, name, _src) in REFERENCE_INDIA_HOLIDAY_ROWS}
        self.assertEqual(py, self._go_rows())

    def test_default_years_match_go(self):
        from signals.banking_calendar import DEFAULT_CALENDAR_YEARS
        self.assertEqual(DEFAULT_CALENDAR_YEARS, self._go_default_years())
        years = self._go_default_years()
        expected = {f"{y:04d}-{m:02d}-{d:02d}": n for y in years for (m, d, n) in self._go_rows()}
        self.assertEqual(default_banking_calendar().holidays, expected)

    def test_every_row_has_source_label(self):
        from signals.banking_calendar import HOLIDAY_SOURCES, reference_india_holiday_rows
        rows = reference_india_holiday_rows(2026)
        self.assertEqual(len(rows), 3)
        for r in rows:
            self.assertIn(r["source"], HOLIDAY_SOURCES, r)
            self.assertEqual(r["source"], "rbi_list", r)  # all three are national RBI holidays


class TestCalendarLocationParityWithGo(unittest.TestCase):
    """Go DefaultBankingCalendar uses Asia/Kolkata; Python must agree."""

    GO_DIR = os.path.join(ROOT, "..", "recon")
    GO_FILE = os.path.join(GO_DIR, "internal", "recon", "banking_calendar.go")
    # 01:00 IST on Republic Day 2026 (a Monday) = 2026-01-25T19:30:00Z.
    EXPECTED = ("2026-01-26", False, "2026-01-27")

    def _py(self, when):
        cal = default_banking_calendar()
        return (cal.civil_date(when).isoformat(), cal.is_banking_day(when), cal.next_banking_day(when).isoformat())

    def test_location_matches_go_source(self):
        import re
        from signals.banking_calendar import CALENDAR_LOCATION
        with open(self.GO_FILE, encoding="utf-8") as f:
            src = f.read()
        self.assertIn('LoadLocation("Asia/Kolkata")', src)
        m = re.search(r"func DefaultBankingCalendar\(\).*?Location:\s*(\w+)", src, re.S)
        self.assertIsNotNone(m)
        self.assertEqual(m.group(1), "istLocation")
        self.assertEqual(CALENDAR_LOCATION, "Asia/Kolkata")

    def test_republic_day_0100_ist_python(self):
        from datetime import datetime, timedelta, timezone
        ist = timezone(timedelta(hours=5, minutes=30))
        self.assertEqual(self._py(datetime(2026, 1, 26, 1, tzinfo=ist)), self.EXPECTED)
        self.assertEqual(self._py(datetime(2026, 1, 25, 19, 30, tzinfo=timezone.utc)), self.EXPECTED)
        self.assertEqual(self._py("2026-01-26T01:00:00+05:30"), self.EXPECTED)
        self.assertEqual(self._py("2026-01-25T19:30:00Z"), self.EXPECTED)

    def test_republic_day_0100_ist_matches_go_runtime(self):
        """Runs the real Go calendar when a Go toolchain is present."""
        import shutil, subprocess, tempfile
        if not shutil.which("go"):
            self.skipTest("go toolchain not on PATH; source-level parity still checked")
        probe = tempfile.mkdtemp(prefix="_parityprobe", dir=self.GO_DIR)
        try:
            with open(os.path.join(probe, "main.go"), "w", encoding="utf-8") as f:
                f.write(
                    'package main\n\nimport (\n\t"fmt"\n\t"time"\n\n\t"zord-outcome-engine/internal/recon"\n)\n\n'
                    "func main() {\n\tc := recon.DefaultBankingCalendar()\n"
                    "\tin := time.Date(2026, 1, 26, 1, 0, 0, 0, recon.ISTLocation())\n"
                    '\tfmt.Println(c.Location.String(), c.CivilDate(in).Format(time.RFC3339), c.IsBankingDay(in), c.NextBankingDay(in).Format(time.RFC3339))\n}\n'
                )
            out = subprocess.run(
                ["go", "run", "./" + os.path.basename(probe)],
                cwd=self.GO_DIR, capture_output=True, text=True, timeout=120,
            )
            self.assertEqual(out.returncode, 0, out.stderr)
            loc, civil, banking, roll = out.stdout.split()
        finally:
            shutil.rmtree(probe, ignore_errors=True)
        self.assertEqual(loc, "Asia/Kolkata")
        # Go serializes IST midnight with +05:30; Python must read it back to the same date.
        self.assertTrue(civil.endswith("+05:30") and roll.endswith("+05:30"), (civil, roll))
        go = (default_banking_calendar().civil_date(civil).isoformat(), banking == "true",
              default_banking_calendar().civil_date(roll).isoformat())
        self.assertEqual(go, self.EXPECTED)
        self.assertEqual(go, self._py(civil))

    def test_scheduler_accepts_ist_offset_dates(self):
        # Go cash-schedule as_of / roll dates now serialize as IST midnight.
        g = gate_banking_day("2026-01-26T00:00:00+05:30")
        self.assertFalse(g["allow"])
        self.assertEqual((g["as_of"], g["deferred_to"]), ("2026-01-26", "2026-01-27"))
        self.assertTrue(gate_banking_day("2026-01-27T00:00:00+05:30")["allow"])
        # Same instant written in UTC: IST midnight 27 Jan is 26 Jan 18:30Z.
        self.assertTrue(gate_banking_day("2026-01-26T18:30:00Z")["allow"])
        # DAG conf as_of and inbound events take the same path.
        p = trigger_signal(SIGNAL_REFUND, tenant_id="t1", connector_id="c1", when="2026-01-26T00:00:00+05:30")
        self.assertEqual(p["status"], "deferred")
        self.assertEqual(p["banking_gate"]["deferred_to"], "2026-01-27")
