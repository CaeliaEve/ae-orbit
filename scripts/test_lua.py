"""Execute OC modules in real Lua runtimes; hardware is replaced by explicit fixtures."""
import pathlib
import sys
from lupa import lua52, lua53, lua54
from test_installer import main as test_installer

root = pathlib.Path(__file__).resolve().parents[1]
files = sorted((root / "oc" / "tests").glob("test_*.lua"))
if not files:
    raise SystemExit("No Lua tests found")
failures = 0
for module in (lua52, lua53, lua54):
    compiler = module.LuaRuntime(unpack_returned_tuples=True)
    compiler.globals().TEST_ROOT = root.as_posix()
    compiler.execute("assert(loadfile(TEST_ROOT .. '/oc/bin/ae-orbit.lua')); assert(loadfile(TEST_ROOT .. '/oc/config.example.lua')); assert(loadfile(TEST_ROOT .. '/oc/install.lua'))")
    for source in files:
        runtime = module.LuaRuntime(unpack_returned_tuples=True)
        runtime.globals().TEST_ROOT = root.as_posix()
        runtime.execute("package.path = TEST_ROOT .. '/oc/lib/?.lua;' .. TEST_ROOT .. '/oc/tests/?.lua;' .. package.path")
        try:
            runtime.execute(source.read_text(encoding="utf-8"))
            print(f"PASS Lua {'.'.join(map(str,module.LUA_VERSION))}: {source.name}")
        except Exception as error:
            failures += 1
            print(f"FAIL Lua {module.LUA_VERSION}: {source.name}: {str(error).splitlines()[0]}", file=sys.stderr)
print(f"Lua test files: {len(files)*3}, failures: {failures}")
failures += test_installer()
raise SystemExit(bool(failures))
