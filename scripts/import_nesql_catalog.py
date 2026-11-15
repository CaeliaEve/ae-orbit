#!/usr/bin/env python3
"""Build a normalized, read-only NESQL catalog for AE Orbit.

The source export is never modified.  Only a generated manifest and a copied
image archive are written to the requested output directory.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import subprocess
import sys
import tempfile
from datetime import datetime, timezone
from pathlib import Path
from typing import Iterable


READER = Path(__file__).with_name("NesqlDumpReader.java")
MANIFEST_VERSION = 1
MAX_MANIFEST_ITEMS = 1_000_000


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source", type=Path, help="NESQL export directory")
    parser.add_argument("--hsqldb-jar", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--java", default="java")
    parser.add_argument("--javac", default="javac")
    parser.add_argument("--keep-ndjson", action="store_true")
    return parser.parse_args()


def clean(value: object) -> str:
    if value is None:
        return ""
    return str(value).strip()


def first(row: dict[str, object], *names: str) -> str:
    for name in names:
        value = clean(row.get(name.lower()))
        if value:
            return value
    return ""


def integer(row: dict[str, object], *names: str, default: int = 0) -> int:
    value = first(row, *names)
    if not value:
        return default
    try:
        return int(value)
    except ValueError as exc:
        raise ValueError(f"invalid integer {names[0]}={value!r}") from exc


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for block in iter(lambda: handle.read(1 << 20), b""):
            digest.update(block)
    return digest.hexdigest()


def image_path(value: str) -> str:
    value = value.replace("\\", "/").lstrip("./")
    if not value or "\x00" in value or value.startswith("/"):
        return ""
    parts = value.split("/")
    if any(part in ("", ".", "..") for part in parts) or ":" in value:
        return ""
    if not value.startswith("item/"):
        return ""
    return value


def source_files(source: Path) -> dict[str, object]:
    files = {}
    for name in ("image.zip", "nesql-db.script", "nesql-db.properties"):
        path = source / name
        if not path.is_file():
            raise FileNotFoundError(f"missing NESQL source file: {path}")
        files[name] = {"bytes": path.stat().st_size, "sha256": sha256(path)}
    return files


def read_rows(ndjson: Path) -> tuple[list[dict[str, str]], list[dict[str, str]]]:
    items: list[dict[str, str]] = []
    tooltips: list[dict[str, str]] = []
    with ndjson.open("r", encoding="utf-8") as handle:
        for line_number, line in enumerate(handle, 1):
            try:
                record = json.loads(line)
            except json.JSONDecodeError as exc:
                raise ValueError(f"invalid reader output at line {line_number}") from exc
            table = record.get("table")
            row = record.get("row")
            if not isinstance(row, dict):
                raise ValueError(f"reader row missing at line {line_number}")
            row = {str(key).lower(): value for key, value in row.items()}
            if table == "item":
                items.append(row)
            elif table == "item_tooltip":
                tooltips.append(row)
    return items, tooltips


def normalized_item(row: dict[str, str], tooltips: dict[str, list[str]]) -> dict[str, object]:
    item_id = first(row, "id", "item_id", "item_key")
    name = first(row, "internal_name", "name", "registry_name", "unlocalized_name")
    if not item_id or not name:
        raise ValueError("ITEM row is missing ID or internal name")
    damage = integer(row, "item_damage", "damage", "metadata", "meta")
    if damage < 0:
        raise ValueError(f"negative item damage for {name}")
    nbt = first(row, "nbt", "item_nbt", "tag")
    item = {
        "key": f"{name}@{damage}",
        "name": name,
        "damage": damage,
        "nbt": nbt,
        "localized_name": first(row, "localized_name", "display_name", "label"),
        "internal_name": first(row, "internal_name", "name"),
        "unlocalized_name": first(row, "unlocalized_name"),
        "mod_id": first(row, "mod_id", "mod"),
        "max_damage": integer(row, "max_damage", "durability"),
        "max_stack_size": integer(row, "max_stack_size", "stack_size", "max_stack"),
        "image_path": image_path(first(row, "image_file_path", "image_path", "icon_path")),
        "tooltips": tooltips.get(item_id, []),
    }
    return {key: value for key, value in item.items() if value not in ("", 0, []) or key in ("key", "name", "damage")}


def build_manifest(items: Iterable[dict[str, str]], tooltip_rows: Iterable[dict[str, str]], source: Path) -> dict[str, object]:
    tooltip_map: dict[str, list[tuple[int, str]]] = {}
    for row in tooltip_rows:
        item_id = first(row, "item_id", "id", "item_key")
        text = first(row, "tooltip", "text", "line", "value", "localized_text")
        if not item_id or not text:
            continue
        order = integer(row, "tooltip_order", "order", "line_order")
        tooltip_map.setdefault(item_id, []).append((order, text))
    ordered_tooltips = {key: [value for _, value in sorted(values)] for key, values in tooltip_map.items()}
    result: list[dict[str, object]] = []
    seen_records: set[str] = set()
    seen_identities: set[tuple[str, int, str]] = set()
    duplicate_exact = 0
    duplicate_conflict = 0
    for row in items:
        item = normalized_item(row, ordered_tooltips)
        record_key = json.dumps(item, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
        identity = (str(item["name"]), int(item["damage"]), str(item.get("nbt", "")))
        if record_key in seen_records:
            duplicate_exact += 1
            continue
        if identity in seen_identities:
            duplicate_conflict += 1
        seen_records.add(record_key)
        seen_identities.add(identity)
        result.append(item)
        if len(result) > MAX_MANIFEST_ITEMS:
            raise ValueError("NESQL catalog exceeds item limit")
    if not result:
        raise ValueError("NESQL export contains no ITEM rows")
    return {
        "version": MANIFEST_VERSION,
        "source": {
            "format": "nesql-hsqldb",
            "files": source_files(source),
            "duplicate_exact": duplicate_exact,
            "duplicate_conflict": duplicate_conflict,
        },
        "items": result,
    }


def run_reader(args: argparse.Namespace, source: Path, output: Path) -> None:
    with tempfile.TemporaryDirectory(prefix="ae-orbit-nesql-") as build_dir:
        classes = Path(build_dir) / "classes"
        classes.mkdir()
        subprocess.run([args.javac, "-encoding", "UTF-8", "-d", str(classes), str(READER)], check=True)
        subprocess.run(
            [args.java, "-cp", f"{classes}{os.pathsep}{args.hsqldb_jar}", "NesqlDumpReader", str(source), str(output)],
            check=True,
        )


def ensure_output_outside_source(source: Path, output: Path) -> None:
    if output == source or source in output.parents:
        raise ValueError("output must be outside the immutable NESQL source directory")


def main() -> int:
    args = parse_args()
    source = args.source.resolve()
    jar = args.hsqldb_jar.resolve()
    output = args.output.resolve()
    if not source.is_dir():
        raise SystemExit(f"source is not a directory: {source}")
    if not jar.is_file():
        raise SystemExit(f"HSQLDB jar not found: {jar}")
    try:
        ensure_output_outside_source(source, output)
    except ValueError as exc:
        raise SystemExit(str(exc)) from exc
    output.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="ae-orbit-nesql-") as temp_dir:
        ndjson = Path(temp_dir) / "nesql.ndjson"
        run_reader(args, source, ndjson)
        items, tooltips = read_rows(ndjson)
        manifest = build_manifest(items, tooltips, source)
        temporary_manifest = output / "manifest.json.new"
        temporary_image = output / "image.zip.new"
        temporary_manifest.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        shutil.copy2(source / "image.zip", temporary_image)
        os.replace(temporary_image, output / "image.zip")
        os.replace(temporary_manifest, output / "manifest.json")
        if args.keep_ndjson:
            shutil.copy2(ndjson, output / "nesql.ndjson")
    print(json.dumps({"output": str(output), "items": len(manifest["items"])}, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, ValueError, subprocess.CalledProcessError) as exc:
        print(f"import failed: {exc}", file=sys.stderr)
        raise SystemExit(1)
