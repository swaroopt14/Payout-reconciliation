"""D32: no secrets in committed config.

Scans every committable yaml/yml/cfg/compose/.env.example file:
  * secret-looking keys (PASSWORD/SECRET/TOKEN/KEY...) must be empty, a
    ${VAR} reference, or an obvious placeholder (change-me / test-only);
  * ${VAR:-default} defaults on secret keys must be placeholders too;
  * connection URLs must not embed a literal password;
  * previously committed secrets must never come back;
  * .env is gitignored and .env.example is not.
Run: PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s backend/tests
"""

from __future__ import annotations

import os
import re
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from _repo import REPO, git_check_ignored, read, repo_files  # noqa: E402

CONFIG_RE = re.compile(r"(\.ya?ml|\.cfg|(^|/)\.env\.example|(^|/)env\.[\w.-]*example)$")
SECRET_KEY_RE = re.compile(
    r"(PASSWORD|PASSWD|SECRET|TOKEN|API_KEY|ACCESS_KEY|PRIVATE_KEY|FERNET_KEY|_KEY$|^KEY$|password|secret|token)",
)
# Keys that match SECRET_KEY_RE but are not secrets.
NON_SECRET_KEYS = {
    "KMS_KEY_ID_HEADER", "VAULT_KEY_ID", "VAULT_KEY_VERSION", "SIGNING_KEY_PATH",
    "AUTH_COOKIE_SECURE", "RAZORPAY_KEY_ID", "TOKEN_TTL", "JWT_AUDIENCE", "JWT_ISSUER",
    "AIRFLOW__API_AUTH__JWT_ISSUER", "_AIRFLOW_WWW_USER_USERNAME", "token_url",
    "ZORD_BULK_INGEST_SOURCE_TYPE", "ZORD_BULK_INGEST_SOURCE_CLASS",
}
PLACEHOLDER_RE = re.compile(r"^(|change-me.*|test-only.*|<[^>]*>|x+|\*+|null|none|false|true)$", re.I)
# Documented local-dev placeholders already baked into code defaults (not credentials).
ALLOWED_LITERALS = {"zord-local-dev-api-key"}

BANNED = [
    "dev-dummy-token-123", "test123", "relay_password", "outcome_password", "zpi_secret",
    "zpl_password", "token_password", "intent_password", "evidence_password", "zord_password",
    "eXDQTEFNyzogVqq4fkgPSMbqJWWoUGPv",
    "K4fU37QVv2JK87y_a9hYWf-0vMwvWHtqYb4kz1c7w80=",
    "UeEmQQwrFzv92o3PwiEUYA==", "05h6HUpmVrsj0ip+FbVZ6Q==",
    "your-secret-key-for-pii", "airflow_jwt_secret",
]

KV_RE = re.compile(r"^\s*-?\s*([A-Za-z_][A-Za-z0-9_.]*)\s*(=|:)\s*(.*?)\s*$")
DEFAULT_RE = re.compile(r"\$\{([A-Za-z_][A-Za-z0-9_]*):?-([^}]*)\}")
# Real-looking key material: base64 of >= 24 bytes (e.g. a 32-byte
# CLEARLINE_SECRETS_KEY is 44 chars ending in '=') or long hex.
KEY_MATERIAL_RE = re.compile(r"(?<![A-Za-z0-9+/_-])(?:[A-Za-z0-9+/]{32,}={0,2}|[0-9a-fA-F]{40,})(?![A-Za-z0-9+/=])")
URL_PW_RE = re.compile(r"[a-z][a-z0-9+]*://[^:/\s@]+:([^@\s/]+)@")


def config_files():
    return sorted(f for f in repo_files() if CONFIG_RE.search(f))


NON_SECRET_SUFFIX_RE = re.compile(r"(_URL|_URI|_HOST|_TOPIC|_TOPIC_[A-Z_]+|_DATA|_PATH|_FILE|_ID|_ISSUER|_AUDIENCE|_TTL|_TIMEOUT|_LENGTH|_masked_secret|_data)$")
NON_SECRET_PREFIX_RE = re.compile(r"^(KAFKA_TOPIC|min_length)")


def _strip_comment(v: str) -> str:
    if v[:1] in ("'", '"'):
        end = v.find(v[0], 1)
        return v[: end + 1] if end > 0 else v
    return re.split(r"\s+#", v, maxsplit=1)[0].strip()


def _is_ok_value(v: str) -> bool:
    v = v.strip().strip('"').strip("'")
    if v in ALLOWED_LITERALS or PLACEHOLDER_RE.match(v):
        return True
    if re.fullmatch(r"\$\{[A-Za-z_][A-Za-z0-9_]*(:?\?[^}]*)?\}", v):
        return True
    m = DEFAULT_RE.fullmatch(v)
    return bool(m and _is_ok_value(m.group(2)))


def scan_text(rel: str, text: str):
    findings = []
    for n, line in enumerate(text.splitlines(), 1):
        s = line.strip()
        if not s or s.startswith("#"):
            continue
        for b in BANNED:
            if b in line:
                findings.append(f"{rel}:{n}: banned literal {b!r}")
        m_kv = KV_RE.match(line)
        if m_kv:
            val = _strip_comment(m_kv.group(3)).strip('"').strip("'")
            if not val.startswith(("$", "/")) and "://" not in val \
                    and KEY_MATERIAL_RE.fullmatch(val) and re.search(r"[0-9]", val) and re.search(r"[A-Za-z]", val):
                findings.append(f"{rel}:{n}: {m_kv.group(1)} looks like real key material")
                continue
        for m in URL_PW_RE.finditer(line):
            if not _is_ok_value(m.group(1)) and not m.group(1).startswith("$"):
                findings.append(f"{rel}:{n}: literal password in URL")
        for m in DEFAULT_RE.finditer(line):
            if SECRET_KEY_RE.search(m.group(1)) and m.group(1) not in NON_SECRET_KEYS \
                    and not _is_ok_value(m.group(2)):
                findings.append(f"{rel}:{n}: secret default ${{{m.group(1)}:-...}} is not a placeholder")
        m = KV_RE.match(line)
        if not m:
            continue
        key, value = m.group(1), _strip_comment(m.group(3))
        leaf = key.split(".")[-1]
        if leaf in NON_SECRET_KEYS or not SECRET_KEY_RE.search(leaf):
            continue
        if NON_SECRET_SUFFIX_RE.search(leaf) or NON_SECRET_PREFIX_RE.search(leaf):
            continue
        if value in ("|", ">", "|-", ">-"):
            continue
        # value may itself be KEY=VAL inside a compose list item handled above.
        if "=" in value and m.group(2) == ":" and not value.startswith("$"):
            continue
        if not _is_ok_value(value):
            findings.append(f"{rel}:{n}: {leaf} has a literal value")
    return findings


class TestScannerSelfCheck(unittest.TestCase):
    def test_flags_literals(self):
        bad = "\n".join([
            "  - RELAY_AUTH_TOKEN=abc123",
            "db_password: hunter2",
            "  - DATABASE_URL=postgres://u:hunter2@db:5432/x",
            "  - JWT_SIGNING_SECRET=${JWT_SIGNING_SECRET:-realvalue}",
            "fernet_key = K4fU37QVv2JK87y_a9hYWf-0vMwvWHtqYb4kz1c7w80=",
        ])
        self.assertEqual(len([f for f in scan_text("x.yml", bad) if "banned" not in f]), 5)

    def test_clearline_secrets_key(self):
        real = "CLEARLINE_SECRETS_KEY=q3Jv8m2ZkLr0bX9tYwP4sN6uE1cH7aDfG5iK0oQzR8M="
        self.assertTrue(scan_text("backend/edge/.env.example", real))
        self.assertTrue(scan_text("c.yml", "  - CLEARLINE_SECRETS_KEY=" + real.split("=", 1)[1]))
        # default on a ${VAR:-...} is flagged too
        self.assertTrue(scan_text("c.yml", "  - CLEARLINE_SECRETS_KEY=${CLEARLINE_SECRETS_KEY:-" + real.split("=", 1)[1] + "}"))
        for ok in ("CLEARLINE_SECRETS_KEY=change-me", "CLEARLINE_SECRETS_KEY=",
                   "  - CLEARLINE_SECRETS_KEY=${CLEARLINE_SECRETS_KEY:?set CLEARLINE_SECRETS_KEY}",
                   "  - CLEARLINE_SECRETS_KEY=${CLEARLINE_SECRETS_KEY}"):
            self.assertEqual(scan_text("x.yml", ok), [], ok)

    def test_key_material_flagged_even_on_innocent_key(self):
        self.assertTrue(scan_text("x.yml", "  - SOME_SETTING=q3Jv8m2ZkLr0bX9tYwP4sN6uE1cH7aDfG5iK0oQzR8M="))

    def test_edge_requires_secrets_key(self):
        body = read("backend/edge/docker-compose.yml")
        self.assertIn("CLEARLINE_SECRETS_KEY=${CLEARLINE_SECRETS_KEY:?", body)
        self.assertIn("CLEARLINE_SECRETS_KEY=change-me", read("backend/edge/.env.example"))

    def test_accepts_placeholders(self):
        ok = "\n".join([
            "  - RELAY_AUTH_TOKEN=${RELAY_AUTH_TOKEN:?set it}",
            "auth_token: \"${RELAY_AUTH_TOKEN}\"",
            "  - DATABASE_URL=postgres://u:${PW:?x}@db:5432/x",
            "  - PW=${PW:-test-only-db}",
            "RELAY_AUTH_TOKEN=change-me",
            "fernet_key =",
            "  - JWT_AUDIENCE=zord-console",
        ])
        self.assertEqual(scan_text("x.yml", ok), [])


class TestNoSecretsInConfig(unittest.TestCase):
    def test_config_files_found(self):
        files = config_files()
        self.assertIn("backend/relay/config.yaml", files)
        self.assertIn("backend/scheduler/config/airflow.cfg", files)

    def test_no_literal_secrets(self):
        findings = []
        for rel in config_files():
            try:
                findings += scan_text(rel, read(rel))
            except UnicodeDecodeError:
                continue
        self.assertEqual(findings, [], "\n" + "\n".join(findings))

    def test_env_examples_are_placeholders(self):
        examples = [f for f in config_files() if f.endswith(".env.example")]
        self.assertGreaterEqual(len(examples), 10, examples)

    def test_dotenv_ignored_examples_not(self):
        for d in ("backend/relay", "backend/edge", "backend/recon", "backend/router", "backend/scheduler"):
            ign = git_check_ignored(f"{d}/.env")
            if ign is None:
                self.skipTest("git unavailable")
            self.assertTrue(ign, f"{d}/.env must be gitignored")
            self.assertFalse(git_check_ignored(f"{d}/.env.example"), f"{d}/.env.example must be committable")


if __name__ == "__main__":
    unittest.main()
