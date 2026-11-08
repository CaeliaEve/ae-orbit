"""Run owned backend/simulator processes plus Lua bridge fixtures against PostgreSQL.

Requires AE_TEST_DATABASE_URL and built dist binaries. Creates and drops only a
random smoke_* schema. No Minecraft process or upstream repository is used.
"""
from __future__ import annotations

import argparse
import json
import os
import pathlib
import secrets
import shutil
import socket
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

ROOT = pathlib.Path(__file__).resolve().parents[1]
NO_WINDOW = subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0


def stop(process: subprocess.Popen | None) -> None:
    if process is not None and process.poll() is None:
        process.terminate()
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=5)


def until(check, timeout=15):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        value = check()
        if value:
            return value
        time.sleep(0.12)
    raise AssertionError("timed out waiting for the expected process behavior")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--psql", default=shutil.which("psql") or str(ROOT / ".tools/pgsql/bin/psql.exe"))
    parser.add_argument("--server", default=str(ROOT / ("dist/ae-orbit.exe" if os.name == "nt" else "dist/ae-orbit")))
    parser.add_argument("--simulator", default=str(ROOT / ("dist/simulator.exe" if os.name == "nt" else "dist/simulator")))
    parser.add_argument("--output", default=str(ROOT / ".local"), help="directory for owned test artifacts")
    args = parser.parse_args()
    dsn = os.environ.get("AE_TEST_DATABASE_URL", "").strip()
    if not dsn:
        raise SystemExit("Set AE_TEST_DATABASE_URL to a dedicated PostgreSQL test database.")
    for path in (args.psql, args.server, args.simulator):
        if not pathlib.Path(path).is_file():
            raise SystemExit(f"Missing executable: {path}")

    schema = "smoke_" + uuid.uuid4().hex
    directory = pathlib.Path(args.output) / schema
    directory.mkdir(parents=True)
    parsed = urllib.parse.urlsplit(dsn)
    query = dict(urllib.parse.parse_qsl(parsed.query))
    query["search_path"] = schema + ",public"
    isolated_dsn = urllib.parse.urlunsplit(parsed._replace(query=urllib.parse.urlencode(query)))
    with socket.socket() as probe:
        probe.bind(("127.0.0.1", 0))
        port = probe.getsockname()[1]
    origin = f"http://127.0.0.1:{port}"
    admin = secrets.token_urlsafe(40)
    env = {key: value for key, value in os.environ.items() if not key.startswith("AE_")}
    env.update(AE_DATABASE_URL=isolated_dsn, AE_ADMIN_TOKEN=admin, AE_LISTEN=f"127.0.0.1:{port}",
               AE_PUBLIC_URL=origin, AE_POLL_MILLIS="100", AE_SCAN_ITEMS="32", AE_RECHECK_SECONDS="300")
    processes = []
    logs = []
    checks = []

    def sql(statement):
        pg_env = {key: value for key, value in os.environ.items() if not key.startswith("PG")}
        pg_env.update(PGHOST=parsed.hostname or "localhost", PGPORT=str(parsed.port or 5432),
                      PGDATABASE=urllib.parse.unquote(parsed.path.lstrip("/")),
                      PGUSER=urllib.parse.unquote(parsed.username or "postgres"),
                      PGPASSWORD=urllib.parse.unquote(parsed.password or ""), PGCONNECT_TIMEOUT="5")
        for key, value in urllib.parse.parse_qsl(parsed.query):
            if key in ("sslmode", "sslcert", "sslkey", "sslrootcert", "options"):
                pg_env["PG" + key.upper()] = value
        result = subprocess.run([args.psql, "-X", "-q", "-v", "ON_ERROR_STOP=1", "-f", "-"],
                                input=statement, text=True, capture_output=True, env=pg_env,
                                creationflags=NO_WINDOW, timeout=15)
        if result.returncode:
            raise RuntimeError("isolated test schema operation failed: " + result.stderr[-1000:])

    def start_server():
        log = (directory / f"server-{len(processes)}.log").open("wb")
        logs.append(log)
        process = subprocess.Popen([args.server, "serve"], cwd=ROOT, env=env,
                                   stdout=log, stderr=log, creationflags=NO_WINDOW)
        processes.append(process)

        def ready():
            if process.poll() is not None:
                raise AssertionError(f"backend exited; inspect {directory}")
            try:
                return request("GET", "/readyz", None)["status"] == "ready"
            except (OSError, AssertionError):
                return False
        until(ready)
        return process

    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def raw_request(url, body=None, headers=None, method=None):
        request_object = urllib.request.Request(url, data=body, headers=headers or {}, method=method)
        try:
            response = opener.open(request_object, timeout=5)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            data = response.read(4 * 1024 * 1024 + 1)
            assert len(data) <= 4 * 1024 * 1024, "smoke response exceeds its test budget"
            response_headers = {name: response.headers.get_all(name) for name in response.headers.keys()}
            return response.status, data.decode("utf-8"), response_headers

    def request(method, path, body=None, expected=200):
        headers = {"Authorization": "Bearer " + admin}
        encoded = None
        if body is not None:
            encoded = json.dumps(body, separators=(",", ":")).encode()
            headers["Content-Type"] = "application/json"
        status, data, _ = raw_request(origin + path, encoded, headers, method)
        if status != expected:
            raise AssertionError(f"{method} {path}: HTTP {status}, expected {expected}")
        return json.loads(data) if data else None

    def network(name):
        return request("POST", "/api/v1/networks",
                       {"name": name, "server_group": name, "source": "simulator"}, 201)

    sql(f'CREATE SCHEMA "{schema}";')
    try:
        backend = start_server()
        created = network("Go process fixture")
        base = "/api/v1/networks/" + created["network"]["id"]
        sim_log = (directory / "simulator.log").open("wb")
        logs.append(sim_log)
        sim_env = dict(env, AE_DEVICE_TOKEN=created["device_token"])
        simulator = subprocess.Popen([args.simulator, "-url", origin, "-items", "257",
                                      "-batch", "17", "-cache", "8", "-cycles", "35", "-interval", "100ms"],
                                     cwd=ROOT, env=sim_env, stdout=sim_log, stderr=sim_log, creationflags=NO_WINDOW)
        processes.append(simulator)
        until(lambda: request("GET", base + "/inventory-stats")["entries"] == "257")
        item = request("GET", base + "/items?craftable=true&limit=1")["items"][0]
        order = request("POST", base + "/orders",
                        {"item_id": item["id"], "quantity": "64", "idempotency_key": "process-smoke-order"}, 201)
        until(lambda: request("GET", base + "/orders/" + order["id"])["state"] == "done")
        simulator.wait(timeout=15)
        assert simulator.returncode == 0, "simulator process failed"
        sim_log.flush()
        stats = json.loads((directory / "simulator.log").read_text().splitlines()[-1])
        assert stats["catalog_sent"] == 257 and stats["completed"] == 1 and stats["cache_entries"] <= 8
        first = request("GET", base + "/items?limit=17")
        second = request("GET", base + "/items?limit=17&cursor=" + urllib.parse.quote(first["next_cursor"]))
        assert len(first["items"]) == 17 and len(second["items"]) == 17
        assert not ({row["id"] for row in first["items"]} & {row["id"] for row in second["items"]})
        assert request("GET", base)["source"] == "simulator"
        checks.append("Go process: 257 items, bounded cache, paginated reads and completed order")
        print("PASS " + checks[-1], flush=True)

        from lupa import lua52, lua53, lua54
        for module in (lua52, lua53, lua54):
            version = ".".join(map(str, module.LUA_VERSION))
            created_lua = network("Lua " + version + " HTTP fixture")
            lua_base = "/api/v1/networks/" + created_lua["network"]["id"]
            runtime = module.LuaRuntime(unpack_returned_tuples=True)
            globals_ = runtime.globals()
            globals_.TEST_ROOT = ROOT.as_posix()
            globals_.FIXTURE_URL = origin
            globals_.FIXTURE_TOKEN = created_lua["device_token"]
            globals_.FIXTURE_JOURNAL = (directory / ("lua-" + version)).as_posix()
            clock_offset = [0.0]
            globals_.fixture_now = lambda offset=clock_offset: time.monotonic() + offset[0]
            globals_.fixture_sleep = time.sleep

            def card_request(url, body, headers, lua_runtime=runtime):
                assert url.startswith(origin + "/bridge/v1/")
                status, text, response_headers = raw_request(url, body.encode(), dict(headers.items()), "POST")
                return status, text, lua_runtime.table_from({
                    name: lua_runtime.table_from(values) for name, values in response_headers.items()
                })

            globals_.fixture_http = card_request
            runtime.execute("package.path = TEST_ROOT .. '/oc/lib/?.lua;' .. TEST_ROOT .. '/oc/tests/?.lua;' .. package.path")
            runtime.execute((ROOT / "scripts/fixtures/oc_http.lua").read_text(encoding="utf-8"))

            def tick_until(check):
                def tick():
                    status = globals_.fixture_tick()
                    assert status["control_ok"] and status["inventory_ok"], "bridge transport failed"
                    return check()
                return until(tick)

            tick_until(lambda: request("GET", lua_base + "/inventory-stats")["entries"] == "2")
            rows = request("GET", lua_base + "/items")["items"]
            exact = next(row for row in rows if row["name"] == "fixture:exact")
            target = next(row for row in rows if row["name"] == "fixture:craftable")
            assert exact["quantity"] == "9223372036854775807" and target["quantity"] == "10"
            order_body = {"item_id": target["id"], "quantity": "64", "idempotency_key": "lua-smoke-order"}
            lua_order = request("POST", lua_base + "/orders", order_body, 201)
            assert request("POST", lua_base + "/orders", order_body, 201)["id"] == lua_order["id"]
            conflict = request("POST", lua_base + "/orders", dict(order_body, quantity="65"), 409)
            assert conflict["error"]["code"] == "conflict"
            observed_output = []

            def finished():
                cpu_view = request("GET", lua_base + "/cpus")
                for cpu in cpu_view["cpus"]:
                    if cpu.get("output"):
                        observed_output.append(cpu["output"]["quantity"])
                return request("GET", lua_base + "/orders/" + lua_order["id"])["state"] == "done"

            tick_until(finished)
            tick_until(lambda: request("GET", lua_base + "/items?craftable=true")["items"][0]["quantity"] == "74")
            assert globals_.fixture_requests == 1 and "64" in observed_output
            assert globals_.fixture_continuations > 0, "real HTTP did not exercise inventory continuation"
            globals_.fixture_fail_lookup(True)
            tick_until(lambda: request("GET", lua_base).get("health", {}).get("read_failures"))
            samples = request("GET", lua_base)["health"]["read_failures"]
            failure = next(sample for sample in samples if sample["source"] == "event")
            assert failure["code"] == "exact_lookup_failed" and failure["message"] == "fixture lookup failed"
            assert failure["name"] == "fixture:craftable" and failure["item_id"] == target["id"]
            globals_.fixture_fail_lookup(False)
            globals_.fixture_tick()
            assert request("GET", lua_base)["health"]["read_failures"] == samples, "omitted snapshot was erased"
            assert request("GET", lua_base + "/items?craftable=true")["items"][0]["quantity"] == "74", "read failure invented zero stock"
            checks.append(f"Lua {version}: real HTTP with continued slices, stock 10+64=74 without change events or periodic rechecks, exact int64, CPU output, one craft invocation")
            print("PASS " + checks[-1], flush=True)
            checks.append(f"Lua {version}: bounded read failure details survive a sparse health poll without changing stock")
            print("PASS " + checks[-1], flush=True)
            clock_offset[0] += 31
            globals_.fixture_tick()
            timing = request("GET", lua_base)
            sync = timing["health"]["sync"]
            assert int(sync["captured_millis"]) >= 31000 and int(sync["first_scan_ack_millis"]) < 31000
            assert int(sync["stages"]["collection"]["samples"]) > 0
            assert int(sync["stages"]["inventory_http"]["samples"]) > 0
            assert timing["first_scan"] and timing["session_started_at"]
            globals_.fixture_tick()
            assert request("GET", lua_base)["health"]["sync"] == sync, "sparse timing was erased or resent immediately"
            checks.append(f"Lua {version}: bounded timing crosses real HTTP, keeps the first scan and survives an omitted snapshot")
            print("PASS " + checks[-1], flush=True)
            assert globals_.fixture_framed_requests > 0, "real HTTP never exercised Content-Length"
            assert globals_.fixture_framed_eof_reads == 0, "complete framed responses made extra native EOF reads"
            checks.append(f"Lua {version}: real HTTP lengths stop bounded native reads at the complete response")
            print("PASS " + checks[-1], flush=True)

        stop(backend)
        backend = start_server()
        assert request("GET", base + "/inventory-stats")["entries"] == "257"
        assert request("GET", base + "/orders/" + order["id"])["state"] == "done"
        checks.append("Backend process restart preserves inventory and completed orders")
        print("PASS " + checks[-1], flush=True)
        summary = {"checks": checks, "simulator": stats, "game_validation": False}
        (pathlib.Path(args.output) / "smoke-result.json").write_text(json.dumps(summary, indent=2) + "\n", encoding="utf-8")
    finally:
        for process in reversed(processes):
            stop(process)
        for log in logs:
            log.close()
        sql(f'DROP SCHEMA "{schema}" CASCADE;')
    print(f"PASS owned processes stopped and isolated schema removed; logs: {directory}")


if __name__ == "__main__":
    main()
