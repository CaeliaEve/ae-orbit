"""Safety checks for the separately owned cooperative process smoke."""
from __future__ import annotations

import os
import pathlib
import subprocess
import sys
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts/smoke_cooperative.py"


class CooperativeSmokeSafetyTest(unittest.TestCase):
    def run_script(self, dsn, *arguments):
        environment = dict(os.environ, AE_TEST_DATABASE_URL=dsn)
        return subprocess.run(
            [sys.executable, str(SCRIPT), *arguments], env=environment,
            capture_output=True, text=True, timeout=10,
            creationflags=subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0,
        )

    def test_forbidden_database_is_rejected_before_process_or_schema_creation(self):
        result = self.run_script(
            "postgres://postgres@127.0.0.1:55609/postgres?sslmode=disable"
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("refusing PostgreSQL port 55609", result.stderr)

    def test_missing_dedicated_database_is_rejected(self):
        result = self.run_script("")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Set AE_TEST_DATABASE_URL", result.stderr)

    def test_generated_artifacts_inside_repository_are_rejected(self):
        result = self.run_script(
            "postgres://postgres@127.0.0.1:55608/postgres?sslmode=disable",
            "--output", str(ROOT / ".local"),
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("output must be outside the repository", result.stderr)


if __name__ == "__main__":
    unittest.main()
