"""Shared helpers for backend/tests (stdlib only; no PyYAML needed)."""

from __future__ import annotations

import os
import re
import subprocess
from typing import Dict, List, Optional

REPO = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
BACKEND = os.path.join(REPO, "backend")

COMPOSE_FILES = [
    "backend/agents/docker-compose.yml",
    "backend/console/docker-compose.yml",
    "backend/edge/docker-compose.yml",
    "backend/evidence/docker-compose.yml",
    "backend/intel/docker-compose.yml",
    "backend/intel/docker-compose.test.yml",
    "backend/intents/docker-compose.yml",
    "backend/payout-smoke-simulator/docker-compose.yml",
    "backend/recon/docker-compose.yml",
    "backend/relay/docker-compose.yml",
    "backend/router/docker-compose.yml",
    "backend/scheduler/docker-compose.yaml",
    "backend/vault/docker-compose.yml",
]


def read(rel: str) -> str:
    with open(os.path.join(REPO, rel), encoding="utf-8") as f:
        return f.read()


def repo_files() -> List[str]:
    """Tracked + untracked-not-ignored files (what a commit could contain)."""
    try:
        out = subprocess.run(
            ["git", "ls-files", "--cached", "--others", "--exclude-standard"],
            cwd=REPO, capture_output=True, text=True, check=True,
        ).stdout
        files = [l for l in out.splitlines() if l]
    except (OSError, subprocess.CalledProcessError):
        files = []
        for root, dirs, names in os.walk(REPO):
            dirs[:] = [d for d in dirs if d not in (".git", "node_modules", ".venv", "vendor", "target")]
            for n in names:
                files.append(os.path.relpath(os.path.join(root, n), REPO))
    return [f for f in files if "node_modules/" not in f and os.path.isfile(os.path.join(REPO, f))]


def git_check_ignored(rel: str) -> Optional[bool]:
    try:
        r = subprocess.run(["git", "check-ignore", "-q", rel], cwd=REPO)
    except OSError:
        return None
    if r.returncode not in (0, 1):
        return None
    return r.returncode == 0


_ENV_LINE = re.compile(r"^\s*-?\s*([A-Z][A-Z0-9_]*)\s*[:=]\s*(.*)$")


def compose_env(rel: str, service_hint: str = "") -> Dict[str, str]:
    """KEY -> raw value for `- KEY=value` / `KEY: value` lines (first wins)."""
    out: Dict[str, str] = {}
    for line in read(rel).splitlines():
        if line.strip().startswith("#"):
            continue
        m = _ENV_LINE.match(line)
        if m:
            k, v = m.group(1), m.group(2).strip().strip('"').strip("'")
            out.setdefault(k, v)
    return out


def dotenv(rel: str) -> Dict[str, str]:
    out: Dict[str, str] = {}
    for line in read(rel).splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        k, v = line.split("=", 1)
        out[k.strip()] = v.strip()
    return out
