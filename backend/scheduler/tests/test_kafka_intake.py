"""Scheduler Kafka intake with a fake consumer (no broker, no confluent_kafka)."""

from __future__ import annotations

import importlib
import json
import os
import sys
import unittest
from datetime import date

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
PLUGINS = os.path.join(ROOT, "plugins")
if PLUGINS not in sys.path:
    sys.path.insert(0, PLUGINS)

from signals import kafka_intake as ki  # noqa: E402
from signals.banking_calendar import BankingCalendar  # noqa: E402

TENANT = "11111111-1111-1111-1111-111111111111"
CONNECTOR = "22222222-2222-2222-2222-222222222222"
WEEKDAY = date(2026, 9, 23)  # Wednesday
SUNDAY = date(2026, 9, 27)


class FakeMsg:
    def __init__(self, value, topic, headers=None, error=None):
        self._v, self._t, self._h, self._e = value, topic, headers, error

    def value(self):
        return self._v

    def topic(self):
        return self._t

    def headers(self):
        return self._h

    def error(self):
        return self._e


class FakeConsumer:
    def __init__(self, messages):
        self.messages = list(messages)
        self.subscribed = None
        self.committed = []
        self.closed = False

    def subscribe(self, topics):
        self.subscribed = list(topics)

    def poll(self, timeout):
        return self.messages.pop(0) if self.messages else None

    def commit(self, message=None, asynchronous=True):
        self.committed.append(message)

    def close(self):
        self.closed = True


def outbox(event_type, topic, *, payload=None, headers=True, tenant=TENANT):
    body = {"event_id": "evt-1", "event_type": event_type, "payload": json.dumps(payload or {})}
    hdrs = [("event_type", event_type.encode()), ("tenant_id", tenant.encode())] if headers else None
    return FakeMsg(json.dumps(body).encode(), topic, hdrs)


class TestKafkaIntake(unittest.TestCase):
    def _intake(self, msgs, **kw):
        consumer = FakeConsumer(msgs)
        got = []
        intake = ki.SignalKafkaIntake(
            consumer, config={}, on_trigger=got.append,
            default_connector_id=CONNECTOR, poll_timeout=0,
            calendar=BankingCalendar(), when=kw.pop("when", WEEKDAY), **kw)
        return intake, consumer, got

    def test_module_imports_without_confluent_kafka(self):
        self.assertNotIn("confluent_kafka", sys.modules)
        importlib.reload(ki)
        self.assertNotIn("confluent_kafka", sys.modules)

    def test_topics_are_kafka_ready_and_stable(self):
        self.assertEqual(ki.intake_topics(), [
            "payments.bank.events.v1", "payments.ledger.events.v1", "payments.outcome.events.v1"])
        self.assertIn("merchant.books.normalized.v1", ki.intake_event_types())
        self.assertIn("bank.statement.received", ki.intake_event_types())

    def test_consumer_config_env(self):
        cfg = ki.consumer_config_from_env({"SCHEDULER_KAFKA_BOOTSTRAP_SERVERS": "k:9092"})
        self.assertEqual(cfg["bootstrap.servers"], "k:9092")
        self.assertFalse(cfg["enable.auto.commit"])
        self.assertEqual(cfg["auto.offset.reset"], "latest")
        self.assertEqual(ki.consumer_config_from_env({"KAFKA_BROKERS": "b:1"})["bootstrap.servers"], "b:1")

    def test_decode_relay_outbox_with_headers(self):
        m = outbox("merchant.books.normalized.v1", "payments.outcome.events.v1",
                   payload={"account_id": "acc-1"})
        d = ki.decode_message(m.value(), topic=m.topic(), headers=m.headers(), default_connector_id=CONNECTOR)
        self.assertEqual(d["event_type"], "merchant.books.normalized.v1")
        self.assertEqual(d["tenant_id"], TENANT)
        self.assertEqual(d["connector_id"], CONNECTOR)
        self.assertEqual(d["account_id"], "acc-1")

    def test_signals_trigger_only_recon_run(self):
        msgs = [
            outbox("bank.statement.received", "payments.bank.events.v1"),
            outbox("merchant.books.normalized.v1", "payments.outcome.events.v1"),
            FakeMsg(json.dumps({"provider_event_type": "refund.processed", "tenant_id": TENANT,
                                "connector_id": CONNECTOR}).encode(), "payments.ledger.events.v1"),
        ]
        intake, consumer, got = self._intake(msgs)
        n = intake.run(max_idle_polls=1)
        self.assertEqual(n, 3)
        self.assertEqual(consumer.subscribed, ki.intake_topics())
        self.assertEqual(sorted(p["signal"] for p in got), ["bank_file", "erp_commit", "refund"])
        for p in got:
            self.assertEqual(p["action"]["endpoint"], "/internal/reconciliation/run")
            self.assertEqual(p["action"]["body"]["tenant_id"], TENANT)
            self.assertNotIn("razorpay.com", json.dumps(p))
        self.assertEqual(len(consumer.committed), 3)
        self.assertTrue(consumer.closed)

    def test_invalid_messages_skipped_but_committed(self):
        msgs = [
            FakeMsg(b"not json", "payments.bank.events.v1"),
            FakeMsg(b"", "payments.bank.events.v1"),
            outbox("bank.statement.received", "payments.bank.events.v1", headers=False),  # no tenant
            outbox("unrelated.event", "payments.outcome.events.v1"),
        ]
        intake, consumer, got = self._intake(msgs)
        self.assertEqual(intake.run(max_idle_polls=1), 4)
        self.assertEqual(got, [])
        self.assertEqual(intake.skipped, 4)
        self.assertEqual(len(consumer.committed), 4)

    def test_non_banking_day_defers(self):
        intake, consumer, got = self._intake(
            [outbox("bank.statement.received", "payments.bank.events.v1")], when=SUNDAY)
        intake.run(max_messages=1)
        self.assertEqual(got[0]["status"], "deferred")
        self.assertIsNone(got[0]["action"])

    def test_run_stops_on_idle_and_closes(self):
        intake, consumer, _ = self._intake([])
        self.assertEqual(intake.run(max_idle_polls=3), 0)
        self.assertTrue(consumer.closed)

    def test_consumer_error_is_skipped(self):
        intake, consumer, got = self._intake([FakeMsg(None, "t", error="broker down")])
        intake.run(max_messages=1)
        self.assertEqual(got, [])


if __name__ == "__main__":
    unittest.main()
