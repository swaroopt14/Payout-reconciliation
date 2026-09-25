"""Item 3: recon consumes the topic relay publishes dispatch events on.

relay config.yaml relay_loop.dispatch_events_topic == relay config.go default
== relay compose kafka-init topic == recon compose KAFKA_TOPIC == recon main.go
default. Topic names are stable; this only proves the wiring (L2).
"""

from __future__ import annotations

import os
import re
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from _repo import compose_env, read  # noqa: E402

EXPECTED = "payments.dispatch.events.v1"


def relay_yaml_dispatch_topic() -> str:
    in_loop = False
    for line in read("backend/relay/config.yaml").splitlines():
        if re.match(r"^relay_loop:\s*$", line):
            in_loop = True
            continue
        if in_loop and re.match(r"^\S", line):
            break
        m = re.match(r'^\s+dispatch_events_topic:\s*"?([^"\s#]+)', line)
        if in_loop and m:
            return m.group(1)
    return ""


class TestDispatchTopicAlignment(unittest.TestCase):
    def test_relay_yaml(self):
        self.assertEqual(relay_yaml_dispatch_topic(), EXPECTED)

    def test_relay_go_default(self):
        m = re.search(r'SetDefault\("relay_loop\.dispatch_events_topic",\s*"([^"]+)"\)',
                      read("backend/relay/config/config.go"))
        self.assertIsNotNone(m)
        self.assertEqual(m.group(1), EXPECTED)

    def test_relay_compose_creates_topic(self):
        self.assertIn(f"--topic {EXPECTED}", read("backend/relay/docker-compose.yml"))

    def test_recon_compose_consumes_same_topic(self):
        self.assertEqual(compose_env("backend/recon/docker-compose.yml").get("KAFKA_TOPIC"),
                         relay_yaml_dispatch_topic())

    def test_recon_go_default(self):
        self.assertIn(f'dispatchTopic = "{EXPECTED}"', read("backend/recon/cmd/main.go"))


if __name__ == "__main__":
    unittest.main()
