#!/usr/bin/env python3
"""Migration coverage: every table a Go service writes must be created by that
service's migrations (L7: dispatch_index must actually be filled, so the table
and its columns have to exist in every environment, not only via init.sql).

For each backend/<svc>/go.mod:
  * created  = CREATE TABLE names in backend/<svc>/db/migrations/*.sql
  * written  = INSERT INTO / UPDATE .. SET / DELETE FROM / COPY targets found
               inside Go string literals (raw `...` or "...") in non-test files
  * missing  = written - created - ALLOWED[svc]

Exit 1 and print the gaps if anything is missing.
Usage: python3 backend/tests/check_migration_coverage.py [--json]
"""

from __future__ import annotations

import glob
import json
import os
import re
import sys
from typing import Dict, Set

BACKEND = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))

# Tables written at runtime that intentionally are NOT in the service's own
# migrations. Each entry needs a justification.
ALLOWED: Dict[str, Dict[str, str]] = {
    "agents": {
        # created by agents/db/init.sql for the local compose DB and by the
        # 20260914120000 migration (which lacks goose markers; see report).
    },
}

IDENT = r'"?(?:[a-z_][a-z0-9_]*"?\."?)?([a-z_][a-z0-9_]*)"?'
CREATE_RE = re.compile(r"create\s+(?:unlogged\s+)?table\s+(?:if\s+not\s+exists\s+)?" + IDENT, re.I)
RENAME_RE = re.compile(r"alter\s+table\s+(?:if\s+exists\s+)?" + IDENT + r"\s+rename\s+to\s+" + IDENT, re.I)
DROP_RE = re.compile(r"drop\s+table\s+(?:if\s+exists\s+)?" + IDENT, re.I)
WRITE_RES = [
    re.compile(r"\binsert\s+into\s+" + IDENT, re.I),
    re.compile(r"\bupdate\s+" + IDENT + r"(?:\s+(?:as\s+)?[a-z_]+)?\s+set\b", re.I),
    re.compile(r"\bdelete\s+from\s+" + IDENT, re.I),
    re.compile(r"\bcopy\s+" + IDENT + r"\s*\(", re.I),
]
RAW_STR = re.compile(r"`([^`]*)`", re.S)
DQ_STR = re.compile(r'"((?:[^"\\\n]|\\.)*)"')
GOOSE_DOWN = re.compile(r"^--\s*\+goose\s+Down", re.I | re.M)
SQL_KEYWORDS = {"select", "values", "set", "only", "the", "a"}


def _up_part(sql: str) -> str:
    m = GOOSE_DOWN.search(sql)
    return sql[: m.start()] if m else sql


def created_tables(mig_dir: str) -> Set[str]:
    tables: Set[str] = set()
    for path in sorted(glob.glob(os.path.join(mig_dir, "*.sql"))):
        with open(path, encoding="utf-8") as f:
            up = _up_part(f.read())
        up = re.sub(r"--[^\n]*", "", up)
        for stmt in re.split(r";", up):
            for m in CREATE_RE.finditer(stmt):
                tables.add(m.group(1).lower())
            for m in RENAME_RE.finditer(stmt):
                tables.discard(m.group(1).lower())
                tables.add(m.group(2).lower())
            for m in DROP_RE.finditer(stmt):
                tables.discard(m.group(1).lower())
    return tables


def written_tables(svc_dir: str) -> Dict[str, Set[str]]:
    out: Dict[str, Set[str]] = {}
    for root, dirs, files in os.walk(svc_dir):
        dirs[:] = [d for d in dirs if d not in ("vendor", "node_modules", ".git", "testdata")]
        for name in files:
            if not name.endswith(".go") or name.endswith("_test.go"):
                continue
            path = os.path.join(root, name)
            with open(path, encoding="utf-8") as f:
                src = f.read()
            literals = [m.group(1) for m in RAW_STR.finditer(src)]
            literals += [m.group(1) for m in DQ_STR.finditer(src)]
            for lit in literals:
                for rx in WRITE_RES:
                    for m in rx.finditer(lit):
                        t = m.group(1).lower()
                        if t in SQL_KEYWORDS:
                            continue
                        out.setdefault(t, set()).add(os.path.relpath(path, BACKEND))
    return out


def coverage() -> Dict[str, Dict[str, object]]:
    report: Dict[str, Dict[str, object]] = {}
    for gomod in sorted(glob.glob(os.path.join(BACKEND, "*", "go.mod"))):
        svc_dir = os.path.dirname(gomod)
        svc = os.path.basename(svc_dir)
        mig_dir = os.path.join(svc_dir, "db", "migrations")
        created = created_tables(mig_dir) if os.path.isdir(mig_dir) else set()
        written = written_tables(svc_dir)
        allowed = set(ALLOWED.get(svc, {}))
        missing = {t: sorted(w) for t, w in written.items() if t not in created and t not in allowed}
        report[svc] = {
            "migrations_dir": os.path.relpath(mig_dir, BACKEND) if os.path.isdir(mig_dir) else None,
            "created": sorted(created),
            "written": sorted(written),
            "missing": missing,
        }
    return report


def main(argv=None) -> int:
    argv = sys.argv[1:] if argv is None else argv
    rep = coverage()
    if "--json" in argv:
        print(json.dumps(rep, indent=2))
    bad = {s: r["missing"] for s, r in rep.items() if r["missing"]}
    for s, r in rep.items():
        print(f"{s}: writes {len(r['written'])} tables, migrations create {len(r['created'])}, missing {len(r['missing'])}")
    for s, miss in bad.items():
        for t, files in miss.items():
            print(f"  MISSING {s}.{t} (written in {', '.join(files)})")
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
