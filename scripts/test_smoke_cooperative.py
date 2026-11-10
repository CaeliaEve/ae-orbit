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

    def test_negative_control_still_requires_dedicated_database(self):
        result = self.run_script("", "--background-recipe-negotiation", "absent")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Set AE_TEST_DATABASE_URL", result.stderr)

    def test_negative_control_still_rejects_forbidden_database(self):
        result = self.run_script(
            "postgres://postgres@127.0.0.1:55609/postgres?sslmode=disable",
            "--background-recipe-negotiation", "absent",
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("refusing PostgreSQL port 55609", result.stderr)

    def test_negative_control_still_rejects_repository_artifacts(self):
        result = self.run_script(
            "postgres://postgres@127.0.0.1:55608/postgres?sslmode=disable",
            "--background-recipe-negotiation", "absent", "--output", str(ROOT / ".local"),
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("output must be outside the repository", result.stderr)


class CooperativeHardwareFixtureTest(unittest.TestCase):
    def fixture_runtime(self, module, tagged=False):
        runtime = module.LuaRuntime(unpack_returned_tuples=True)
        globals_ = runtime.globals()
        globals_.TEST_ROOT = ROOT.as_posix()
        if tagged:
            globals_.FIXTURE_BRIDGE_MODE = "cooperative"
            globals_.FIXTURE_TAGGED = True
        globals_.FIXTURE_URL = "http://127.0.0.1:1"
        globals_.FIXTURE_TOKEN = "x" * 32
        globals_.FIXTURE_JOURNAL = ""
        runtime.execute("""
            package.path=TEST_ROOT..'/oc/lib/?.lua;'..TEST_ROOT..'/oc/tests/?.lua;'..package.path
            local capture={new=function(_,deps)
              fixture_adapter=deps.adapter
              return {start=function() end}
            end}
            package.loaded["aeorbit.cooperative"]=capture
            package.loaded["aeorbit.runner"]=capture
        """)
        runtime.execute((ROOT / "scripts/fixtures/oc_http.lua").read_text(encoding="utf-8"))
        return runtime

    def test_default_fixture_retains_untagged_stock_and_recipe(self):
        from lupa import lua52, lua53, lua54
        for module in (lua52, lua53, lua54):
            with self.subTest(runtime=module.LUA_VERSION):
                self.fixture_runtime(module).execute("""
                    local iterator=fixture_adapter:iterator()
                    local stock,exact=iterator(),iterator()
                    assert(stock.hasTag==false and stock.tag==nil)
                    assert(exact.hasTag==false and exact.tag==nil and iterator()==nil)
                    assert(fixture_adapter:count(exact)=="9223372036854775807")
                    assert(fixture_adapter:count(fixture_adapter:read(stock))=="10")
                    assert(fixture_adapter:recipe_status(stock)==true)
                    assert(fixture_recipe_failures==0 and fixture_lookup_failures==0)
                """)

    def test_tagged_recipe_failure_retains_exact_stock_and_identity(self):
        from lupa import lua52, lua53, lua54
        for module in (lua52, lua53, lua54):
            with self.subTest(runtime=module.LUA_VERSION):
                runtime = self.fixture_runtime(module, tagged=True)
                runtime.execute("""
                    local base64=require("aeorbit.base64")
                    local tag=base64.decode("H4sIAAAAAAAAA+NiYGAAAHg/+U4EAAAA")
                    local detail={name="fixture:craftable",damage=0,hasTag=true,tag=tag}
                    fixture_stock="9007199254740993"
                    local exact=fixture_adapter:read(detail)
                    assert(exact.tag==tag and exact.hasTag and fixture_adapter:count(exact)=="9007199254740993")
                    assert(fixture_adapter:recipe_status(exact)==true)
                    fixture_recipe_failure=true
                    assert(not pcall(fixture_adapter.recipe_status,fixture_adapter,exact))
                    assert(fixture_recipe_failures==1)
                    assert(fixture_adapter:count(fixture_adapter:read(detail))=="9007199254740993")
                    assert(fixture_exact_reads==2 and fixture_lookup_failures==0)
                """)


if __name__ == "__main__":
    unittest.main()
