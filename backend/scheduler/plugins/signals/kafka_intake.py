"""Kafka intake for the Clearline signal scheduler (replaces the stub wiring).

Consumes the kafka_ready topics from ``signals.topics.SIGNAL_SOURCES``:

  payments.outcome.events.v1  (merchant.books.normalized.v1, import.completed.v1)
  payments.ledger.events.v1   (provider observations, refund.*)
  payments.bank.events.v1     (bank.statement.received)

``merchant.books.normalized.v1`` is an *event type* that relay routes onto
``payments.outcome.events.v1`` (relay config route_allow_list); the consumer
subscribes to the topic and filters by event_type. Topic names are taken
from topics.py and never renamed here.

Each message is decoded (relay publishes the OutboxEvent JSON with a nested
``payload`` plus ``event_type``/``tenant_id`` Kafka headers) and handed to
``bridge.handle_inbound_event`` -> ``trigger_signal``. The resulting trigger
payload goes to an injectable ``on_trigger`` callback. Nothing here calls a
PSP: the only downstream action is the pinned recon run built by
``bridge.build_run_request`` (via signal_recon_dag).

confluent_kafka is imported lazily so this module (and the unit tests) import
fine where the library is not installed.
"""

from __future__ import annotations

import json
import logging
import os
from typing import Any, Callable, Dict, Iterable, List, Mapping, Optional

from .bridge import handle_inbound_event
from .topics import SIGNAL_SOURCES

log = logging.getLogger(__name__)

DEFAULT_GROUP_ID = "clearline-signal-scheduler"
DEFAULT_BOOTSTRAP = "broker1:9092,broker2:9093,broker3:9094"


def intake_topics(sources: Mapping[str, Mapping[str, Any]] = SIGNAL_SOURCES) -> List[str]:
    """Unique, sorted topics for every kafka_ready signal source."""
    return sorted({str(s["topic"]) for s in sources.values() if s.get("kafka_ready")})


def intake_event_types(sources: Mapping[str, Mapping[str, Any]] = SIGNAL_SOURCES) -> List[str]:
    out: List[str] = []
    for s in sources.values():
        if s.get("kafka_ready"):
            out.extend(s.get("event_types") or ())
    return sorted(set(out))


def consumer_config_from_env(env: Optional[Mapping[str, str]] = None) -> Dict[str, Any]:
    env = os.environ if env is None else env
    bootstrap = (
        env.get("SCHEDULER_KAFKA_BOOTSTRAP_SERVERS")
        or env.get("KAFKA_BROKERS")
        or DEFAULT_BOOTSTRAP
    )
    cfg: Dict[str, Any] = {
        "bootstrap.servers": bootstrap,
        "group.id": env.get("SCHEDULER_KAFKA_GROUP_ID") or DEFAULT_GROUP_ID,
        # Wake on new data only; a fresh group must not replay history.
        "auto.offset.reset": env.get("SCHEDULER_KAFKA_AUTO_OFFSET_RESET") or "latest",
        # Commit after the trigger is handed off (at-least-once; recon runs
        # are idempotent per tenant+connector).
        "enable.auto.commit": False,
    }
    user = env.get("SCHEDULER_KAFKA_SASL_USERNAME")
    if user:
        cfg.update({
            "security.protocol": env.get("SCHEDULER_KAFKA_SECURITY_PROTOCOL") or "SASL_SSL",
            "sasl.mechanisms": env.get("SCHEDULER_KAFKA_SASL_MECHANISM") or "SCRAM-SHA-512",
            "sasl.username": user,
            "sasl.password": env.get("SCHEDULER_KAFKA_SASL_PASSWORD", ""),
        })
    return cfg


def _load_consumer_class():
    try:
        from confluent_kafka import Consumer  # type: ignore
    except ImportError as exc:  # pragma: no cover - exercised only without the lib
        raise RuntimeError(
            "confluent-kafka is not installed; add it to the scheduler image "
            "(backend/scheduler/requirements.txt) or inject a consumer"
        ) from exc
    return Consumer


def _as_text(v: Any) -> str:
    if v is None:
        return ""
    if isinstance(v, (bytes, bytearray)):
        return bytes(v).decode("utf-8", "replace")
    return str(v)


def _headers_dict(raw: Optional[Iterable]) -> Dict[str, str]:
    out: Dict[str, str] = {}
    for item in raw or ():
        try:
            k, v = item
        except (TypeError, ValueError):
            continue
        out[_as_text(k).lower().replace("-", "_")] = _as_text(v)
    return out


def decode_message(value: Any, *, topic: str = "", headers: Optional[Iterable] = None,
                   default_connector_id: str = "") -> Optional[Dict[str, Any]]:
    """Decode one Kafka record into the flat dict handle_inbound_event expects."""
    text = _as_text(value).strip()
    if not text:
        return None
    try:
        body = json.loads(text)
    except ValueError:
        return None
    if not isinstance(body, dict):
        return None
    nested = body.get("payload")
    if isinstance(nested, str):
        try:
            nested = json.loads(nested)
        except ValueError:
            nested = None
    nested = nested if isinstance(nested, dict) else {}
    hdr = _headers_dict(headers)

    def pick(key: str) -> str:
        for src in (body, nested, hdr):
            v = src.get(key)
            if v not in (None, ""):
                return _as_text(v)
        return ""

    return {
        "event_type": pick("event_type"),
        "provider_event_type": pick("provider_event_type"),
        "tenant_id": pick("tenant_id"),
        "connector_id": pick("connector_id") or default_connector_id,
        "account_id": pick("account_id"),
        "topic": topic or pick("topic"),
    }


TriggerCallback = Callable[[Dict[str, Any]], Any]


class SignalKafkaIntake:
    """Poll loop: Kafka record -> decode -> handle_inbound_event -> on_trigger."""

    def __init__(self, consumer: Any = None, *, config: Optional[Dict[str, Any]] = None,
                 topics: Optional[List[str]] = None, on_trigger: Optional[TriggerCallback] = None,
                 default_connector_id: Optional[str] = None, poll_timeout: float = 1.0,
                 calendar=None, when=None):
        self.topics = list(topics) if topics is not None else intake_topics()
        self.config = config if config is not None else consumer_config_from_env()
        self._consumer = consumer
        self.on_trigger = on_trigger
        self.default_connector_id = (
            default_connector_id
            if default_connector_id is not None
            else os.environ.get("SCHEDULER_DEFAULT_CONNECTOR_ID", "")
        )
        self.poll_timeout = poll_timeout
        self.calendar = calendar
        self.when = when
        self.triggered: List[Dict[str, Any]] = []
        self.skipped = 0
        self._subscribed = False

    @property
    def consumer(self):
        if self._consumer is None:
            self._consumer = _load_consumer_class()(self.config)
        return self._consumer

    def subscribe(self) -> None:
        if not self._subscribed:
            self.consumer.subscribe(self.topics)
            self._subscribed = True

    def process_message(self, msg: Any) -> Optional[Dict[str, Any]]:
        err = msg.error() if hasattr(msg, "error") else None
        if err:
            log.warning("kafka intake: consumer error %s", err)
            return None
        decoded = decode_message(
            msg.value(),
            topic=_as_text(msg.topic()) if hasattr(msg, "topic") else "",
            headers=msg.headers() if hasattr(msg, "headers") else None,
            default_connector_id=self.default_connector_id,
        )
        if not decoded:
            self.skipped += 1
            return None
        try:
            payload = handle_inbound_event(decoded, when=self.when, calendar=self.calendar)
        except ValueError as exc:  # missing tenant/connector: skip, never block the partition
            log.warning("kafka intake: skipped %s (%s)", decoded.get("event_type"), exc)
            self.skipped += 1
            return None
        if payload is None:
            self.skipped += 1
            return None
        self.triggered.append(payload)
        if self.on_trigger is not None:
            self.on_trigger(payload)
        return payload

    def run(self, *, max_messages: Optional[int] = None, max_idle_polls: Optional[int] = None,
            stop: Callable[[], bool] = lambda: False) -> int:
        """Consume until stop() / max_messages / max_idle_polls. Returns records handled."""
        self.subscribe()
        handled = idle = 0
        try:
            while not stop():
                msg = self.consumer.poll(self.poll_timeout)
                if msg is None:
                    idle += 1
                    if max_idle_polls is not None and idle >= max_idle_polls:
                        break
                    continue
                idle = 0
                self.process_message(msg)
                handled += 1
                try:
                    self.consumer.commit(message=msg, asynchronous=False)
                except TypeError:
                    self.consumer.commit(msg)
                if max_messages is not None and handled >= max_messages:
                    break
        finally:
            self.consumer.close()
        return handled


def main() -> None:  # pragma: no cover - process entry point
    logging.basicConfig(level=logging.INFO)
    intake = SignalKafkaIntake(on_trigger=lambda p: log.info("signal trigger %s", json.dumps(p, default=str)))
    log.info("kafka intake subscribing to %s", intake.topics)
    intake.run()


if __name__ == "__main__":  # pragma: no cover
    main()
