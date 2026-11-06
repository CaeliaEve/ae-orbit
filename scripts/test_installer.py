"""Run the real installer in Lua 5.2/5.3/5.4 against a temporary OpenOS disk.

Only the OC component/filesystem boundary is replaced. Downloads, validation,
file writes, updates, rollback and bridge journal writes execute production Lua.
"""
import json
import pathlib
import tempfile
import unittest

from lupa import lua52, lua53, lua54


ROOT = pathlib.Path(__file__).resolve().parents[1]
REPOSITORY = "CaeliaEve/ae-orbit"
COMMIT = "0123456789abcdef0123456789abcdef01234567"
API = f"https://api.github.com/repos/{REPOSITORY}/commits/"
RAW = f"https://raw.githubusercontent.com/{REPOSITORY}/{COMMIT}/oc/"
SERVICE = "https://ae.example.test"
DESTINATIONS = {
    **{f"lib/aeorbit/{name}.lua": f"/lib/aeorbit/{name}.lua" for name in (
        "quantity", "json", "base64", "cache", "journal", "client", "adapter", "tracker", "runner")},
    "bin/ae-orbit.lua": "/bin/ae-orbit.lua",
    "config.example.lua": "/etc/ae-orbit.cfg.example",
    "install.lua": "/bin/ae-orbit-install.lua",
}


class OpenOS:
    def __init__(self, module):
        self.disk = tempfile.TemporaryDirectory(prefix="ae-orbit-install-")
        self.root = pathlib.Path(self.disk.name)
        self.lua = module.LuaRuntime(unpack_returned_tuples=True)
        self.now = 0
        self.requests = []
        self.handles = []
        self.overrides = {}
        self.rename_failure = None
        self.rename_failed = False
        self.messages = []
        self.sources = {source: (ROOT / "oc" / source).read_text(encoding="utf-8")
                        for source in DESTINATIONS}
        globals_ = self.lua.globals()
        globals_.map_path = lambda path: str(self.path(path))
        globals_.capture_print = lambda *args: self.messages.append(" ".join(map(str, args)))
        globals_.sleep = self.sleep
        globals_.test_filesystem = self.lua.table_from({
            "path": lambda path: str(pathlib.PurePosixPath(path).parent),
            "exists": lambda path: self.path(path).exists(),
            "isDirectory": lambda path: self.path(path).is_dir(),
            "isLink": lambda path: self.path(path).is_symlink(),
            "makeDirectory": self.mkdir,
            "remove": self.remove,
            "rename": self.rename,
        })
        internet = self.lua.table_from({"request": self.request})
        globals_.test_component = self.lua.table_from({"getPrimary": lambda name: internet if name == "internet" else None})
        globals_.test_computer = self.lua.table_from({"uptime": lambda: self.now})
        self.lua.execute('''
            package.loaded.filesystem = test_filesystem
            package.loaded.component = test_component
            package.loaded.computer = test_computer
            local open, load_file = io.open, loadfile
            function io.open(path, mode)
                if fail_open and path:find(fail_open, 1, true) then return nil, "disk full" end
                local file, reason = open(map_path(path), mode)
                if not file then return nil, reason end
                return setmetatable({}, {__index = function(_, method)
                    return function(_, ...)
                        if method == "flush" and fail_flush and path:find(fail_flush, 1, true) then
                            return nil, "injected flush failure"
                        end
                        local result, err = file[method](file, ...)
                        if method == "close" then
                            if not result then return result, err end
                            if fail_close and path:find(fail_close, 1, true) then
                                if close_error_mode == "throw" then error("injected close failure") end
                                if close_error_mode == "false" then return false, "injected close failure" end
                                return nil, "injected close failure"
                            end
                            -- GTNH OpenOS buffer.close forwards filesystem.close,
                            -- whose successful component call returns no values.
                            if not standard_lua_close then return end
                        end
                        return result, err
                    end
                end})
            end
            function loadfile(path, mode, env) return load_file(map_path(path), mode, env or _ENV) end
            os.sleep = sleep
            print = capture_print
        ''')
        self.install = self.lua.compile((ROOT / "oc/install.lua").read_text(encoding="utf-8"), name="=install.lua")

    def close(self):
        self.disk.cleanup()

    def path(self, virtual):
        path = pathlib.PurePosixPath(virtual)
        if not path.is_absolute() or ".." in path.parts:
            raise AssertionError("installer escaped the OpenOS disk")
        return self.root.joinpath(*path.parts[1:])

    def write(self, virtual, content):
        path = self.path(virtual)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8", newline="")

    def snapshot(self):
        return {"/" + path.relative_to(self.root).as_posix(): path.read_bytes()
                for path in self.root.rglob("*") if path.is_file()}

    def mkdir(self, virtual):
        if self.path(virtual).exists():
            return False, "file or directory already exists"
        self.path(virtual).mkdir(parents=True, exist_ok=True)
        return True

    def remove(self, virtual):
        path = self.path(virtual)
        if not path.exists():
            return False, "no such file"
        if path.is_dir():
            path.rmdir()
        else:
            path.unlink()
        return True

    def rename(self, old, new):
        if new == self.rename_failure and not self.rename_failed:
            self.rename_failed = True
            return False, "injected rename failure"
        source, target = self.path(old), self.path(new)
        if target.exists() or not source.exists():
            return False, "destination exists or source missing"
        source.rename(target)
        return True

    def sleep(self, duration):
        self.now += max(duration, 0.01)

    def request(self, url, body=None, headers=None, method=None):
        self.requests.append(url)
        if url.startswith(API):
            assert headers and headers["Accept"] == "application/vnd.github.sha", "commit lookup must request the small SHA response"
            assert headers["User-Agent"], "GitHub requires a User-Agent"
            data = COMMIT
        elif url.startswith(RAW):
            data = self.sources[url[len(RAW):]]
        elif url.startswith(SERVICE + "/oc/"):
            data = self.sources[url[len(SERVICE + "/oc/"):]]
        else:
            raise AssertionError("unexpected or unpinned download: " + url)
        overrides = self.overrides.get(url, {})
        data = overrides.get("data", data)
        code = overrides.get("code", 200)
        state = {"closed": False, "offset": 0}
        self.handles.append(state)
        length = overrides.get("length", str(len(data.encode("utf-8"))))
        response_headers = self.lua.table_from(
            {"content-length": self.lua.table_from([length])} if length is not None else {})

        def connect():
            if overrides.get("connect_error"):
                raise RuntimeError("connection failed after HTTP status became available")
            return not overrides.get("hang")

        def read(size):
            assert size <= 1024, "OC downloads must use bounded chunks"
            if overrides.get("stall"):
                return ""
            offset = state["offset"]
            if offset >= len(data):
                if overrides.get("read_error"):
                    return None, "connection reset"
                return None
            chunk = data[offset:offset + size]
            state["offset"] += len(chunk)
            return chunk

        def close():
            state["closed"] = True

        return self.lua.table_from({"finishConnect": connect,
                                    "response": lambda: (code, "fixture response", response_headers),
                                    "read": read, "close": close})

    def seed_upgrade(self):
        for destination in DESTINATIONS.values():
            self.write(destination, "-- installed version\nreturn 'old'\n")
        self.write("/etc/ae-orbit.cfg", "return {token='keep-device-token'}\n")
        self.write("/var/lib/ae-orbit/journal.0", "keep execution history exactly")

    def run_bridge(self):
        """Exercise the real entry point and journal with a bounded runner harness."""
        for source, destination in DESTINATIONS.items():
            self.write(destination, self.sources[source])
        self.write("/etc/ae-orbit.cfg", 'return {me_address="test-me",state_dir="/var/lib/ae-orbit"}\n')
        self.lua.globals().TEST_ROOT = (ROOT / "oc").as_posix()
        self.lua.execute('''
            package.path = TEST_ROOT .. "/lib/?.lua;" .. package.path
            test_component.proxy = function() return {} end
            package.loaded.event = {listen = function() end, ignore = function() end}
            package.loaded["aeorbit.adapter"] = {new = function() return {} end}
            package.loaded["aeorbit.client"] = {new = function() return {} end}
            package.loaded["aeorbit.runner"] = {new = function(_, dependencies)
                return {
                    session = "test-session", diagnose = function() end,
                    start = function()
                        dependencies.journal:save({attempts={},completed={},boot_id="test-boot"})
                    end,
                    tick = function() error("test finished", 0) end
                }
            end}
            io.stderr = {write = function(_, text) capture_print(text) end}
            assert(loadfile("/bin/ae-orbit.lua"))()
        ''')


class InstallerTests(unittest.TestCase):
    runtime = lua52

    def setUp(self):
        self.oc = OpenOS(self.runtime)
        self.addCleanup(self.oc.close)

    def assertClosed(self):
        self.assertTrue(all(handle["closed"] for handle in self.oc.handles), "HTTP handle leaked")

    def assertInstalled(self):
        for source, destination in DESTINATIONS.items():
            self.assertEqual(self.oc.path(destination).read_text(encoding="utf-8"), self.oc.sources[source])
        self.assertClosed()

    def test_default_install_pins_all_files_and_creates_initial_config(self):
        self.oc.install()
        self.assertInstalled()
        self.assertEqual(self.oc.path("/etc/ae-orbit.cfg").read_text(encoding="utf-8"), self.oc.sources["config.example.lua"])
        self.assertEqual(self.oc.requests.count(API + "main"), 1)
        self.assertEqual(sum(url.endswith("/config.example.lua") for url in self.oc.requests), 1)

    def test_standard_lua_close_return_is_also_supported(self):
        self.oc.lua.globals().standard_lua_close = True
        self.oc.install()
        self.assertInstalled()

    def test_installs_without_collectgarbage_in_the_openos_sandbox(self):
        self.oc.lua.globals().collectgarbage = None
        self.oc.install()
        self.assertInstalled()

    def test_upgrade_preserves_configuration_journal_and_unrelated_files(self):
        self.oc.seed_upgrade()
        self.oc.write("/home/notes.txt", "user file")
        before = self.oc.snapshot()
        self.oc.install("github")
        self.assertInstalled()
        for path in ("/etc/ae-orbit.cfg", "/var/lib/ae-orbit/journal.0", "/home/notes.txt"):
            self.assertEqual(self.oc.path(path).read_bytes(), before[path])
        self.assertEqual(set(self.oc.snapshot()), set(before), "staging or backup files left after success")

    def test_full_commit_skips_the_rate_limited_api(self):
        self.oc.install("github", COMMIT)
        self.assertInstalled()
        self.assertFalse(any(url.startswith(API) for url in self.oc.requests))

    def test_branch_names_are_url_encoded_and_then_pinned(self):
        self.oc.install("github", "release/next")
        self.assertInstalled()
        self.assertIn(API + "release%2Fnext", self.oc.requests)

    def test_existing_backend_download_mode_remains_available(self):
        self.oc.install(SERVICE + "/")
        self.assertInstalled()
        self.assertTrue(all(url.startswith(SERVICE + "/oc/") for url in self.oc.requests))

    def test_updater_installed_on_disk_can_run_again(self):
        self.oc.install()
        updater = self.oc.lua.compile(self.oc.path("/bin/ae-orbit-install.lua").read_text(encoding="utf-8"))
        self.oc.sources["lib/aeorbit/quantity.lua"] = "return {version='next'}\n"
        updater("github", COMMIT)
        self.assertInstalled()

    def test_http_error_does_not_replace_any_program_or_leave_staging_files(self):
        self.oc.seed_upgrade()
        before = self.oc.snapshot()
        self.oc.overrides[RAW + "lib/aeorbit/json.lua"] = {"code": 404, "connect_error": True}
        with self.assertRaisesRegex(Exception, "404"):
            self.oc.install("github", COMMIT)
        self.assertEqual(self.oc.snapshot(), before)
        self.assertClosed()

    def test_git_api_rate_limit_explains_commit_sha_fallback(self):
        self.oc.overrides[API + "main"] = {"code": 403, "connect_error": True}
        with self.assertRaisesRegex(Exception, "[Ss][Hh][Aa]"):
            self.oc.install()
        self.assertEqual(self.oc.snapshot(), {})
        self.assertClosed()

    def test_invalid_commit_response_cannot_control_the_download_host(self):
        self.oc.overrides[API + "main"] = {"data": "../../malicious.example"}
        with self.assertRaisesRegex(Exception, "[Cc]ommit|SHA"):
            self.oc.install()
        self.assertEqual(self.oc.snapshot(), {})
        self.assertClosed()

    def test_oversized_file_aborts_without_replacing_the_old_install(self):
        self.oc.seed_upgrade()
        before = self.oc.snapshot()
        self.oc.overrides[RAW + "lib/aeorbit/json.lua"] = {"data": "--padding\n" * 20000, "length": None}
        with self.assertRaisesRegex(Exception, "budget|large|limit"):
            self.oc.install("github", COMMIT)
        self.assertEqual(self.oc.snapshot(), before)
        self.assertClosed()

    def test_syntax_error_is_rejected_before_any_file_is_replaced(self):
        self.oc.seed_upgrade()
        before = self.oc.snapshot()
        self.oc.overrides[RAW + "lib/aeorbit/json.lua"] = {"data": "function broken("}
        with self.assertRaisesRegex(Exception, "Lua|syntax"):
            self.oc.install("github", COMMIT)
        self.assertEqual(self.oc.snapshot(), before)
        self.assertClosed()

    def test_valid_lua_truncated_by_a_read_error_is_not_installed(self):
        self.oc.seed_upgrade()
        before = self.oc.snapshot()
        self.oc.overrides[RAW + "lib/aeorbit/json.lua"] = {"data": "return {}\n", "read_error": True}
        with self.assertRaises(Exception):
            self.oc.install("github", COMMIT)
        self.assertEqual(self.oc.snapshot(), before)
        self.assertClosed()

    def test_content_length_detects_a_silent_truncated_file(self):
        self.oc.seed_upgrade()
        before = self.oc.snapshot()
        self.oc.overrides[RAW + "lib/aeorbit/json.lua"] = {"data": "return {}\n", "length": "1000"}
        with self.assertRaisesRegex(Exception, "length|[Tt]runcat|[Ii]ncomplete"):
            self.oc.install("github", COMMIT)
        self.assertEqual(self.oc.snapshot(), before)
        self.assertClosed()

    def test_stalled_download_times_out_and_closes_the_handle(self):
        self.oc.overrides[RAW + "lib/aeorbit/json.lua"] = {"stall": True}
        with self.assertRaisesRegex(Exception, "[Tt]imeout|timed out"):
            self.oc.install("github", COMMIT)
        self.assertEqual(self.oc.snapshot(), {})
        self.assertLess(self.oc.now, 61)
        self.assertClosed()

    def test_disk_full_leaves_the_old_install_intact(self):
        self.oc.seed_upgrade()
        before = self.oc.snapshot()
        self.oc.lua.globals().fail_open = "/lib/aeorbit/json.lua"
        with self.assertRaisesRegex(Exception, "disk full"):
            self.oc.install("github", COMMIT)
        self.assertEqual(self.oc.snapshot(), before)
        self.assertClosed()

    def test_flush_failure_leaves_the_old_install_intact(self):
        self.oc.seed_upgrade()
        before = self.oc.snapshot()
        self.oc.lua.globals().fail_flush = "/lib/aeorbit/json.lua"
        with self.assertRaisesRegex(Exception, "flush failure"):
            self.oc.install("github", COMMIT)
        self.assertEqual(self.oc.snapshot(), before)
        self.assertClosed()

    def test_close_failure_leaves_the_old_install_intact(self):
        self.oc.seed_upgrade()
        before = self.oc.snapshot()
        self.oc.lua.globals().fail_close = "/lib/aeorbit/json.lua"
        for mode in ("nil", "false", "throw"):
            with self.subTest(mode=mode):
                self.oc.lua.globals().close_error_mode = mode
                with self.assertRaisesRegex(Exception, "close failure"):
                    self.oc.install("github", COMMIT)
                self.assertEqual(self.oc.snapshot(), before)
                self.assertClosed()

    def test_initial_config_copy_close_failure_is_not_ignored(self):
        self.oc.lua.globals().fail_close = "/etc/ae-orbit.cfg.ae-orbit-new"
        with self.assertRaisesRegex(Exception, "close failure"):
            self.oc.install("github", COMMIT)
        self.assertEqual(self.oc.snapshot(), {})
        self.assertClosed()

    def test_bridge_entrypoint_persists_journal_with_openos_close(self):
        self.oc.run_bridge()
        self.assertIn("AE Orbit connected. Session: test-session", self.oc.messages)
        envelope = json.loads(self.oc.path("/var/lib/ae-orbit/journal.1").read_text(encoding="utf-8"))
        self.assertEqual(envelope["generation"], "1")
        self.assertEqual(json.loads(envelope["body"])["boot_id"], "test-boot")
        self.assertTrue(any("test finished" in message for message in self.oc.messages))

    def test_bridge_entrypoint_stops_if_journal_flush_fails(self):
        self.oc.lua.globals().fail_flush = "/var/lib/ae-orbit/journal.1"
        self.oc.run_bridge()
        self.assertFalse(any("AE Orbit connected." in message for message in self.oc.messages))
        self.assertTrue(any("journal: write failed" in message for message in self.oc.messages))

    def test_bridge_entrypoint_stops_if_journal_close_fails(self):
        self.oc.lua.globals().fail_close = "/var/lib/ae-orbit/journal.1"
        self.oc.run_bridge()
        self.assertFalse(any("AE Orbit connected." in message for message in self.oc.messages))
        self.assertTrue(any("journal: write failed" in message for message in self.oc.messages))

    def test_replacement_failure_rolls_back_every_changed_file(self):
        self.oc.seed_upgrade()
        before = self.oc.snapshot()
        self.oc.rename_failure = "/lib/aeorbit/json.lua"
        with self.assertRaisesRegex(Exception, "rename"):
            self.oc.install("github", COMMIT)
        self.assertTrue(self.oc.rename_failed, "failure was not reached")
        self.assertEqual(self.oc.snapshot(), before)
        self.assertClosed()

    def test_failed_first_install_removes_newly_created_programs(self):
        self.oc.rename_failure = "/lib/aeorbit/json.lua"
        with self.assertRaisesRegex(Exception, "rename"):
            self.oc.install("github", COMMIT)
        self.assertTrue(self.oc.rename_failed)
        self.assertEqual(self.oc.snapshot(), {})
        self.assertClosed()

    def test_untrusted_service_origin_is_rejected_before_network_or_disk_work(self):
        for origin in ("http://ae.example.test", "https://user:secret@ae.example.test", "https://ae.example.test/path"):
            with self.subTest(origin=origin), self.assertRaises(Exception):
                self.oc.install(origin)
        self.assertEqual(self.oc.requests, [])
        self.assertEqual(self.oc.snapshot(), {})

    def test_second_installer_cannot_modify_an_active_installation(self):
        self.oc.seed_upgrade()
        self.oc.write("/tmp/ae-orbit-install.lock/owner", "another installer owns this lock")
        before = self.oc.snapshot()
        with self.assertRaisesRegex(Exception, "lock|already running"):
            self.oc.install()
        self.assertEqual(self.oc.requests, [])
        self.assertEqual(self.oc.snapshot(), before)

    def test_a_stale_backup_is_preserved_for_recovery(self):
        self.oc.seed_upgrade()
        self.oc.write("/lib/aeorbit/json.lua.ae-orbit-old", "return 'recoverable version'\n")
        before = self.oc.snapshot()
        with self.assertRaisesRegex(Exception, "Previous installation|[Rr]ecovery"):
            self.oc.install("github", COMMIT)
        self.assertEqual(self.oc.snapshot(), before)

    def test_failed_download_can_be_retried_without_manual_cleanup(self):
        self.oc.seed_upgrade()
        self.oc.overrides[RAW + "lib/aeorbit/json.lua"] = {"data": ""}
        with self.assertRaisesRegex(Exception, "[Ee]mpty"):
            self.oc.install("github", COMMIT)
        self.oc.overrides.clear()
        self.oc.install("github", COMMIT)
        self.assertInstalled()


def main():
    suite = unittest.TestSuite()
    for module in (lua52, lua53, lua54):
        cls = type("Lua" + "".join(map(str, module.LUA_VERSION)) + "InstallerTests",
                   (InstallerTests,), {"runtime": module})
        suite.addTests(unittest.defaultTestLoader.loadTestsFromTestCase(cls))
    result = unittest.TextTestRunner(verbosity=1).run(suite)
    return 0 if result.wasSuccessful() else 1


if __name__ == "__main__":
    raise SystemExit(main())
