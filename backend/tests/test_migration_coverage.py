"""Item 6: every table a Go service writes is created by its own migrations."""

from __future__ import annotations

import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import check_migration_coverage as cmc  # noqa: E402


class TestCheckerLogic(unittest.TestCase):
    def test_detects_missing_and_ignores_comments(self):
        with tempfile.TemporaryDirectory() as d:
            os.makedirs(os.path.join(d, "db", "migrations"))
            with open(os.path.join(d, "db", "migrations", "001.sql"), "w") as f:
                f.write("-- +goose Up\nCREATE TABLE IF NOT EXISTS alpha (id int);\n"
                        "CREATE TABLE old_b (id int);\nALTER TABLE old_b RENAME TO b;\n"
                        "-- +goose Down\nDROP TABLE alpha;\n")
            with open(os.path.join(d, "repo.go"), "w") as f:
                f.write('package x\n// insert into commentonly is prose\n'
                        'const q1 = `INSERT INTO alpha (id) VALUES ($1)`\n'
                        'const q2 = "UPDATE public.b SET id = 1"\n'
                        'const q3 = `DELETE FROM c WHERE id = $1`\n')
            with open(os.path.join(d, "repo_test.go"), "w") as f:
                f.write('package x\nconst t = `INSERT INTO testonly VALUES (1)`\n')
            self.assertEqual(cmc.created_tables(os.path.join(d, "db", "migrations")), {"alpha", "b"})
            self.assertEqual(set(cmc.written_tables(d)), {"alpha", "b", "c"})


class TestRepoMigrationCoverage(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.report = cmc.coverage()

    def test_no_service_writes_unmigrated_tables(self):
        gaps = {s: r["missing"] for s, r in self.report.items() if r["missing"]}
        self.assertEqual(gaps, {})

    def test_relay_tables_come_from_goose_migrations(self):
        created = set(self.report["relay"]["created"])
        for t in ("relay_outbox", "dispatches", "relay_publish_failures"):
            self.assertIn(t, created)

    def test_agents_vector_index_state_migrated(self):
        self.assertIn("vector_index_state", self.report["agents"]["created"])

    def test_recon_dispatch_index_and_payouts(self):
        created = set(self.report["recon"]["created"])
        self.assertIn("dispatch_index", created)
        self.assertIn("canonical_payouts", created)


if __name__ == "__main__":
    unittest.main()
