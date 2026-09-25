"""Item D + item 4: router wiring is mandatory everywhere and dispatch stays off.

Every service that calls the router (relay dispatch, console routing API)
must get ROUTER_URL + ROUTER_AUTH_TOKEN via required-var syntax, or every
payout holds. Relay dispatch is explicitly disabled (D27/D43).
"""

from __future__ import annotations

import os
import re
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from _repo import compose_env, dotenv, read  # noqa: E402

REQ = re.compile(r"^\$\{(?P<var>[A-Z_]+):\?[^}]+\}$")


def required_var(value: str) -> str:
    m = REQ.match(value or "")
    return m.group("var") if m else ""


class TestRouterEnvRequired(unittest.TestCase):
    def test_relay_compose(self):
        env = compose_env("backend/relay/docker-compose.yml")
        self.assertEqual(required_var(env.get("ROUTER_URL")), "ROUTER_URL")
        self.assertEqual(required_var(env.get("RELAY_DISPATCH_ROUTER_URL")), "ROUTER_URL")
        self.assertEqual(required_var(env.get("ROUTER_AUTH_TOKEN")), "ROUTER_AUTH_TOKEN")

    def test_console_compose(self):
        env = compose_env("backend/console/docker-compose.yml")
        self.assertEqual(required_var(env.get("ZORD_ROUTER_URL")), "ROUTER_URL")
        self.assertEqual(required_var(env.get("ROUTER_AUTH_TOKEN")), "ROUTER_AUTH_TOKEN")

    def test_router_compose(self):
        env = compose_env("backend/router/docker-compose.yml")
        self.assertEqual(required_var(env.get("ROUTER_AUTH_TOKEN")), "ROUTER_AUTH_TOKEN")
        self.assertIn("zord-console", env.get("JWT_AUDIENCE", ""))

    def test_env_examples_list_router_vars(self):
        for rel in ("backend/relay/.env.example", "backend/router/.env.example"):
            env = dotenv(rel)
            self.assertIn("ROUTER_URL", env, rel)
            self.assertIn("ROUTER_AUTH_TOKEN", env, rel)
            self.assertEqual(env["ROUTER_URL"], "http://zord-router:8091", rel)

    def test_relay_yaml_router_url(self):
        self.assertIn('router_url: "${ROUTER_URL:-http://zord-router:8091}"',
                      read("backend/relay/config.yaml"))


class TestRelayDispatchOff(unittest.TestCase):
    def test_compose_explicitly_disabled(self):
        env = compose_env("backend/relay/docker-compose.yml")
        self.assertEqual(env.get("RELAY_DISPATCH_ENABLED"), "false")

    def test_config_yaml_disabled(self):
        body = read("backend/relay/config.yaml")
        m = re.search(r"^dispatch:\s*\n((?:[ \t]+.*\n|\s*\n)+)", body, re.M)
        self.assertIsNotNone(m, "dispatch: block missing")
        self.assertRegex(m.group(1), r"(?m)^\s+enabled:\s*false\b")

    def test_go_default_disabled(self):
        self.assertIn('v.SetDefault("dispatch.enabled", false)', read("backend/relay/config/config.go"))


if __name__ == "__main__":
    unittest.main()
