"""Exercise two real Lua bridges against an owned backend and isolated PostgreSQL.

Only AE hardware and the internet card are substitutes. No game process, device
credentials, or real-game throughput is involved. Artifacts stay outside the repo.
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
import tempfile
import time
import traceback
import urllib.error
import urllib.parse
import urllib.request
import uuid

from smoke import NO_WINDOW, ROOT, stop, until


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--psql", default=shutil.which("psql") or str(ROOT / ".tools/pgsql/bin/psql.exe"))
    parser.add_argument("--server", default=str(ROOT / ("dist/ae-orbit.exe" if os.name == "nt" else "dist/ae-orbit")))
    parser.add_argument("--output", default=str(pathlib.Path(tempfile.gettempdir()) / "ae-cooperative-smoke"))
    parser.add_argument("--lua", nargs="+", choices=("5.2", "5.3", "5.4"), default=["5.2", "5.3", "5.4"])
    parser.add_argument("--background-recipe-negotiation", choices=("normal", "absent"), default="normal",
                        help="test-only negative control: omit negotiation in the isolated tagged scenario")
    args = parser.parse_args()
    dsn = os.environ.get("AE_TEST_DATABASE_URL", "").strip()
    if not dsn:
        raise SystemExit("Set AE_TEST_DATABASE_URL to a dedicated PostgreSQL test database.")
    parsed = urllib.parse.urlsplit(dsn)
    if parsed.port == 55609:
        raise SystemExit("refusing PostgreSQL port 55609")
    output = pathlib.Path(args.output).resolve()
    if output.is_relative_to(ROOT):
        raise SystemExit("output must be outside the repository")
    for executable in (args.psql, args.server):
        if not pathlib.Path(executable).is_file():
            raise SystemExit("Missing executable: " + executable)

    schema = "smoke_cooperative_" + uuid.uuid4().hex
    directory = output / schema
    directory.mkdir(parents=True)
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
    processes, logs, checks, wire = [], [], [], []
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    summary = {"game_validation": False, "schema": schema, "checks": checks,
               "background_recipe_negotiation": args.background_recipe_negotiation}

    def sql(statement):
        pg_env = {key: value for key, value in os.environ.items() if not key.startswith("PG")}
        pg_env.update(PGHOST=parsed.hostname or "localhost", PGPORT=str(parsed.port or 5432),
                      PGDATABASE=urllib.parse.unquote(parsed.path.lstrip("/")),
                      PGUSER=urllib.parse.unquote(parsed.username or "postgres"),
                      PGPASSWORD=urllib.parse.unquote(parsed.password or ""), PGCONNECT_TIMEOUT="5")
        for key, value in urllib.parse.parse_qsl(parsed.query):
            if key in ("sslmode", "sslcert", "sslkey", "sslrootcert", "options"):
                pg_env["PG" + key.upper()] = value
        result = subprocess.run([args.psql, "-X", "-q", "-t", "-A", "-v", "ON_ERROR_STOP=1", "-f", "-"],
                                input=statement, text=True, capture_output=True, env=pg_env,
                                creationflags=NO_WINDOW, timeout=15)
        if result.returncode:
            raise RuntimeError("isolated schema operation failed: " + result.stderr[-1500:])
        return result.stdout.strip()

    def raw_request(url, body=None, headers=None, method=None):
        request_object = urllib.request.Request(url, data=body, headers=headers or {}, method=method)
        try:
            response = opener.open(request_object, timeout=5)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            data = response.read(4 * 1024 * 1024 + 1)
            assert len(data) <= 4 * 1024 * 1024, "response exceeds smoke budget"
            response_headers = {name: response.headers.get_all(name) for name in response.headers.keys()}
            return response.status, data.decode("utf-8"), response_headers

    def request(method, path, body=None, expected=200):
        headers = {"Authorization": "Bearer " + admin}
        encoded = None
        if body is not None:
            encoded = json.dumps(body, separators=(",", ":")).encode()
            headers["Content-Type"] = "application/json"
        status, text, _ = raw_request(origin + path, encoded, headers, method)
        assert status == expected, f"{method} {path}: HTTP {status}, expected {expected}: {text}"
        return json.loads(text) if text else None

    def start_server():
        log = (directory / f"server-{len(processes)}.log").open("wb")
        logs.append(log)
        process = subprocess.Popen([args.server, "serve"], cwd=ROOT, env=env,
                                   stdout=log, stderr=log, creationflags=NO_WINDOW)
        processes.append(process)

        def ready():
            assert process.poll() is None, f"backend exited; inspect {directory}"
            try:
                return request("GET", "/readyz")["status"] == "ready"
            except (OSError, AssertionError):
                return False
        until(ready)
        return process

    def passed(message):
        checks.append(message)
        print("PASS " + message, flush=True)

    class Fixture:
        def __init__(self, module, token, role, version, tagged=False):
            self.role, self.session = role, None
            self.drop_next_inventory = False
            self.lost_body = None
            self.inventory_bodies = []
            self.reads = {}
            self.runtime = module.LuaRuntime(unpack_returned_tuples=True)
            self.globals = self.runtime.globals()
            globals_ = self.globals
            globals_.TEST_ROOT = ROOT.as_posix()
            globals_.FIXTURE_URL, globals_.FIXTURE_TOKEN = origin, token
            globals_.FIXTURE_BRIDGE_MODE, globals_.FIXTURE_ROLE = "cooperative", role
            globals_.FIXTURE_TAGGED = tagged
            suffix = "-tagged" if tagged else ""
            globals_.FIXTURE_JOURNAL = (directory / f"lua-{version}-{role}{suffix}").as_posix()
            globals_.fixture_now, globals_.fixture_sleep = time.monotonic, time.sleep

            def card_request(url, body, headers):
                assert url.startswith(origin + "/bridge/v2/"), "cooperative fixture used a legacy endpoint"
                status, text, response_headers = raw_request(url, body.encode(), dict(headers.items()), "POST")
                path = url.removeprefix(origin)
                wire.append({"role": role, "path": path, "body": body, "status": status, "response": text})
                assert status == 200, f"{role} {path}: HTTP {status}: {text}; payload={body}"
                decoded = json.loads(text)
                if path.endswith("/hello"):
                    self.session = decoded["session_id"]
                    if tagged and role == "primary":
                        assert decoded["limits"]["background_recipe_inconclusive"] is True
                        if args.background_recipe_negotiation == "absent":
                            del decoded["limits"]["background_recipe_inconclusive"]
                            text = json.dumps(decoded, separators=(",", ":"))
                            response_headers = {name: values for name, values in response_headers.items()
                                                if name.lower() != "content-length"}
                            response_headers["Content-Length"] = [str(len(text.encode("utf-8")))]
                if path.endswith("/poll"):
                    for read in decoded.get("reads", []):
                        self.reads[read["assignment_id"]] = read
                    assert role == "primary" or not decoded.get("commands"), "worker received craft command"
                if path.endswith("/inventory"):
                    self.inventory_bodies.append(body)
                    if self.drop_next_inventory and json.loads(body).get("updates"):
                        self.drop_next_inventory = False
                        self.lost_body = body
                        # Discard the real committed response at the card
                        # boundary; the client must keep its frozen body.
                        raise OSError("fixture discarded committed inventory response")
                return status, text, self.runtime.table_from({
                    name: self.runtime.table_from(values) for name, values in response_headers.items()
                })

            globals_.fixture_http = card_request
            self.runtime.execute("package.path = TEST_ROOT .. '/oc/lib/?.lua;' .. TEST_ROOT .. '/oc/tests/?.lua;' .. package.path")
            try:
                self.runtime.execute((ROOT / "scripts/fixtures/oc_http.lua").read_text(encoding="utf-8"))
            except Exception as error:
                raise AssertionError(f"{role} fixture startup failed: {error}; last wire={wire[-1:]}") from error

        def tick(self, allow_loss=False):
            try:
                status = self.globals.fixture_tick()
            except Exception as error:
                raise AssertionError(f"{self.role} fixture tick failed: {error}; last wire={wire[-1:]}") from error
            assert status["control_ok"], f"{self.role} control transport failed; last wire={wire[-1:]}"
            assert status["inventory_ok"] or allow_loss, f"{self.role} inventory transport failed; last wire={wire[-1:]}"
            return status

    created_schema = False
    try:
        sql(f'CREATE SCHEMA "{schema}";')
        created_schema = True
        backend = start_server()
        from lupa import lua52, lua53, lua54
        modules = {"5.2": lua52, "5.3": lua53, "5.4": lua54}
        for version in args.lua:
            created = request("POST", "/api/v1/networks",
                              {"name": "Cooperative Lua " + version, "server_group": "cooperative-" + version,
                               "source": "simulator"}, 201)
            network_id = created["network"]["id"]
            base = "/api/v1/networks/" + network_id
            worker_created = request("POST", base + "/devices", {"name": "Quantity reader B"}, 201)
            assert worker_created["device_token"] != created["device_token"]
            assert worker_created["device"]["role"] == "quantity_worker"
            request("POST", base + "/cooperation", {})
            a = Fixture(modules[version], created["device_token"], "primary", version)
            b = Fixture(modules[version], worker_created["device_token"], "quantity_worker", version)
            assert a.session and b.session and a.session != b.session

            def drive(fixtures, check, timeout=20, allow_loss=False):
                def tick():
                    for fixture in fixtures:
                        fixture.tick(allow_loss)
                    return check()
                return until(tick, timeout)

            drive([a, b], lambda: request("GET", base + "/inventory-stats")["entries"] == "2")
            rows = request("GET", base + "/items")["items"]
            exact = next(row for row in rows if row["name"] == "fixture:exact")
            target = next(row for row in rows if row["name"] == "fixture:craftable")
            assert exact["quantity"] == "9223372036854775807" and target["craftable"] is True
            passed(f"Lua {version}: independent A/B credentials and sessions; primary discovers exact int64 stock")
            lookup_path = base + "/items/lookup?ids=" + target["id"]
            recipe_before = request("GET", lookup_path)["items"][0]["craftable_observed_at"]
            b.globals.fixture_stock = "9007199254740993"
            generation = request("POST", base + "/items/refresh", {"ids": [target["id"]]}, 202)["refreshes"][0]
            b.globals.FIXTURE_FREE_MEMORY = 4096
            inventory_before = len(b.inventory_bodies)
            b.tick()
            low_memory_poll = next(entry for entry in reversed(wire)
                                   if entry["role"] == "quantity_worker" and entry["path"].endswith("/poll"))
            low_memory_body = json.loads(low_memory_poll["body"])
            assert low_memory_body["read_slots"] == 0 and low_memory_body["read_bytes"] == 0, "low memory did not apply read backpressure"
            assert len(b.inventory_bodies) == inventory_before, "low memory performed inventory work"
            retained = request("GET", lookup_path)
            assert retained["refreshes"][0]["state"] == "pending" and retained["items"][0]["quantity"] == target["quantity"]
            b.globals.FIXTURE_FREE_MEMORY = 131072

            def confirmed(quantity, request_id):
                view = request("GET", lookup_path)
                if not view["refreshes"] or view["refreshes"][0]["state"] != "confirmed":
                    return False
                assert view["refreshes"][0]["request_id"] == request_id
                assert view["items"][0]["quantity"] == quantity, view
                return view

            refreshed = drive([b], lambda: confirmed("9007199254740993", generation["request_id"]))
            assert refreshed["items"][0]["craftable"] is True
            assert refreshed["items"][0]["craftable_observed_at"] == recipe_before
            receipts = [update for body in b.inventory_bodies for update in json.loads(body)["updates"]]
            assert any(update.get("id") == target["id"] and update["craftable"] is None and
                       b.reads[update["assignment_id"]]["purpose"] == "refresh" for update in receipts)
            assert b.globals.fixture_requests == 0
            passed(f"Lua {version}: B confirms 9007199254740993 with craftable:null and preserves A recipe evidence")
            passed(f"Lua {version}: low-memory B advertises zero read capacity, retains pending refresh, then recovers over HTTP")

            order = request("POST", base + "/orders",
                            {"item_id": target["id"], "quantity": "64", "idempotency_key": "cooperative-order"}, 201)
            drive([a], lambda: request("GET", base + "/orders/" + order["id"])["state"] == "done")
            drive([a], lambda: request("GET", lookup_path)["items"][0]["quantity"] == "74")
            joint = [read for read in a.reads.values() if read["purpose"] == "order" and read["kind"] == "quantity_recipe"]
            assert joint, "post-craft joint verification was not assigned to A"
            joint_ids = {read["assignment_id"] for read in joint}
            joint_receipts = [update for body in a.inventory_bodies for update in json.loads(body)["updates"]
                              if update.get("assignment_id") in joint_ids]
            assert any(update["quantity"] == "74" and update["craftable"] is True for update in joint_receipts)
            assert a.globals.fixture_requests == 1 and b.globals.fixture_requests == 0
            passed(f"Lua {version}: exactly one craft on A; post-craft joint quantity/recipe receipt produces stock 74")

            sql(f"""UPDATE "{schema}".devices SET last_seen=now()-interval '1 hour'
                WHERE network_id='{network_id}' AND role='primary';
                UPDATE "{schema}".networks SET last_seen=now()-interval '1 hour' WHERE id='{network_id}';""")
            b.tick()
            offline = request("GET", base)
            assert offline["online"] is False and offline["session_id"] == a.session
            assert offline["services"]["quantity_readers"] == 1
            assert all(offline["services"][key] is False for key in ("discovery", "recipes", "craft"))
            passed(f"Lua {version}: A offline with B online leaves quantity reader available, no primary services")

            # A committed receipt loses its response; retain both runtime sessions
            # across a real backend process restart, then retry identical bytes.
            b.globals.fixture_stock = 103
            b.drop_next_inventory = True
            retry_generation = request("POST", base + "/items/refresh", {"ids": [target["id"]]}, 202)["refreshes"][0]
            drive([b], lambda: b.lost_body, allow_loss=True)
            frozen = b.lost_body
            committed = confirmed("103", retry_generation["request_id"])
            assert committed, "lost response was not committed by real backend"
            checkpoint_sql = f"""SELECT id||':'||inventory_seq||':'||poll_seq||':'||retired
                FROM "{schema}".bridge_sessions WHERE network_id='{network_id}' ORDER BY id;"""
            checkpoints = sql(checkpoint_sql)
            evidence = request("GET", lookup_path)
            stop(backend)
            backend = start_server()
            assert sql(checkpoint_sql) == checkpoints, "restart changed persistent sessions or ACKs"
            before_retry = len(b.inventory_bodies)
            b.tick()
            assert len(b.inventory_bodies) > before_retry and b.inventory_bodies[before_retry] == frozen, "retry body mutated"
            assert request("GET", lookup_path) == evidence, "immutable retry changed accepted evidence"
            assert b.session in checkpoints and a.session in checkpoints
            passed(f"Lua {version}: lost inventory response retries immutable bytes after owned backend restart; sessions/ACK/evidence preserved")
            assert a.globals.fixture_framed_eof_reads == 0 and b.globals.fixture_framed_eof_reads == 0
            assert a.globals.fixture_framed_requests > 0 and b.globals.fixture_framed_requests > 0
            passed(f"Lua {version}: separate bounded Lua fixtures use real framed HTTP without forbidden worker hardware calls")

            # Keep tagged stock and failure injection separate from all earlier
            # stock/order expectations, including the shared v1 fixture defaults.
            tagged = request("POST", "/api/v1/networks",
                             {"name": "Tagged recipe " + version, "server_group": "tagged-" + version,
                              "source": "simulator"}, 201)
            network_id = tagged["network"]["id"]
            base = "/api/v1/networks/" + network_id
            worker = request("POST", base + "/devices", {"name": "Tagged quantity reader"}, 201)
            request("POST", base + "/cooperation", {})
            primary = Fixture(modules[version], tagged["device_token"], "primary", version, tagged=True)
            reader = Fixture(modules[version], worker["device_token"], "quantity_worker", version, tagged=True)
            drive([primary], lambda: request("GET", base + "/inventory-stats")["entries"] == "2")
            target = next(row for row in request("GET", base + "/items")["items"]
                          if row["name"] == "fixture:craftable")
            lookup_path = base + "/items/lookup?ids=" + target["id"]
            item_where = f"network_id='{network_id}' AND item_id={target['id']}"
            sql(f"""UPDATE "{schema}".inventory_schedule SET next_quantity_at=now()+interval '1 hour',
                next_verify_at=now()+interval '1 hour' WHERE network_id='{network_id}';""")
            drive([primary, reader], lambda: sql(f"""SELECT count(*) FROM "{schema}".cooperative_read_fields
                WHERE network_id='{network_id}' AND completed_at IS NULL AND expires_at>now();""") == "0")

            def due_recipe():
                sql(f"""UPDATE "{schema}".inventory_schedule SET next_verify_at=now()-interval '1 second'
                    WHERE {item_where};""")

            def assigned_receipts(fixture):
                return [update for body in fixture.inventory_bodies for update in json.loads(body)["updates"]
                        if update.get("id") == target["id"] and update.get("assignment_id")]

            def recipe_evidence():
                # Include every craftable_* column, not just the public timestamp.
                return json.loads(sql(f"""SELECT jsonb_object_agg(key,value) FROM "{schema}".inventory i,
                    LATERAL jsonb_each(to_jsonb(i)) WHERE {item_where}
                    AND (key='craftable' OR key LIKE 'craftable\\_%' ESCAPE '\\');"""))

            def accepted_state():
                return sql(f"""SELECT jsonb_build_object(
                    'inventory',(SELECT to_jsonb(i) FROM "{schema}".inventory i WHERE {item_where}),
                    'schedule',(SELECT to_jsonb(s) FROM "{schema}".inventory_schedule s WHERE {item_where}),
                    'fields',(SELECT jsonb_agg(to_jsonb(f) ORDER BY field)
                        FROM "{schema}".cooperative_read_fields f WHERE {item_where}))::text;""")

            # Capture prior recipe evidence through a separate successful native
            # recipe call and committed joint receipt before enabling uncertainty.
            seeded_at = len(primary.inventory_bodies)
            due_recipe()
            drive([primary], lambda: any(update.get("id") == target["id"] and update.get("craftable") is True
                                        and update.get("assignment_id") for body in primary.inventory_bodies[seeded_at:]
                                        for update in json.loads(body)["updates"]))
            before_recipe_evidence = recipe_evidence()
            assert before_recipe_evidence["craftable"] is True
            assert before_recipe_evidence["craftable_session_id"] == primary.session
            assert before_recipe_evidence["craftable_observed_at"] is not None
            assert primary.globals.fixture_recipe_calls > 0 and primary.globals.fixture_recipe_failures == 0

            primary.globals.fixture_stock = "9007199254740993"
            primary.globals.fixture_recipe_failure = True
            exact_reads_before = primary.globals.fixture_exact_reads
            receipts_before = len(assigned_receipts(primary))
            primary.drop_next_inventory = True
            due_recipe()
            drive([primary], lambda: primary.globals.fixture_recipe_failures == 1,
                  allow_loss=args.background_recipe_negotiation == "normal")
            assert primary.globals.fixture_exact_reads == exact_reads_before + 1
            assert primary.globals.fixture_lookup_failures == 0
            current_quantity = request("GET", lookup_path)["items"][0]["quantity"]
            if args.background_recipe_negotiation == "absent":
                assert current_quantity == "10" and len(assigned_receipts(primary)) == receipts_before
                assert primary.lost_body is None, "negative control unexpectedly produced an inventory receipt"
                print(f"RED evidence Lua {version}: exact read succeeded; recipe failed; quantity remains 10; no receipt",
                      flush=True)
            assert current_quantity == "9007199254740993", (
                f"Lua {version}: background recipe failure withheld validated quantity 9007199254740993 "
                f"(accepted quantity remains {current_quantity})")
            assert primary.lost_body is not None, "inconclusive receipt did not reach ACK-loss boundary"
            first_receipt = json.loads(primary.lost_body)["updates"][0]
            assert first_receipt["id"] == target["id"] and first_receipt["quantity"] == "9007199254740993"
            assert first_receipt["recipe_inconclusive"] is True
            assert first_receipt["craftable"] is None
            assignment = first_receipt["assignment_id"]
            read = primary.reads[assignment]
            assert read["kind"] == "quantity_recipe" and read["purpose"] == "background"
            assert first_receipt["observation"].isdigit()
            after_recipe_evidence = recipe_evidence()
            assert after_recipe_evidence == before_recipe_evidence
            claims = json.loads(sql(f"""SELECT jsonb_agg(to_jsonb(f) ORDER BY field)
                FROM "{schema}".cooperative_read_fields f WHERE {item_where};"""))
            assert len(claims) == 2 and {claim["field"] for claim in claims} == {"quantity", "recipe"}
            assert all(claim["assignment_id"] == assignment and claim["session_id"] == primary.session
                       and claim["completed_at"] is not None for claim in claims)
            active_claim_count = sum(claim["completed_at"] is None for claim in claims)
            assert active_claim_count == 0
            delays = json.loads(sql(f"""SELECT jsonb_build_array(
                extract(epoch FROM next_quantity_at-now()),extract(epoch FROM next_verify_at-now()))
                FROM "{schema}".inventory_schedule WHERE {item_where};"""))
            assert all(290 <= delay <= 300 for delay in delays), "default RecheckAfter did not defer both schedules"
            passed(f"Lua {version}: tagged background uncertainty accepts exact 9007199254740993, preserves all recipe evidence, completes both claims and defers recheck 300s")

            state_before_retry = accepted_state()
            exact_reads_after = primary.globals.fixture_exact_reads
            recipe_calls_after = primary.globals.fixture_recipe_calls
            inventory_before_retry = len(primary.inventory_bodies)
            primary.tick()
            assert len(primary.inventory_bodies) == inventory_before_retry + 1
            assert primary.inventory_bodies[-1] == primary.lost_body
            assert primary.globals.fixture_exact_reads == exact_reads_after
            assert primary.globals.fixture_recipe_calls == recipe_calls_after
            assert accepted_state() == state_before_retry, "replay mutated accepted quantity, evidence, claims or schedule"
            passed(f"Lua {version}: lost committed inconclusive ACK retries byte-identical body without hardware read or state mutation")

            reader.globals.fixture_stock = "9007199254740995"
            requested_refresh = request("POST", base + "/items/refresh", {"ids": [target["id"]]}, 202)["refreshes"][0]
            assert requested_refresh["state"] == "pending"
            refreshed = drive([reader], lambda: confirmed("9007199254740995", requested_refresh["request_id"]))
            requested_refresh = refreshed["refreshes"][0]
            assert requested_refresh["state"] == "confirmed"
            assert recipe_evidence() == before_recipe_evidence
            assert any(update["quantity"] == "9007199254740995" and update["craftable"] is None
                       and "recipe_inconclusive" not in update
                       and reader.reads[update["assignment_id"]]["purpose"] == "refresh"
                       for update in assigned_receipts(reader))
            assert reader.globals.fixture_recipe_calls == 0 and reader.globals.fixture_requests == 0
            assert primary.globals.fixture_requests == 0
            assert primary.globals.fixture_framed_eof_reads == 0 and reader.globals.fixture_framed_eof_reads == 0
            passed(f"Lua {version}: B confirms later user refresh 9007199254740995 immediately despite deferred background schedule; recipe evidence unchanged")
        summary["status"] = "passed"
    except BaseException:
        summary["status"] = "failed"
        summary["failure"] = traceback.format_exc()
        raise
    finally:
        for process in reversed(processes):
            stop(process)
        for log in logs:
            log.close()
        summary["owned_processes_stopped"] = all(process.poll() is not None for process in processes)
        if created_schema:
            sql(f'DROP SCHEMA "{schema}" CASCADE;')
            summary["schema_removed"] = sql(f"SELECT count(*) FROM pg_namespace WHERE nspname='{schema}';") == "0"
        (directory / "wire.json").write_text(json.dumps(wire, indent=2) + "\n", encoding="utf-8")
        (directory / "result.json").write_text(json.dumps(summary, indent=2) + "\n", encoding="utf-8")
        print(f"Artifacts: {directory}", flush=True)
    assert summary["owned_processes_stopped"] and summary["schema_removed"], "owned resource cleanup failed"
    print("PASS owned backend processes stopped and randomized schema removed", flush=True)


if __name__ == "__main__":
    main()
