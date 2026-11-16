#!/usr/bin/env python3
"""Build an AE Orbit browser texture pack from an NESQL++ source directory."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import sys
import zipfile
from pathlib import Path


FORMAT = "ae-orbit.texture-pack"
VERSION = 1
MAX_ASSETS = 200_000


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source", type=Path, help="NESQL++ source directory")
    parser.add_argument("--output", type=Path, required=True, help="Output ZIP path")
    return parser.parse_args()


def safe_asset_path(value: object) -> str:
    path = str(value or "").replace("\\", "/")
    parts = path.split("/")
    if not path.startswith("assets/") or not path.endswith(".png"):
        return ""
    if path.startswith("/") or "\x00" in path or ":" in path:
        return ""
    if any(not part or part in (".", "..") for part in parts):
        return ""
    return path


def ensure_output_outside_source(source: Path, output: Path) -> None:
    source = source.resolve()
    output = output.resolve()
    if output == source or source in output.parents:
        raise ValueError("output must be outside the immutable NESQL++ source directory")


def asset_paths(source_manifest: dict[str, object], source: Path) -> list[str]:
    paths: list[str] = []
    for entry in source_manifest.get("files", []):
        if not isinstance(entry, dict) or entry.get("kind") not in ("asset", "assets"):
            continue
        path = safe_asset_path(entry.get("path"))
        if not path or not (source / path).is_file():
            continue
        paths.append(path)
    paths = sorted(set(paths))
    if not paths:
        raise ValueError("NESQL++ source contains no PNG assets")
    if len(paths) > MAX_ASSETS:
        raise ValueError("texture pack exceeds asset limit")
    return paths


def build_manifest(source_manifest: dict[str, object], paths: list[str]) -> dict[str, object]:
    if source_manifest.get("format") != "elysium.source":
        raise ValueError("source is not an NESQL++ elysium.source export")
    revision = source_manifest.get("revision")
    return {
        "format": FORMAT,
        "version": VERSION,
        "revision": str(revision) if revision is not None else "",
        "assets": paths,
    }


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1 << 20), b""):
            digest.update(block)
    return digest.hexdigest()


def write_pack(source: Path, output: Path) -> tuple[int, str]:
    source_manifest = json.loads((source / "manifest.json").read_text(encoding="utf-8"))
    paths = asset_paths(source_manifest, source)
    manifest = build_manifest(source_manifest, paths)
    output.parent.mkdir(parents=True, exist_ok=True)
    temporary = output.with_suffix(output.suffix + ".new")
    with zipfile.ZipFile(temporary, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=6) as archive:
        archive.writestr(
            zipfile.ZipInfo("manifest.json", date_time=(1980, 1, 1, 0, 0, 0)),
            json.dumps(manifest, ensure_ascii=False, separators=(",", ":")).encode("utf-8"),
        )
        for path in paths:
            info = zipfile.ZipInfo(path, date_time=(1980, 1, 1, 0, 0, 0))
            # PNG is already compressed; storing it avoids wasting build CPU.
            info.compress_type = zipfile.ZIP_STORED
            info.external_attr = 0o644 << 16
            archive.writestr(info, (source / path).read_bytes())
    os.replace(temporary, output)
    return len(paths), sha256(output)


def main() -> int:
    args = parse_args()
    source = args.source.resolve()
    output = args.output.resolve()
    if not source.is_dir():
        raise SystemExit(f"source is not a directory: {source}")
    try:
        ensure_output_outside_source(source, output)
        count, digest = write_pack(source, output)
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        print(f"texture pack build failed: {exc}", file=sys.stderr)
        return 1
    print(json.dumps({"output": str(output), "assets": count, "sha256": digest}, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
