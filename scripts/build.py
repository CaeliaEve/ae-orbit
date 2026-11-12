"""Build native service/simulator executables and the OC ZIP; optionally Linux amd64."""
import argparse
import os
import pathlib
import shutil
import subprocess
import zipfile

root = pathlib.Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--go", default=shutil.which("go") or str(root / ".tools/go/bin/go.exe"))
parser.add_argument("--linux", action="store_true", help="also cross-compile Linux amd64")
args = parser.parse_args()
distribution = root / "dist"
distribution.mkdir(exist_ok=True)
env = dict(os.environ, CGO_ENABLED="0")
env.pop("GOOS", None)
env.pop("GOARCH", None)


def build(destination, suffix, environment):
    destination.mkdir(parents=True, exist_ok=True)
    for command in ("ae-orbit", "simulator"):
        subprocess.run([args.go, "build", "-trimpath", "-ldflags=-s -w", "-o",
                        str(destination / (command + suffix)), "./cmd/" + command],
                       cwd=root, env=environment, check=True)


build(distribution, ".exe" if os.name == "nt" else "", env)
if args.linux:
    build(distribution / "linux-amd64", "", dict(env, GOOS="linux", GOARCH="amd64"))
with zipfile.ZipFile(distribution / "ae-orbit-oc.zip", "w", zipfile.ZIP_DEFLATED) as bundle:
    for source in sorted((root / "oc/lib/aeorbit").glob("*.lua")):
        bundle.write(source, source.relative_to(root / "oc").as_posix())
    bundle.write(root / "oc/bin/ae-orbit.lua", "bin/ae-orbit.lua")
    bundle.write(root / "oc/rc.d/ae-orbit.lua", "etc/rc.d/ae-orbit.lua")
    bundle.write(root / "oc/config.example.lua", "etc/ae-orbit.cfg.example")
    bundle.write(root / "oc/install.lua", "install.lua")
    bundle.write(root / "oc/install.lua", "bin/ae-orbit-install.lua")
    bundle.writestr("README.txt", "Copy bin/, lib/ and etc/rc.d/ to matching OpenOS directories. For a first install copy etc/ae-orbit.cfg.example\n"
                    "to /etc/ae-orbit.cfg and configure url, token and me_address. Stop the bridge before updating.\n"
                    "Update from GitHub with: ae-orbit-install\n"
                    "Reboot OpenOS after an update to reload cached modules. Autostart: rc ae-orbit enable\n"
                    "Alternatively use the running backend's /oc/install.lua installer. See the repository README.\n")
print("Built service, simulator and OC package in dist/.")
